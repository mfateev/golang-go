// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
	"weak"
)

// These privileged fixtures model a trusted metadata service and its fault.
// Real application code cannot enter a service through the isolate API.
//
//go:linkname ownershipEnterMetadata runtime.isolateEnterMetadata
func ownershipEnterMetadata() uintptr

//go:linkname ownershipLeaveMetadata runtime.isolateLeaveMetadata
func ownershipLeaveMetadata(uintptr)

//go:linkname ownershipViolation runtime.isolateOwnershipViolation
func ownershipViolation(string)

//go:linkname ownershipAllocationOrigin runtime.isolateAllocOrigin
func ownershipAllocationOrigin(unsafe.Pointer) (uintptr, bool)

func TestOwnershipFaultTerminatesInstance(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		for _, where := range []string{"main", "child", "initializer", "initializer child"} {
			t.Run(where+"/"+map[bool]string{false: "concurrent", true: "deterministic"}[deterministic], func(t *testing.T) {
				processMap := map[int]int{1: 7}
				var recovered, deferred, after atomic.Bool
				fail := func() {
					defer deferred.Store(true)
					defer func() {
						if recover() != nil {
							recovered.Store(true)
						}
					}()
					processMap[1] = 9 // Runtime owner check must precede the write.
					after.Store(true)
				}
				program := Program{entry: isolatebridge.ProgramEntry{
					NewState: func() (func(func()), error) {
						if where == "initializer" {
							fail()
						}
						if where == "initializer child" {
							go fail()
							select {}
						}
						return func(fn func()) { fn() }, nil
					},
					Main: func() {
						if where == "child" {
							defer deferred.Store(true)
							go fail()
							_, _ = isolatebridge.Current().Call(1, nil)
							after.Store(true)
						} else {
							fail()
						}
					},
				}}
				i, err := New(Config{Program: program, Deterministic: deterministic})
				if where == "initializer" || where == "initializer child" {
					if i != nil {
						t.Fatal("New returned an instance after an ownership fault")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					if err = i.Start(); err != nil {
						t.Fatal(err)
					}
					err = i.Wait()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if killErr := i.Kill(ctx); killErr != nil {
						t.Fatal(killErr)
					}
					if i.boundary.LiveGoroutines() != 0 {
						t.Fatal("fault left live instance goroutines")
					}
					if startErr := i.Start(); startErr == nil {
						t.Fatal("faulted instance restarted")
					}
				}
				var fault *OwnershipError
				if !errors.As(err, &fault) || fault.Reason != "isolate: map write crosses owner boundary" {
					t.Fatalf("ownership error = %v", err)
				}
				if owner, heap := ownershipAllocationOrigin(unsafe.Pointer(fault)); !heap || owner != 0 {
					t.Fatalf("host error object owner=(%d,%v), want process heap", owner, heap)
				}
				if owner, heap := ownershipAllocationOrigin(unsafe.Pointer(unsafe.StringData(fault.Reason))); !heap || owner != 0 {
					t.Fatalf("host error text owner=(%d,%v), want process heap", owner, heap)
				}
				if recovered.Load() || deferred.Load() || after.Load() || processMap[1] != 7 {
					t.Fatalf("fault ran application code or mutated the process: recover=%v defer=%v after=%v map=%v", recovered.Load(), deferred.Load(), after.Load(), processMap)
				}
			})
		}
	}
}

func TestOwnershipFaultUnwindsMetadataLocks(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var shared sync.Mutex
		var cleaned, caught, serviceAfter, applicationAfter, applicationDefer atomic.Bool
		service := func() {
			owner := ownershipEnterMetadata()
			defer ownershipLeaveMetadata(owner)
			shared.Lock()
			defer shared.Unlock()
			defer cleaned.Store(true)
			inner := ownershipEnterMetadata()
			defer ownershipLeaveMetadata(inner)
			defer func() {
				if recover() != nil {
					caught.Store(true)
				}
			}()
			ownershipViolation("isolate: foreign heap reference publication")
			serviceAfter.Store(true)
		}
		program := Program{entry: isolatebridge.ProgramEntry{
			NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
			Main: func() {
				defer applicationDefer.Store(true)
				service()
				applicationAfter.Store(true)
			},
		}}
		i, err := New(Config{Program: program, Deterministic: deterministic})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var fault *OwnershipError
		if err := i.Wait(); !errors.As(err, &fault) {
			t.Fatalf("metadata fault = %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := i.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		if !shared.TryLock() {
			t.Fatal("metadata lock was abandoned")
		}
		shared.Unlock()
		if !cleaned.Load() || caught.Load() || serviceAfter.Load() || applicationAfter.Load() || applicationDefer.Load() {
			t.Fatalf("unwind: cleaned=%v caught=%v service=%v application=%v defer=%v", cleaned.Load(), caught.Load(), serviceAfter.Load(), applicationAfter.Load(), applicationDefer.Load())
		}
		// The process and another managed instance remain usable after failure.
		program.entry.Main = func() {}
		peer, err := New(Config{Program: program, Deterministic: deterministic})
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.Start(); err != nil {
			t.Fatal(err)
		}
		if err := peer.Wait(); err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(i)
	}
}

