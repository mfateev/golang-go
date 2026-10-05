// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"internal/isolatebridge"
	"runtime"
	"testing"
	"time"
	_ "unsafe" // for go:linkname
	"weak"
)

//go:linkname isolateAllocCacheCount runtime.isolateAllocCacheCount
func isolateAllocCacheCount() int

func TestIsolateCachedEviction(t *testing.T) {
	const stateBytes = 64 << 10
	count, batches := 1024, 3
	if testing.Short() {
		count, batches = 256, 2
	}
	// Warm runtime GC/finalizer machinery before taking the baseline. HeapAlloc
	// measures live objects; RSS and HeapSys can retain freed pages for reuse.
	for range 3 {
		runtime.GC()
		runtime.Gosched()
	}
	baselineCaches := isolateAllocCacheCount()
	baselineGoroutines := runtime.NumGoroutine()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			state := isolateCachedHeapState(stateBytes)
			state[0], state[len(state)-1] = 1, 2
			for {
				if _, err := Call(1, []byte{state[0], state[len(state)-1]}); err != nil {
					panic(err)
				}
				state[0]++
				runtime.KeepAlive(state)
			}
		},
	}}
	for batch := range batches {
		cached := make([]*Isolate, count)
		commands := make([]*Command, count)
		weakInstances := make([]weak.Pointer[Isolate], count)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		// Cleanup also covers an assertion failure during creation/resumption.
		t.Cleanup(func() {
			for _, instance := range cached {
				if instance != nil {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
					_ = instance.Kill(cleanupCtx)
					cleanupCancel()
				}
			}
			cancel()
		})
		next := func(instance *Isolate) *Command {
			select {
			case command := <-instance.Commands():
				return command
			case <-ctx.Done():
				t.Fatal("cached instance did not reach Call")
				return nil
			}
		}
		for index := range count {
			instance, err := New(Config{Program: program, Deterministic: true})
			if err != nil {
				t.Fatal(err)
			}
			cached[index], weakInstances[index] = instance, weak.Make(instance)
			if err := instance.Start(); err != nil {
				t.Fatal(err)
			}
			commands[index] = next(instance)
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
		}
		for range 3 {
			runtime.GC()
		}
		if got := isolateAllocCacheCount(); got < count {
			t.Fatalf("batch %d: cached allocator count=%d, want at least %d", batch, got, count)
		}
		var retained runtime.MemStats
		runtime.ReadMemStats(&retained)
		if retained.HeapAlloc < baseline.HeapAlloc+uint64(count*stateBytes*3/4) {
			t.Fatalf("batch %d: cached heap=%d, baseline=%d: state was not retained", batch, retained.HeapAlloc, baseline.HeapAlloc)
		}
		// Resume a sample after collection and validate actual state through copied
		// bytes. All other instances remain suspended until eviction.
		for index := 0; index < count; index += 31 {
			instance := cached[index]
			if weakInstances[index].Value() == nil {
				t.Fatalf("batch %d: live cached instance disappeared", batch)
			}
			commands[index].Reply(nil, nil)
			if err := instance.Resume(); err != nil {
				t.Fatal(err)
			}
			command := next(instance)
			if len(command.Payload) != 2 || command.Payload[0] != 2 || command.Payload[1] != 2 {
				t.Fatalf("batch %d: cached state changed: %v", batch, command.Payload)
			}
			commands[index] = command
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
		}
		for index, instance := range cached {
			if err := instance.Kill(ctx); err != nil {
				t.Fatal(err)
			}
			if err := instance.Wait(); err != errMainRevoked {
				t.Fatalf("batch %d: Wait=%v", batch, err)
			}
			if got := instance.boundary.LiveGoroutines(); got != 0 {
				t.Fatalf("batch %d: eviction left %d members", batch, got)
			}
			cached[index], commands[index] = nil, nil
		}
		cancel()
		deadline := time.Now().Add(10 * time.Second)
		for {
			runtime.GC()
			remaining := 0
			for _, instance := range weakInstances {
				if instance.Value() != nil {
					remaining++
				}
			}
			caches := isolateAllocCacheCount()
			goroutines := runtime.NumGoroutine()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			// A small allowance covers permanent runtime bookkeeping and test roots.
			// Even the short workload owns 16 MiB of state; leaking a batch exceeds it.
			if remaining == 0 && caches <= baselineCaches && goroutines <= baselineGoroutines+16 && after.HeapAlloc <= baseline.HeapAlloc+(8<<20) {
				t.Logf("batch=%d cached=%d heap: baseline=%d retained=%d after=%d caches=%d goroutines=%d", batch, count, baseline.HeapAlloc, retained.HeapAlloc, after.HeapAlloc, caches, goroutines)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("batch %d: eviction leaked: instances=%d caches=%d (baseline %d), goroutines=%d (baseline %d), heap=%d (baseline %d)", batch, remaining, caches, baselineCaches, goroutines, baselineGoroutines, after.HeapAlloc, baseline.HeapAlloc)
			}
			runtime.Gosched()
		}
	}
}

// Keep the cached payload on the heap: a constant-size buffer can otherwise
// live entirely on the goroutine stack and evade the live-heap assertion.
//
//go:noinline
func isolateCachedHeapState(size int) []byte { return make([]byte, size) }
