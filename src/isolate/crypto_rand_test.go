// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"io"
	"math/big"
	"math/rand"
	randv2 "math/rand/v2"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestDeterministicCryptoRand(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for _, seed := range [][32]byte{{}, {1}, {0xff, 0x23}} {
			for repetition := 0; repetition < 3; repetition++ {
				b := deterministicBoundary(t)
				if err := b.ConfigureRandom(seed); err != nil {
					t.Fatal(err)
				}
				want := randv2.NewChaCha8(seed)
				for index, size := range []int{0, 1, 0, 7, 2, 17, 4096, 3} {
					// Host entropy and GC must not consume
					// the byte stream or discard a partly consumed word.
					_, _ = crand.Read(make([]byte, 19))
					runtime.GC()
					b.Run(func() {
						got, expected := make([]byte, size), make([]byte, size)
						var n int
						var err error
						if index%2 == 0 {
							n, err = crand.Read(got)
						} else {
							n, err = io.ReadFull(crand.Reader, got)
						}
						if n != size || err != nil {
							t.Fatalf("Read=(%d,%v)", n, err)
						}
						_, _ = want.Read(expected)
						if !bytes.Equal(got, expected) {
							t.Fatalf("procs=%d size=%d: got %x want %x", procs, size, got, expected)
						}
					})
				}
				if err := b.ConfigureRandom([32]byte{9}); err == nil {
					t.Fatal("seed changed after initialization")
				}
			}
		}
	}
}

func TestDeterministicCryptoRandHelpers(t *testing.T) {
	var baseline []string
	for repetition := 0; repetition < 3; repetition++ {
		b := deterministicBoundary(t)
		var got []string
		b.Run(func() {
			for i := 0; i < 8; i++ {
				n, err := crand.Int(crand.Reader, big.NewInt(1000003))
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, n.String(), crand.Text())
			}
		})
		if repetition == 0 {
			baseline = got
		} else if !slices.Equal(got, baseline) {
			t.Fatal("Int/Text did not replay")
		}
	}
}

func TestDeterministicCryptoRandConcurrent(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	var baseline []byte
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for repetition := 0; repetition < 3; repetition++ {
			b := deterministicBoundary(t)
			var got []byte
			b.Run(func() {
				var wg sync.WaitGroup
				var records sync.Mutex
				for worker := 0; worker < 16; worker++ {
					wg.Go(func() {
						for round := 0; round < 32; round++ {
							var buf [17]byte
							if round%2 == 0 {
								_, _ = crand.Read(buf[:])
							} else {
								_, _ = io.ReadFull(crand.Reader, buf[:])
							}
							records.Lock()
							got = append(got, byte(worker))
							got = append(got, buf[:]...)
							records.Unlock()
							runtime.Gosched()
						}
					})
				}
				wg.Wait()
			})
			waitBoundaryExit(t, b)
			if baseline == nil {
				baseline = got
			} else if !bytes.Equal(got, baseline) {
				t.Fatalf("concurrent stream changed at procs=%d", procs)
			}
		}
	}
}

func TestDeterministicCryptoRandReadOnly(t *testing.T) {
	b := deterministicBoundary(t)
	seed := [32]byte{7}
	if err := b.ConfigureRandom(seed); err != nil {
		t.Fatal(err)
	}
	want := randv2.NewChaCha8(seed)
	checkWorkflow := func() {
		got, expected := make([]byte, 31), make([]byte, 31)
		_, _ = crand.Read(got)
		_, _ = want.Read(expected)
		if !bytes.Equal(got, expected) {
			t.Fatal("read-only handler advanced workflow randomness")
		}
	}
	b.Run(checkWorkflow)
	for request := 0; request < 3; request++ {
		done := make(chan struct{})
		go func() {
			defer close(done)
			b.Run(func() {
				_, _ = b.ReadOnlyCall(1, nil)
				got, expected := make([]byte, 31), make([]byte, 31)
				_, _ = io.ReadFull(crand.Reader, got)
				querySeed := seed
				querySeed[0] ^= 0x80
				queryReader := randv2.NewChaCha8(querySeed)
				var transport [32]byte
				_, _ = queryReader.Read(transport[:])
				_, _ = queryReader.Read(expected)
				if !bytes.Equal(got, expected) {
					t.Error("wrong scratch stream")
				}
			})
		}()
		command := <-b.Commands()
		if err := b.Suspend(); err != nil {
			t.Fatal(err)
		}
		command.Reply(nil, nil)
		if err := b.ResumeReadOnly(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("query did not finish")
		}
		if err := b.Resume(); err != nil {
			t.Fatal(err)
		}
		b.Run(checkWorkflow)
	}
}

