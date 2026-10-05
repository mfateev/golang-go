// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"bytes"
	"internal/isolatebridge"
	"iter"
	"math/rand"
	randv2 "math/rand/v2"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func deterministicBoundary(t *testing.T) *isolatebridge.Boundary {
	t.Helper()
	b := isolatebridge.New()
	if err := b.EnableDeterminism(); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDeterministicRandomStreams(t *testing.T) {
	// Process configuration and host reseeding must not choose workflow values.
	t.Setenv("GODEBUG", "randautoseed=0,randseednop=0")
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 4; repetition++ {
			b := deterministicBoundary(t)
			want := rand.New(rand.NewSource(1))
			// A group retains its generator and Read remainder between entries.
			for _, size := range []int{1, 8, 2, 17, 3} {
				rand.Seed(int64(size + repetition))
				_ = randv2.Uint64()
				b.Run(func() {
					for i := 0; i < 8; i++ {
						if got, expected := rand.Uint64(), want.Uint64(); got != expected {
							t.Fatalf("GOMAXPROCS=%d: Uint64=%x, want %x", procs, got, expected)
						}
					}
					got, expected := make([]byte, size), make([]byte, size)
					if n, err := rand.Read(got); n != size || err != nil {
						t.Fatalf("Read=(%d,%v)", n, err)
					}
					_, _ = want.Read(expected)
					if !bytes.Equal(got, expected) {
						t.Fatalf("Read=%x, want %x", got, expected)
					}
					// Even with randseednop=0, isolate Seed follows Go's no-op rule.
					rand.Seed(200)
				})
				runtime.GC()
			}
			b.Run(func() {
				for _, expected := range []uint64{0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, 0x06c45d188009454f} {
					// Neither select polling nor legacy rand advances rand/v2.
					_ = rand.Int63()
					closed := make(chan struct{})
					close(closed)
					select {
					case <-closed:
					case <-closed:
					}
					if got := randv2.Uint64(); got != expected {
						t.Fatalf("rand/v2=%x, want %x", got, expected)
					}
				}
			})
		}
	}
}

func TestDeterministicPoolIgnoresGC(t *testing.T) {
	b := deterministicBoundary(t)
	var pool *sync.Pool
	count := 0
	b.Run(func() { pool = &sync.Pool{New: func() any { count++; return count }} })
	for i := 1; i <= 12; i++ {
		b.Run(func() {
			pool.Put(100)
			if got := pool.Get(); got != i {
				t.Fatalf("Get=%v, want %d", got, i)
			}
		})
		if i%2 == 0 {
			runtime.GC()
		}
	}
}

func TestDeterministicConcurrentRandom(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	var baseline []uint64
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 4; repetition++ {
			b := deterministicBoundary(t)
			var observations []uint64
			b.Run(func() {
				var wg sync.WaitGroup
				var records sync.Mutex
				for worker := 0; worker < 16; worker++ {
					wg.Go(func() {
						for round := 0; round < 32; round++ {
							value := rand.Uint64() ^ randv2.Uint64()
							var bytes [3]byte
							_, _ = rand.Read(bytes[:])
							records.Lock()
							observations = append(observations, value, uint64(bytes[0])<<16|uint64(bytes[1])<<8|uint64(bytes[2]))
							records.Unlock()
							runtime.Gosched()
						}
					})
				}
				wg.Wait()
			})
			waitBoundaryExit(t, b)
			if baseline == nil {
				baseline = observations
			} else if !slices.Equal(observations, baseline) {
				t.Fatalf("GOMAXPROCS=%d random observations changed", procs)
			}
		}
	}
}

func TestDeterministicLocalTimeZone(t *testing.T) {
	t.Setenv("TZ", "America/Los_Angeles")
	b := deterministicBoundary(t)
	if err := b.ConfigureTime(0, 77); err != nil {
		t.Fatal(err)
	}
	b.Run(func() {
		if got := time.Now().Format(time.RFC3339); got != "1970-01-01T00:00:00Z" {
			t.Errorf("Now=%s", got)
		}
		if time.Now().Location() != time.UTC {
			t.Error("Now did not return UTC")
		}
		for _, local := range []time.Time{time.Unix(0, 0), time.Date(1970, 1, 1, 0, 0, 0, 0, time.Local), time.Now().Local()} {
			name, offset := local.Zone()
			if offset != 0 || name != "UTC" {
				t.Errorf("Local zone=(%s,%d)", name, offset)
			}
		}
		zone := time.FixedZone("explicit", 7200)
		if _, offset := time.Now().In(zone).Zone(); offset != 7200 {
			t.Error("explicit zone was overridden")
		}
	})
}

