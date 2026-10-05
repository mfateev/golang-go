// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"internal/isolatebridge"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDeterministicSelectSequence(t *testing.T) {
	run := func() []int {
		b := isolatebridge.New()
		if err := b.EnableDeterminism(); err != nil {
			t.Fatal(err)
		}
		var result []int
		b.Run(func() {
			a, c := make(chan int, 1), make(chan int, 1)
			closed := make(chan int)
			close(closed)
			var disabled chan int
			for i := 0; i < 128; i++ {
				a <- 1
				select {
				case c <- 2:
					result = append(result, 0)
				case <-a:
					result = append(result, 1)
				case <-closed:
					result = append(result, 2)
				case <-disabled:
					panic("nil channel selected")
				default:
					panic("default selected with ready cases")
				}
				select {
				case <-a:
				default:
				}
				select {
				case <-c:
				default:
				}
			}
			cases := []reflect.SelectCase{
				{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(closed)},
				{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(closed)},
			}
			for i := 0; i < 128; i++ {
				index, _, _ := reflect.Select(cases)
				result = append(result, index)
			}
		})
		return result
	}
	want := run()
	for i := 0; i < 10; i++ {
		if got := run(); !slices.Equal(got, want) {
			t.Fatalf("select stream differs in instance %d", i)
		}
	}
	for _, selected := range []int{0, 1, 2} {
		if !slices.Contains(want[:128], selected) {
			t.Fatalf("shuffle never selected ready case %d", selected)
		}
	}
}

// Exercise FIFO dispatch with repeated user parks and GC-driven suspension.
// Compare full execution traces rather than just the eventual result. Two
// independent groups and a GC goroutine compete for the process's Ps.
func TestDeterministicDispatcherStress(t *testing.T) {
	const workers, rounds = 48, 64
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	oldGC := debug.SetGCPercent(20)
	defer debug.SetGCPercent(oldGC)
	run := func() []uint64 {
		b := isolatebridge.New()
		if err := b.EnableDeterminism(); err != nil {
			panic(err)
		}
		var trace []uint64
		b.Run(func() {
			var wg sync.WaitGroup
			var records sync.Mutex
			var rw sync.RWMutex
			var barrier sync.Mutex
			cond := sync.NewCond(&barrier)
			arrived, generation := 0, 0
			var active atomic.Int32
			var shared uint64
			wg.Add(workers)
			for id := 0; id < workers; id++ {
				go func() {
					defer wg.Done()
					left, right := make(chan int, 1), make(chan int, 1)
					left <- 1
					right <- 2
					m := map[int]int{9: 3, -4: 7, 2: 11}
					var retained [][]byte
					for round := 0; round < rounds; round++ {
						if active.Add(1) != 1 {
							panic("multiple user goroutines executing in one isolate")
						}
						data := make([]byte, 4096)
						data[round] = byte(id)
						retained = append(retained, data)
						var hash uint64
						for key, value := range m {
							hash = hash*31 + uint64(key+value)
						}
						for i := 0; i < 1024; i++ {
							hash = (hash ^ (hash >> 13)) * 0x9e3779b97f4a7c15
						}
						chosen := 0
						select {
						case <-left:
							chosen = 1
							left <- 1
						case <-right:
							chosen = 2
							right <- 2
						}
						if active.Add(-1) != 0 {
							panic("execution token overlap")
						}
						rw.Lock()
						shared++
						rw.Unlock()
						rw.RLock()
						observed := shared
						rw.RUnlock()
						records.Lock()
						trace = append(trace, uint64(id)<<48|uint64(round)<<32|uint64(chosen)<<24|observed<<8|(hash&255))
						records.Unlock()
						runtime.Gosched()
						if round%8 == 7 {
							barrier.Lock()
							current := generation
							arrived++
							if arrived == workers {
								arrived = 0
								generation++
								cond.Broadcast()
							} else {
								for current == generation {
									cond.Wait()
								}
							}
							barrier.Unlock()
						}
					}
					runtime.KeepAlive(retained)
				}()
			}
			wg.Wait()
		})
		return trace
	}
	want := run()
	if len(want) != workers*rounds {
		t.Fatalf("trace length = %d", len(want))
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
				runtime.Gosched()
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 3; repetition++ {
			results := make(chan []uint64, 2)
			for instance := 0; instance < 2; instance++ {
				go func() { results <- run() }()
			}
			for instance := 0; instance < 2; instance++ {
				if got := <-results; !slices.Equal(got, want) {
					for index := range min(len(got), len(want)) {
						if got[index] != want[index] {
							t.Fatalf("GOMAXPROCS=%d repetition=%d instance=%d: trace differs at %d: %#x != %#x", procs, repetition, instance, index, got[index], want[index])
						}
					}
					t.Fatalf("trace lengths differ: %d != %d", len(got), len(want))
				}
			}
		}
	}
}

func TestDeterministicGoroutineFIFO(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for iteration := 0; iteration < 20; iteration++ {
			b := isolatebridge.New()
			if err := b.EnableDeterminism(); err != nil {
				t.Fatal(err)
			}
			var order []int
			var mu sync.Mutex
			b.Run(func() {
				var wg sync.WaitGroup
				wg.Add(4)
				for i := 0; i < 4; i++ {
					go func() {
						defer wg.Done()
						mu.Lock()
						order = append(order, i)
						mu.Unlock()
						runtime.Gosched()
						mu.Lock()
						order = append(order, i+10)
						mu.Unlock()
					}()
				}
				mu.Lock()
				order = append(order, 99)
				mu.Unlock()
				wg.Wait()
			})
			if !slices.Equal(order, []int{99, 0, 1, 2, 3, 10, 11, 12, 13}) {
				t.Fatalf("GOMAXPROCS=%d run %d: %v", procs, iteration, order)
			}
		}
	}
}

