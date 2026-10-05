// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestIsolateMetadataOwnership(t *testing.T) {
	group := runtime.IsolateMetadataGroupForTest()
	var metadata reflect.Type
	runtime.IsolateMetadataRunForTest(group, 701, func() {
		check := func(owner uintptr, depth uint32) {
			if got := runtime.IsolateMetadataOwnerForTest(); got != owner {
				t.Fatalf("owner=%d, want %d", got, owner)
			}
			if got := runtime.IsolateMetadataDepthForTest(); got != depth {
				t.Fatalf("depth=%d, want %d", got, depth)
			}
			value := runtime.IsolateMetadataBytesForTest(65536)
			if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[11])); !ok || got != owner {
				t.Fatalf("allocation owner=(%d,%v), want %d", got, ok, owner)
			}
			runtime.KeepAlive(value)
		}
		check(701, 0)
		runtime.IsolateMetadataScopeForTest(func() {
			check(0, 1)
			runtime.IsolateMetadataScopeForTest(func() { check(0, 2) })
			check(0, 1)
		})
		check(701, 0)
		metadata = reflect.StructOf([]reflect.StructField{{Name: "OwnedMetadataTest", Type: reflect.TypeFor[[3001]byte]()}})
		typePointer := (*[2]unsafe.Pointer)(unsafe.Pointer(&metadata))[1]
		if got, ok := runtime.IsolateAllocOriginForTest(typePointer); !ok || got != 0 {
			t.Fatalf("dynamic type metadata owner=(%d,%v), want process", got, ok)
		}
		check(701, 0)
		// Constructing metadata must not make the subsequent value process-owned.
		value := reflect.MakeSlice(reflect.SliceOf(metadata), 32, 32)
		if got, ok := runtime.IsolateAllocOriginForTest(value.UnsafePointer()); !ok || got != 701 {
			t.Fatalf("reflect value owner=(%d,%v), want 701", got, ok)
		}
	})
	if host := reflect.StructOf([]reflect.StructField{{Name: "OwnedMetadataTest", Type: reflect.TypeFor[[3001]byte]()}}); host != metadata {
		t.Fatal("host and isolate disagree on canonical type identity")
	}
}

func TestIsolateMetadataPanicRestoresOwner(t *testing.T) {
	runtime.IsolateMetadataRunForTest(runtime.IsolateMetadataGroupForTest(), 702, func() {
		func() {
			defer func() {
				if recover() != "metadata panic" {
					t.Error("lost metadata panic")
				}
			}()
			runtime.IsolateMetadataScopeForTest(func() { panic("metadata panic") })
		}()
		if runtime.IsolateMetadataOwnerForTest() != 702 || runtime.IsolateMetadataDepthForTest() != 0 {
			t.Fatal("panic leaked metadata privileges")
		}
	})
}

func TestIsolateMetadataRevocation(t *testing.T) {
	t.Run("concurrent", func(t *testing.T) { testIsolateMetadataRevocation(t, false) })
	t.Run("deterministic", func(t *testing.T) { testIsolateMetadataRevocation(t, true) })
}

func testIsolateMetadataRevocation(t *testing.T, deterministic bool) {
	for _, primitive := range []string{"Mutex", "RWMutex", "Cond", "Once"} {
		t.Run(primitive, func(t *testing.T) {
			group := runtime.IsolateMetadataGroupForTest()
			if deterministic && !runtime.IsolateMetadataDeterministicForTest(group) {
				t.Fatal("cannot enable deterministic dispatch")
			}
			var ready, serviceDone, afterService, userDefer atomic.Bool
			var mu sync.Mutex
			var rw sync.RWMutex
			cond := sync.NewCond(&mu)
			var once sync.Once
			var release func()
			switch primitive {
			case "Mutex":
				mu.Lock()
				release = mu.Unlock
			case "RWMutex":
				rw.Lock()
				release = rw.Unlock
			case "Cond":
				release = func() { mu.Lock(); cond.Signal(); mu.Unlock() }
			case "Once":
				mu.Lock()
				go once.Do(func() { ready.Store(true); mu.Lock(); mu.Unlock() })
				for !ready.Load() {
					runtime.Gosched()
				}
				ready.Store(false)
				release = mu.Unlock
			}
			go runtime.IsolateMetadataRunForTest(group, 703, func() {
				defer userDefer.Store(true)
				runtime.IsolateMetadataScopeForTest(func() {
					// A nested scope must not process Kill before the outer lock is released.
					defer serviceDone.Store(true)
					ready.Store(true)
					switch primitive {
					case "Mutex":
						mu.Lock()
						defer mu.Unlock()
					case "RWMutex":
						rw.RLock()
						defer rw.RUnlock()
					case "Cond":
						mu.Lock()
						defer mu.Unlock()
						cond.Wait()
					case "Once":
						once.Do(func() { t.Error("Once initializer ran twice") })
					}
					runtime.IsolateMetadataScopeForTest(func() { runtime.Gosched() })
				})
				afterService.Store(true)
			})
			deadline := time.Now().Add(5 * time.Second)
			for !ready.Load() || runtime.IsolateMetadataLiveForTest(group) != 1 || runtime.IsolateMetadataRunningForTest(group) != 0 {
				if time.Now().After(deadline) {
					t.Fatal("service did not block")
				}
				runtime.Gosched()
			}
			var suspended atomic.Bool
			if deterministic {
				go func() { runtime.IsolateMetadataSuspendForTest(group); suspended.Store(true) }()
				for !runtime.IsolateMetadataSuspendWaitingForTest(group) {
					if suspended.Load() {
						t.Fatal("Suspend returned while service was waiting on a shared lock")
					}
					if time.Now().After(deadline) {
						t.Fatal("Suspend did not register")
					}
					runtime.Gosched()
				}
			}
			runtime.IsolateMetadataRevokeForTest(group)
			if runtime.IsolateMetadataLiveForTest(group) != 1 || serviceDone.Load() {
				t.Fatal("Kill discarded a caller with a pending shared lock")
			}
			release()
			for runtime.IsolateMetadataLiveForTest(group) != 0 {
				if time.Now().After(deadline) {
					t.Fatal("Kill did not finish after service release")
				}
				runtime.Gosched()
			}
			if !serviceDone.Load() || afterService.Load() || userDefer.Load() {
				t.Fatalf("serviceDone=%v afterService=%v userDefer=%v", serviceDone.Load(), afterService.Load(), userDefer.Load())
			}
			if deterministic {
				for !suspended.Load() {
					if time.Now().After(deadline) {
						t.Fatal("revocation did not wake Suspend")
					}
					runtime.Gosched()
				}
			}
			// A later host user must be able to acquire the same shared locks.
			mu.Lock()
			mu.Unlock()
			rw.Lock()
			rw.Unlock()
		})
	}
}

func TestExecPreemptionLockOrder(t *testing.T) {
	runtime.ExecPreemptionLockOrderForTest()
}