func TestDeterministicLocationSources(t *testing.T) {
	t.Setenv("ZONEINFO", "/unavailable-isolate-zoneinfo")
	b := deterministicBoundary(t)
	if err := b.ConfigureTime(0, 77); err != nil {
		t.Fatal(err)
	}
	b.Run(func() {
		for _, name := range []string{"", "UTC", "Local"} {
			loc, err := time.LoadLocation(name)
			if err != nil {
				t.Fatal(err)
			}
			if zone, offset := time.Unix(0, 0).In(loc).Zone(); zone != "UTC" || offset != 0 {
				t.Errorf("LoadLocation(%q)=(%s,%d)", name, zone, offset)
			}
		}
		if loc, err := time.LoadLocation("America/New_York"); loc != nil || err == nil || !strings.Contains(err.Error(), "explicit time zone data") {
			t.Errorf("database lookup=(%v,%v)", loc, err)
		}
		// Minimal TZif v1: one UTC zone, no transitions. Supplying the bytes
		// explicitly remains supported independently of ZONEINFO and host files.
		data := make([]byte, 54)
		copy(data, "TZif")
		data[39], data[43] = 1, 4 // One type and four abbreviation bytes.
		copy(data[50:], "UTC\x00")
		loc, err := time.LoadLocationFromTZData("provided", data)
		if err != nil {
			t.Fatal(err)
		}
		if zone, offset := time.Now().In(loc).Zone(); zone != "UTC" || offset != 0 || loc.String() != "provided" {
			t.Errorf("explicit zone=(%s,%d), location=%s", zone, offset, loc)
		}
	})
}

func TestDeterministicClockRequired(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"Now", func() { time.Now() }},
		{"Since", func() { time.Since(time.Time{}) }},
		{"Sleep", func() { time.Sleep(time.Nanosecond) }},
		{"NewTimer", func() { time.NewTimer(time.Second) }},
		{"After", func() { time.After(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := deterministicBoundary(t)
			b.Run(func() {
				defer func() {
					message, ok := recover().(string)
					if !ok || !strings.Contains(message, "requires a host clock") {
						t.Errorf("panic=%q", message)
					}
				}()
				tc.run()
			})
		})
	}
	// No-op sleeps do not need any time source.
	deterministicBoundary(t).Run(func() { time.Sleep(0); time.Sleep(-1) })
}

func TestDeterministicSyncMapRange(t *testing.T) {
	deterministicBoundary(t).Run(func() {
		type key int64
		var m sync.Map
		for i := 63; i >= -64; i-- {
			m.Store(key(i), i)
		}
		var visited []key
		m.Range(func(k, v any) bool {
			i := k.(key)
			visited = append(visited, i)
			if i == -64 {
				m.Delete(key(-63))
				m.Store(key(-62), 900)
				m.Store(key(100), 100)
			}
			if i == -62 && v != 900 {
				t.Errorf("current value=%v", v)
			}
			return true
		})
		var expected []key
		for i := -64; i <= 63; i++ {
			if i != -63 {
				expected = append(expected, key(i))
			}
		}
		if !slices.Equal(visited, expected) {
			t.Fatalf("range=%v", visited)
		}
		calls := 0
		m.Range(func(_, _ any) bool { calls++; return false })
		if calls != 1 {
			t.Fatalf("early stop called %d times", calls)
		}
		m.Range(func(_, _ any) bool { m.Clear(); return true })
		m.Range(func(_, _ any) bool { t.Error("Clear retained an entry"); return true })
		for _, keys := range [][]any{{uint64(1 << 63), uint64(0), ^uint64(0)}, {"z", "a", "m"}} {
			for _, k := range keys {
				m.Store(k, k)
			}
			var got []any
			m.Range(func(k, _ any) bool { got = append(got, k); return true })
			if _, ok := keys[0].(string); ok {
				if !slices.Equal(got, []any{"a", "m", "z"}) {
					t.Errorf("strings=%v", got)
				}
			} else if !slices.Equal(got, []any{uint64(0), uint64(1 << 63), ^uint64(0)}) {
				t.Errorf("unsigned=%v", got)
			}
			m.Clear()
		}
	})
}