// Runtime services retain their process wait records through revocation. The
// host can finish the operation, then the outer service exit discards the G.
func TestOwnershipFaultMetadataChannelCleanup(t *testing.T) {
	for _, operation := range []string{"send", "receive", "select"} {
		t.Run(operation, func(t *testing.T) {
			ready := make(chan struct{})
			resume := make(chan struct{})
			never := make(chan struct{})
			var cleaned, after atomic.Bool
			var lock sync.Mutex
			program := Program{entry: isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main: func() {
					func() {
						owner := ownershipEnterMetadata()
						defer ownershipLeaveMetadata(owner)
						lock.Lock()
						defer lock.Unlock()
						defer cleaned.Store(true)
						close(ready)
						switch operation {
						case "send":
							resume <- struct{}{}
						case "receive":
							<-resume
						case "select":
							select {
							case <-resume:
							case <-never:
								panic("unexpected service wake")
							}
						}
					}()
					after.Store(true)
				},
			}}
			i, err := New(Config{Program: program})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			<-ready
			// Publish revocation before allowing the process operation to finish.
			i.boundary.BeginStop()
			i.boundary.WakeStoppedWaiters()
			if cleaned.Load() {
				t.Fatal("service wait was interrupted")
			}
			if operation == "send" {
				<-resume
			} else {
				close(resume)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := i.Kill(ctx); err != nil {
				t.Fatal(err)
			}
			if !cleaned.Load() || after.Load() {
				t.Fatal("service cleanup or discard failed")
			}
			if !lock.TryLock() {
				t.Fatal("process lock was abandoned")
			}
			lock.Unlock()
		})
	}
}

func TestOwnershipFaultConcurrentReportAndKill(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	for range 64 {
		processMap := map[int]int{1: 7}
		var applicationDefer atomic.Bool
		program := Program{entry: isolatebridge.ProgramEntry{
			NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
			Main: func() {
				gate := make(chan struct{})
				for range 32 {
					go func() {
						defer applicationDefer.Store(true)
						<-gate
						processMap[1] = 9
					}()
				}
				close(gate)
				_, _ = isolatebridge.Current().Call(1, nil)
			},
		}}
		i, err := New(Config{Program: program})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-i.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("fault did not complete")
		}
		// Compete with the asynchronous fault reporter's queue scan.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		killed := make(chan error, 2)
		for range 2 {
			go func() { killed <- i.Kill(ctx) }()
		}
		for range 2 {
			if err := <-killed; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		var fault *OwnershipError
		if err := i.Wait(); !errors.As(err, &fault) {
			t.Fatalf("fault completion cause lost: %v", err)
		}
		if i.boundary.LiveGoroutines() != 0 || applicationDefer.Load() || processMap[1] != 7 {
			t.Fatal("concurrent failure left live goroutines, ran defers, or changed process memory")
		}
	}
}

func TestOwnershipFaultKillWaitsForMetadataCleanup(t *testing.T) {
	var release, shared sync.Mutex
	release.Lock()
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			owner := ownershipEnterMetadata()
			defer ownershipLeaveMetadata(owner)
			shared.Lock()
			defer func() { release.Lock(); release.Unlock(); shared.Unlock() }()
			ownershipViolation("isolate: foreign heap reference publication")
		},
	}}
	i, err := New(Config{Program: program, Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-i.terminal // The diagnostic is published before process-service cleanup.
	select {
	case <-i.Done():
		t.Fatal("Done closed before the metadata service released its locks")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err = i.Kill(ctx)
	cancel()
	var pending *KillPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("Kill during cleanup = %v, want pending", err)
	}
	if pending.GoroutineID == 0 || pending.Stack == "" {
		t.Fatalf("parked cleanup has no best-effort diagnostic: %+v", pending)
	}
	release.Unlock()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	var fault *OwnershipError
	if err := i.Wait(); !errors.As(err, &fault) {
		t.Fatalf("fault = %v", err)
	}
	if !shared.TryLock() {
		t.Fatal("cleanup did not release the process lock")
	}
	shared.Unlock()
}

func TestOwnershipFaultOrdinaryPanicStillRecoverable(t *testing.T) {
	var recovered, deferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			defer deferred.Store(true)
			defer func() { recovered.Store(recover() == "ordinary application panic") }()
			panic("ordinary application panic")
		},
	}}
	i, err := New(Config{Program: program, Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	if !recovered.Load() || !deferred.Load() {
		t.Fatal("ordinary panic lost Go recovery or defers")
	}
}

func TestOwnershipFaultErrorsDoNotRetainInstances(t *testing.T) {
	for range 3 {
		runtime.GC()
		runtime.Gosched()
	}
	baseline := isolateAllocCacheCount()
	processMap := map[int]int{1: 7}
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			state := isolateCachedHeapState(64 << 10)
			state[0] = 1
			processMap[1] = 9
			runtime.KeepAlive(state)
		},
	}}
	var held []error
	var instances []weak.Pointer[Isolate]
	for range 64 {
		err, instance := ownershipFailureForGC(t, program)
		held = append(held, err)
		instances = append(instances, instance)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		remaining := 0
		for _, instance := range instances {
			if instance.Value() != nil {
				remaining++
			}
		}
		caches := isolateAllocCacheCount()
		if remaining == 0 && caches <= baseline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained errors kept instances/caches alive: instances=%d caches=%d baseline=%d", remaining, caches, baseline)
		}
		runtime.Gosched()
	}
	for _, err := range held {
		var fault *OwnershipError
		if !errors.As(err, &fault) || fault.Reason != "isolate: map write crosses owner boundary" {
			t.Fatalf("retained error changed: %v", err)
		}
	}
	runtime.KeepAlive(held)
}

// Do not keep the caller's last instance alive through a stack temporary.
//
//go:noinline
func ownershipFailureForGC(t *testing.T, program Program) (error, weak.Pointer[Isolate]) {
	t.Helper()
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	err = i.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	return err, weak.Make(i)
}
