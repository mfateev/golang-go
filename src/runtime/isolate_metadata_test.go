// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/testenv"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestIsolateMetadataGCStartup(t *testing.T) {
	const child = "GO_ISOLATE_METADATA_GC_STARTUP"
	if os.Getenv(child) != "1" {
		cmd := testenv.Command(t, testenv.Executable(t), "-test.run=^TestIsolateMetadataGCStartup$", "-test.v")
		cmd.Env = append(cmd.Environ(), child+"=1", "GOGC=off", "GOMAXPROCS=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cold GC startup: %v\n%s", err, output)
		}
		return
	}
	if got := runtime.IsolateMetadataGCWorkersForTest(); got != 0 {
		t.Fatalf("cold process already has %d GC workers", got)
	}
	group := runtime.IsolateMetadataGroupForTest()
	for _, procs := range []int{1, 4} {
		runtime.GOMAXPROCS(procs)
		debug.SetGCPercent(1)
		runtime.IsolateMetadataRunForTest(group, 709, func() {
			runtime.IsolateMetadataScopeForTest(func() {
				// This allocation must start the first workers, and subsequently
				// add workers after GOMAXPROCS increases, on the service caller.
				value := runtime.IsolateMetadataBytesForTest(64 << 20)
				if got := runtime.IsolateMetadataGCWorkersForTest(); got < int32(procs) {
					t.Fatalf("GC workers=%d, want at least %d", got, procs)
				}
				if got := runtime.IsolateMetadataLiveForTest(group); got != 1 {
					t.Fatalf("runtime workers inherited the isolate: live=%d", got)
				}
				if runtime.IsolateMetadataOwnerForTest() != 0 || runtime.IsolateMetadataDepthForTest() != 1 {
					t.Fatal("GC startup changed metadata privileges")
				}
				runtime.KeepAlive(value)
			})
		})
		debug.SetGCPercent(-1)
		runtime.GC()
	}
}

func TestIsolateMetadataRejectUserGoroutine(t *testing.T) {
	runtime.IsolateMetadataRunForTest(runtime.IsolateMetadataGroupForTest(), 710, func() {
		defer func() {
			if got := recover(); got != "isolate: metadata services cannot start goroutines" {
				t.Fatalf("goroutine restriction: got %v", got)
			}
			if runtime.IsolateMetadataOwnerForTest() != 710 || runtime.IsolateMetadataDepthForTest() != 0 {
				t.Fatal("goroutine rejection leaked metadata privileges")
			}
		}()
		runtime.IsolateMetadataScopeForTest(func() { go func() {}() })
	})
}

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

func TestIsolateMetadataPanicOwnership(t *testing.T) {
	type panicText string
	for _, named := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			group := runtime.IsolateMetadataGroupForTest()
			owner := nextAllocTestOwner()
			var unlocked bool
			var mu sync.Mutex
			runtime.IsolateMetadataRunForTest(group, owner, func() {
				defer func() {
					got := recover()
					if got == nil {
						t.Fatal("lost service panic")
					}
					if !unlocked {
						t.Error("panic copied before service lock cleanup")
					}
					if runtime.IsolateMetadataOwnerForTest() != owner || runtime.IsolateMetadataDepthForTest() != 0 {
						t.Error("panic retained service privileges")
					}
					if (reflect.TypeOf(got) == reflect.TypeFor[panicText]()) != named {
						t.Errorf("panic type %T, named=%v", got, named)
					}
					text := reflect.ValueOf(got).String()
					if text != strings.Repeat("metadata panic text ", 32) {
						t.Error("panic text changed")
					}
					box := (*[2]unsafe.Pointer)(unsafe.Pointer(&got))[1]
					for _, p := range []unsafe.Pointer{box, unsafe.Pointer(unsafe.StringData(text))} {
						if gotOwner, ok := runtime.IsolateAllocOriginForTest(p); !ok || gotOwner != owner {
							t.Errorf("panic allocation owner=(%d,%v), want %d", gotOwner, ok, owner)
						}
					}
				}()
				runtime.IsolateMetadataScopeForTest(func() {
					mu.Lock()
					defer func() {
						if runtime.IsolateMetadataOwnerForTest() != 0 {
							t.Error("lock cleanup ran outside service owner")
						}
						mu.Unlock()
						unlocked = true
					}()
					raise := func() {
						message := strings.Repeat("metadata panic text ", 32)
						if gotOwner, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(unsafe.StringData(message))); !ok || gotOwner != 0 {
							t.Fatal("service panic was not process-owned")
						}
						if named {
							panic(panicText(message))
						}
						panic(message)
					}
					if nested {
						runtime.IsolateMetadataScopeForTest(raise)
					} else {
						raise()
					}
				})
			})
			runtime.KeepAlive(group)
		}
	}
}
