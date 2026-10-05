// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

var isolateAllocTestID atomic.Uint64

func nextAllocTestOwner() uintptr { return uintptr(10000000 + isolateAllocTestID.Add(1)) }

func TestIsolateAllocOwners(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	var caches [2]unsafe.Pointer
	var retained [][]byte
	for _, procs := range []int{1, 2, 8, 1} {
		runtime.GOMAXPROCS(procs)
		for instance, group := range groups {
			runtime.IsolateMetadataRunForTest(group, owners[instance], func() {
				cache := runtime.IsolateCachePointerForTest()
				if caches[instance] != nil && cache != caches[instance] {
					t.Error("P migration changed instance cache")
				}
				caches[instance] = cache
				for _, size := range []int{1, 2, 7, 8, 9, 15, 16, 17, 24, 48, 80, 128, 256, 512, 1024, 2048, 32760, 32768, 65536, 3 << 20} {
					value := runtime.IsolateMetadataBytesForTest(size)
					for _, offset := range []int{0, size / 2, size - 1} {
						if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[offset])); !ok || got != owners[instance] {
							t.Errorf("owner=%d size=%d offset=%d: got (%d,%v)", owners[instance], size, offset, got, ok)
						}
					}
					value[size-1] = byte(instance + 1)
					retained = append(retained, value)
				}
			})
			// Force sweeping and central-list reuse between owners. Live spans
			// must keep their original tag, including partially occupied spans.
			runtime.GC()
		}
	}
	if caches[0] == caches[1] {
		t.Fatal("instances share an allocator cache")
	}
	for i, value := range retained {
		owner := owners[i/20%2]
		if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[0])); !ok || got != owner {
			t.Errorf("live span retagged: got (%d,%v), want %d", got, ok, owner)
		}
		if value[len(value)-1] != byte(i/20%2+1) {
			t.Error("live object changed across sweep")
		}
	}
	for _, size := range []int{1, 9, 16, 128, 4096, 65536} {
		value := runtime.IsolateMetadataBytesForTest(size)
		if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[0])); !ok || got != 0 {
			t.Errorf("host owner=(%d,%v)", got, ok)
		}
	}
	runtime.KeepAlive(groups)
}

func TestIsolateAllocConcurrentGC(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	var instances sync.WaitGroup
	for range 8 {
		instances.Go(func() {
			group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
			var firstCache atomic.Pointer[byte]
			runtime.IsolateMetadataRunForTest(group, owner, func() {
				var workers sync.WaitGroup
				for range 8 {
					workers.Go(func() {
						cache := (*byte)(runtime.IsolateCachePointerForTest())
						firstCache.CompareAndSwap(nil, cache)
						if firstCache.Load() != cache {
							t.Error("concurrent group members changed cache")
						}
						for n := 1; n <= 128; n++ {
							values := runtime.IsolatePointerSliceForTest(n)
							for i := range values {
								values[i] = new(int)
								*values[i] = i + 100
							}
							if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&values[0])); !ok || got != owner {
								t.Errorf("scan slice owner=(%d,%v), want %d", got, ok, owner)
							}
							for i, value := range values {
								if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(value)); !ok || got != owner {
									t.Errorf("scan element owner=(%d,%v), want %d", got, ok, owner)
								}
								if *value != i+100 {
									t.Error("GC lost live pointer")
								}
							}
							runtime.Gosched()
						}
					})
				}
				workers.Wait()
			})
		})
	}
	instances.Wait()
	close(stop)
	<-stopped
}

func TestIsolateAllocCacheRetirement(t *testing.T) {
	owner := nextAllocTestOwner()
	value := isolateCacheCycleForTest(owner)
	deadline := time.Now().Add(5 * time.Second)
	for runtime.IsolateCachePresentForTest(owner) {
		if time.Now().After(deadline) {
			t.Fatal("cyclic group retained its allocator cache")
		}
		runtime.GC()
		runtime.Gosched()
	}
	// Retiring a cache releases its spans to GC; it must not free a live heap.
	if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[17])); !ok || got != owner || value[17] != 99 {
		t.Fatalf("cache retirement damaged live heap: owner=(%d,%v) value=%d", got, ok, value[17])
	}
	runtime.KeepAlive(value)
}

func TestIsolateAllocStats(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
	var values [][]byte
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		for range 4096 {
			values = append(values, runtime.IsolateMetadataBytesForTest(96))
		}
	})
	runtime.ReadMemStats(&after)
	if got, want := after.TotalAlloc-before.TotalAlloc, uint64(4096*96); got < want {
		t.Fatalf("TotalAlloc delta=%d, want at least %d", got, want)
	}
	runtime.KeepAlive(values)
	runtime.KeepAlive(group)
}

//go:noinline
func isolateCacheCycleForTest(owner uintptr) []byte {
	group := runtime.IsolateMetadataGroupForTest()
	runtime.IsolateMetadataSetExitForTest(group, func(int) { runtime.KeepAlive(group) })
	var value []byte
	runtime.IsolateMetadataRunForTest(group, owner, func() { value = runtime.IsolateMetadataBytesForTest(1024); value[17] = 99 })
	return value
}