func TestDeterministicSyncMapKeyWidths(t *testing.T) {
	type signed int16
	type unsigned uint32
	for _, keys := range [][]any{
		{int(-2), int(0), int(3)}, {int8(-128), int8(0), int8(127)},
		{int16(-32768), int16(0), int16(32767)}, {int32(-1 << 31), int32(0), int32(1<<31 - 1)},
		{int64(-1 << 63), int64(0), int64(1<<63 - 1)},
		{uint(0), uint(1), ^uint(0)}, {uint8(0), uint8(1), uint8(255)},
		{uint16(0), uint16(1), uint16(65535)}, {uint32(0), uint32(1), ^uint32(0)},
		{uint64(0), uint64(1), ^uint64(0)},
		{signed(-32768), signed(0), signed(32767)}, {unsigned(0), unsigned(1), ^unsigned(0)},
	} {
		deterministicBoundary(t).Run(func() {
			var m sync.Map
			for i := len(keys) - 1; i >= 0; i-- {
				m.Store(keys[i], i)
			}
			index := 0
			m.Range(func(k, v any) bool {
				if k != keys[index] || v != index {
					t.Errorf("%T entry=(%v,%v), want (%v,%d)", k, k, v, keys[index], index)
				}
				index++
				return true
			})
			if index != len(keys) {
				t.Errorf("visited %d keys", index)
			}
		})
	}
}

func TestDeterministicPull(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	var baseline []int
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 5; repetition++ {
			b := deterministicBoundary(t)
			var trace []int
			b.Run(func() {
				var wg sync.WaitGroup
				var records sync.Mutex
				record := func(value int) { records.Lock(); trace = append(trace, value); records.Unlock() }
				for worker := 0; worker < 12; worker++ {
					wg.Go(func() {
						next, stop := iter.Pull(func(yield func(int) bool) {
							defer func() { record(1000 + worker) }()
							for i := 0; i < 8; i++ {
								record(worker*100 + i)
								runtime.Gosched()
								if !yield(i) {
									return
								}
							}
						})
						defer stop()
						for i := 0; i < 5; i++ {
							v, ok := next()
							if !ok || v != i {
								t.Errorf("next=(%d,%v), want %d", v, ok, i)
							}
							record(2000 + worker*100 + i)
						}
					})
				}
				wg.Wait()
				next, stop := iter.Pull2(func(yield func(string, int) bool) { yield("a", 1); yield("b", 2) })
				defer stop()
				for _, k := range []string{"a", "b"} {
					key, _, ok := next()
					if !ok || key != k {
						t.Errorf("Pull2=(%s,%v)", key, ok)
					}
				}
				if k, v, ok := next(); ok || k != "" || v != 0 {
					t.Errorf("exhausted=(%s,%d,%v)", k, v, ok)
				}
				stop()
				stop()
				ran := false
				_, stopBeforeNext := iter.Pull(func(yield func(int) bool) { ran = true; yield(1) })
				stopBeforeNext()
				if ran {
					t.Error("stopped iterator ran")
				}
				func() {
					defer func() {
						if got := recover(); got != "iterator failure" {
							t.Errorf("panic=%v", got)
						}
					}()
					next, stop := iter.Pull(func(func(int) bool) { panic("iterator failure") })
					defer stop()
					next()
				}()
			})
			waitBoundaryExit(t, b)
			if baseline == nil {
				baseline = trace
			} else if !slices.Equal(trace, baseline) {
				t.Fatalf("GOMAXPROCS=%d trace changed", procs)
			}
		}
	}
}

func TestDeterministicPullRevocation(t *testing.T) {
	for _, mode := range []string{"unstarted", "yielded", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			b := deterministicBoundary(t)
			ready := make(chan struct{})
			var deferred atomic.Bool
			go b.Run(func() {
				next, stop := iter.Pull(func(yield func(int) bool) {
					defer deferred.Store(true)
					if mode == "blocked" {
						close(ready)
						var never chan struct{}
						<-never
					}
					yield(1)
				})
				defer stop()
				if mode != "unstarted" {
					next()
				}
				if mode != "blocked" {
					close(ready)
				}
				var never chan struct{}
				<-never
			})
			<-ready
			if err := b.Suspend(); err != nil {
				t.Fatal(err)
			}
			b.Stop()
			waitBoundaryExit(t, b)
			if deferred.Load() {
				t.Fatal("revocation ran iterator defer")
			}
		})
	}
}