func TestCryptoRandHostReaderUnchanged(t *testing.T) {
	original := crand.Reader
	defer func() { crand.Reader = original }()
	crand.Reader = bytes.NewReader([]byte("host-reader"))
	buf := make([]byte, 11)
	if n, err := crand.Read(buf); n != len(buf) || err != nil || string(buf) != "host-reader" {
		t.Fatalf("host reader changed: %q (%d,%v)", buf, n, err)
	}
}

type failingRandomReader struct{}
type failingRandomError struct{}

func (failingRandomError) Error() string { panic("reader error formatter must not run") }

func (failingRandomReader) Read([]byte) (int, error) {
	return 0, failingRandomError{}
}

func TestDeterministicCryptoRandReaderFailure(t *testing.T) {
	// Trusted boundary probes do not redirect package globals; restore the host
	// Reader after checking that the error cannot reach runtime.fatal.
	original := crand.Reader
	defer func() { crand.Reader = original }()
	b := deterministicBoundary(t)
	b.Run(func() {
		crand.Reader = failingRandomReader{}
		defer func() {
			if got := recover(); got != "isolate: forbidden operation crypto/rand.Read: random reader failed" {
				t.Fatalf("reader failure panic=%v", got)
			}
		}()
		_, _ = crand.Read(make([]byte, 17))
	})
}

func TestDeterministicCryptoRandPrimeRejected(t *testing.T) {
	b := deterministicBoundary(t)
	b.Run(func() {
		defer func() {
			if got := recover(); got != "isolate: forbidden operation crypto/rand.Prime" {
				t.Fatalf("Prime panic=%v", got)
			}
		}()
		_, _ = crand.Prime(crand.Reader, 64)
	})
}

func TestDeterministicSingleRandomStream(t *testing.T) {
	b := deterministicBoundary(t)
	seed := [32]byte{23}
	if err := b.ConfigureRandom(seed); err != nil {
		t.Fatal(err)
	}
	expected := randv2.NewChaCha8(seed)
	next := func() uint64 {
		var buf [8]byte
		_, _ = expected.Read(buf[:])
		return binary.LittleEndian.Uint64(buf[:])
	}
	b.Run(func() {
		var first [3]byte
		_, _ = crand.Read(first[:])
		var wantFirst [3]byte
		_, _ = expected.Read(wantFirst[:])
		if first != wantFirst {
			t.Fatal("wrong initial bytes")
		}
		if got, want := randv2.Uint64(), next(); got != want {
			t.Fatalf("rand/v2=%x want %x", got, want)
		}
		if got, want := rand.Uint64(), next(); got != want {
			t.Fatalf("rand=%x want %x", got, want)
		}
		closed := make(chan struct{})
		close(closed)
		select {
		case <-closed:
		case <-closed:
		}
		_ = next()
		_ = next() // Two select shuffle draws.
		var final, wantFinal [19]byte
		_, _ = crand.Reader.Read(final[:])
		_, _ = expected.Read(wantFinal[:])
		if final != wantFinal {
			t.Fatalf("select did not share byte stream: %x want %x", final, wantFinal)
		}
	})
}