func TestDeterministicMapIteration(t *testing.T) {
	b := isolatebridge.New()
	if err := b.EnableDeterminism(); err != nil {
		t.Fatal(err)
	}
	b.Run(func() {
		type key int64
		m := make(map[key]int)
		for i := 999; i >= -1000; i-- {
			m[key(i)] = i
		}
		index := -1000
		for k, v := range m {
			if k != key(index) || v != index {
				t.Fatalf("entry = %v:%v, want %d", k, v, index)
			}
			index++
		}
		if index != 1000 {
			t.Fatalf("iteration ended at %d", index)
		}
		mutating := map[int]int{1: 1, 2: 2, 3: 3, 4: 4}
		var visited []int
		for k, v := range mutating {
			visited = append(visited, k)
			if k == 1 {
				delete(mutating, 2)
				mutating[3] = 30
				for i := 5; i < 100; i++ {
					mutating[i] = i
				}
			}
			if k == 3 && v != 30 {
				t.Fatalf("updated value = %d", v)
			}
		}
		if !slices.Equal(visited, []int{1, 3, 4}) {
			t.Fatalf("mutating range = %v", visited)
		}
		stringsMap := map[string]int{"z": 1, "a": 2, "m": 3}
		iterator := reflect.ValueOf(stringsMap).MapRange()
		var keys []string
		for iterator.Next() {
			keys = append(keys, iterator.Key().String())
		}
		if !slices.Equal(keys, []string{"a", "m", "z"}) {
			t.Fatalf("reflected keys = %v", keys)
		}
		iterator.Reset(reflect.ValueOf(map[string]int{"b": 1, "a": 2}))
		if !iterator.Next() || iterator.Key().String() != "a" {
			t.Fatal("reset lost canonical order")
		}
		unsigned := map[uint64]int{^uint64(0): 1, 0: 2, 1 << 63: 3}
		var uintKeys []uint64
		for k := range unsigned {
			uintKeys = append(uintKeys, k)
		}
		if !slices.Equal(uintKeys, []uint64{0, 1 << 63, ^uint64(0)}) {
			t.Fatalf("unsigned keys = %v", uintKeys)
		}
		cleared := map[int]int{1: 1, 2: 2, 3: 3}
		count := 0
		for range cleared {
			count++
			clear(cleared)
		}
		if count != 1 {
			t.Fatalf("clear visited %d keys", count)
		}
	})
}

func TestDeterministicMapUnsupportedKeys(t *testing.T) {
	for _, value := range []any{map[float64]int{1: 1}, map[*int]int{}, map[any]int{}, map[[1]int]int{}} {
		func() {
			defer func() {
				if got := recover(); got == nil || !strings.Contains(got.(string), "integer or string keys") {
					t.Errorf("unsupported %T: panic = %v", value, got)
				}
			}()
			b := isolatebridge.New()
			if err := b.EnableDeterminism(); err != nil {
				t.Fatal(err)
			}
			b.Run(func() { reflect.ValueOf(value).MapRange().Next() })
		}()
	}
}

// Dynamic sync.Map keys must satisfy the same ordering restriction as native
// maps. Reject the complete snapshot before invoking any callback.
func TestDeterministicUnsupportedAPIs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     func()
		message string
	}{
		{"sync.Map.Range.pointer", func() { var m sync.Map; m.Store(new(int), 1); m.Range(func(_, _ any) bool { return true }) }, "sync.Map.Range"},
		{"sync.Map.Range.mixed", func() { var m sync.Map; m.Store(1, 1); m.Store("a", 2); m.Range(func(_, _ any) bool { return true }) }, "sync.Map.Range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := isolatebridge.New()
			if err := b.EnableDeterminism(); err != nil {
				t.Fatal(err)
			}
			b.Run(func() {
				defer func() {
					failure := recover()
					if message, ok := failure.(string); !ok || !strings.Contains(message, tc.message) {
						t.Errorf("panic = %v", failure)
					}
				}()
				tc.run()
			})
			// The guard must not affect ordinary host Go.
			tc.run()
		})
	}
}

// Holding locks across explicit yields forces actual Mutex and RWMutex parks;
// the unbuffered channel forces a sender/receiver handoff on every round.
func TestDeterministicDispatcherContendedLocks(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	run := func() []int {
		b := isolatebridge.New()
		if err := b.EnableDeterminism(); err != nil {
			t.Fatal(err)
		}
		var trace []int
		b.Run(func() {
			var mu sync.Mutex
			var rw sync.RWMutex
			var wg sync.WaitGroup
			exchange := make(chan int)
			wg.Go(func() {
				for round := 0; round < 32; round++ {
					value := <-exchange
					mu.Lock()
					trace = append(trace, 1000+value)
					mu.Unlock()
				}
			})
			for id := 0; id < 16; id++ {
				wg.Go(func() {
					for round := 0; round < 8; round++ {
						rw.Lock()
						runtime.Gosched()
						mu.Lock()
						runtime.Gosched()
						trace = append(trace, id)
						mu.Unlock()
						rw.Unlock()
						rw.RLock()
						runtime.Gosched()
						rw.RUnlock()
						if id < 4 {
							select {
							case exchange <- id:
							}
						}
					}
				})
			}
			wg.Wait()
		})
		return trace
	}
	want := run()
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 5; repetition++ {
			if got := run(); !slices.Equal(got, want) {
				t.Fatalf("GOMAXPROCS=%d repetition=%d: lock/channel trace differs", procs, repetition)
			}
		}
	}
}