func TestDeterministicImmediateTimer(t *testing.T) {
	b := deterministicBoundary(t)
	if err := b.ConfigureTime(1e9, 77); err != nil {
		t.Fatal(err)
	}
	b.Run(func() {
		for _, duration := range []time.Duration{0, -time.Second} {
			timer := time.NewTimer(duration)
			if len(timer.C) != 0 || cap(timer.C) != 0 || reflect.ValueOf(timer.C).Len() != 0 || reflect.ValueOf(timer.C).Cap() != 0 {
				t.Fatal("timer channel exposes its buffer")
			}
			select {
			case fired := <-timer.C:
				if fired.UnixNano() != 1e9 {
					t.Errorf("fired at %v", fired)
				}
			default:
				t.Fatal("non-positive timer was not immediately ready")
			}
			if timer.Stop() {
				t.Error("Stop after receive returned true")
			}
			if timer.Reset(0) {
				t.Error("Reset after receive returned true")
			}
			if !timer.Reset(0) {
				t.Error("Reset with pending delivery returned false")
			}
			if !timer.Stop() {
				t.Error("Stop with pending delivery returned false")
			}
			if timer.Stop() {
				t.Error("second Stop returned true")
			}
			select {
			case <-timer.C:
				t.Error("Stop retained a stale value")
			default:
			}
		}
		called := make(chan struct{}, 1)
		callback := time.AfterFunc(0, func() { called <- struct{}{} })
		if !callback.Stop() {
			t.Error("Stop before callback returned false")
		}
		if callback.Reset(0) {
			t.Error("Reset of stopped callback returned true")
		}
		<-called
		if callback.Stop() {
			t.Error("Stop after callback returned true")
		}
	})
	waitBoundaryExit(t, b)
}

func TestDeterministicTimerUndoDelivery(t *testing.T) {
	for _, mode := range []string{"Stop", "Reset"} {
		t.Run(mode, func(t *testing.T) {
			b := deterministicBoundary(t)
			if err := b.ConfigureTime(1e9, 77); err != nil {
				t.Fatal(err)
			}
			go b.Run(func() {
				timer := time.NewTimer(time.Second)
				_, _ = b.Call(11, nil)
				if mode == "Stop" {
					if !timer.Stop() {
						t.Error("Stop with pending delivery returned false")
					}
					select {
					case <-timer.C:
						t.Error("Stop retained a stale value")
					default:
					}
				} else {
					if !timer.Reset(0) {
						t.Error("Reset with pending delivery returned false")
					}
					if got := <-timer.C; got.UnixNano() != 3e9 {
						t.Errorf("Reset retained old time %v", got)
					}
				}
			})
			control := <-b.Commands()
			timer := <-b.Commands()
			if control.Op != 11 || timer.Op != 77 {
				t.Fatal("unexpected timer handshake")
			}
			if err := b.Suspend(); err != nil {
				t.Fatal(err)
			}
			if err := b.AdvanceTime(2e9); err != nil {
				t.Fatal(err)
			}
			timer.Reply(nil, nil)
			if err := b.Resume(); err != nil {
				t.Fatal(err)
			}
			if err := b.Suspend(); err != nil {
				t.Fatal(err)
			}
			if err := b.AdvanceTime(3e9); err != nil {
				t.Fatal(err)
			}
			control.Reply(nil, nil)
			if err := b.Resume(); err != nil {
				t.Fatal(err)
			}
			waitBoundaryExit(t, b)
		})
	}
}

func TestDeterministicPullGoexit(t *testing.T) {
	b := deterministicBoundary(t)
	finished := make(chan struct{})
	var returned atomic.Bool
	go b.Run(func() {
		defer close(finished)
		next, stop := iter.Pull(func(func(int) bool) { runtime.Goexit() })
		defer stop()
		next()
		returned.Store(true)
	})
	<-finished
	waitBoundaryExit(t, b)
	if returned.Load() {
		t.Fatal("iterator Goexit returned from next")
	}
}
