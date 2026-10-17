// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.isolates

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func hostIntegrationInstance(t *testing.T, deterministic bool, fn func()) *Isolate {
	t.Helper()
	p := Program{entry: isolatebridge.ProgramEntry{MetadataVersion: MetadataVersion,
		Main: fn, NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil }}}
	i, err := New(Config{Program: p, Deterministic: deterministic})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := i.Kill(ctx); err != nil {
			t.Error(err)
		}
	})
	return i
}

func hostIntegrationOwner(p unsafe.Pointer) uintptr {
	owner, ok := ownershipAllocationOrigin(p)
	if !ok {
		panic("expected a heap allocation")
	}
	return owner
}

type hostIntegrationRecord struct {
	Text  string
	Bytes []byte
	Map   map[string]*hostIntegrationRecord
	Value any
	Next  *hostIntegrationRecord
	Fn    func() *hostIntegrationRecord
}

func TestHostAllocateDomains(t *testing.T) {
	i := hostIntegrationInstance(t, true, func() {})
	a := i.Allocator()
	host := &hostIntegrationRecord{Text: "metadata"}
	var inner *hostIntegrationRecord
	record, err := Allocate(a, func() *hostIntegrationRecord {
		result := &hostIntegrationRecord{Text: strings.Repeat("value", 20), Bytes: make([]byte, 256)}
		result.Map = map[string]*hostIntegrationRecord{"cycle": result}
		result.Next, result.Value = result, result.Bytes
		result.Fn = func() *hostIntegrationRecord { return result }
		var err error
		inner, err = Allocate(HostAllocator(), func() *hostIntegrationRecord { return &hostIntegrationRecord{Text: host.Text} })
		if err != nil {
			panic(err)
		}
		result.Bytes[0] = 7
		if result.Map["cycle"] != result {
			panic("map cycle changed")
		}
		return result
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := hostIntegrationOwner(unsafe.Pointer(record))
	if owner == 0 || hostIntegrationOwner(unsafe.Pointer(&record.Bytes[0])) != owner || hostIntegrationOwner(unsafe.Pointer(inner)) != 0 {
		t.Fatal("nested allocation did not restore domains")
	}
	if record.Fn() != record || record.Next != record || record.Value.([]byte)[0] != 7 {
		t.Fatal("constructor graph changed")
	}
	func() {
		defer func() {
			if recover() != "constructor" {
				t.Error("constructor panic changed")
			}
		}()
		_, _ = Allocate(a, func() int { panic("constructor") })
	}()
	ordinary := new(hostIntegrationRecord)
	if hostIntegrationOwner(unsafe.Pointer(ordinary)) != 0 {
		t.Fatal("panic did not restore allocator")
	}
	cached, err := Allocate(a, func() *hostIntegrationRecord { return host })
	if err != nil || cached != host || hostIntegrationOwner(unsafe.Pointer(cached)) != 0 {
		t.Fatal("cached object was reowned")
	}
}

func TestHostAllocateLeaseAndRevocation(t *testing.T) {
	i := hostIntegrationInstance(t, true, func() {})
	a := i.Allocator()
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		value, err := Allocate(a, func() []byte {
			b := make([]byte, 128)
			close(entered)
			<-release
			b[0] = 3
			return b
		})
		if value != nil {
			result <- errors.New("revoked construction delivered a result")
			return
		}
		result <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) || pending.PendingHostWork != 1 {
		t.Fatalf("Kill = %v, want one construction lease", err)
	}
	ran := false
	if _, err := Allocate(a, func() int { ran = true; return 1 }); err != ErrRevoked || ran {
		t.Fatal("revoked allocator ran constructor")
	}
	close(release)
	if err := <-result; err != ErrRevoked {
		t.Fatalf("Allocate = %v", err)
	}
	<-i.Done()
}

func TestHostFunctionReentryAndNestedAllocation(t *testing.T) {
	var called atomic.Int32
	var nested func(func()) (int, error)
	nested = NewHostFunc(func(c HostContext, fn func()) (int, error) {
		if IsActive() {
			panic("host execution still active")
		}
		called.Add(1)
		c.Reenter(fn)
		return 11, nil
	})
	operation := NewHostFunc(func(c HostContext, fn func()) (*hostIntegrationRecord, error) {
		if IsActive() {
			panic("host execution still active")
		}
		a := c.Allocator()
		return Allocate(a, func() *hostIntegrationRecord {
			c.Reenter(func() {
				if !IsActive() {
					panic("missing application execution")
				}
				n, err := nested(fn)
				if err != nil || n != 11 {
					panic("nested call failed")
				}
			})
			return &hostIntegrationRecord{Bytes: make([]byte, 128)}
		})
	})
	for pass := 0; pass < 2; pass++ {
		i := hostIntegrationInstance(t, true, func() {
			value, err := operation(func() { called.Add(10) })
			if err != nil || hostIntegrationOwner(unsafe.Pointer(value)) == 0 {
				panic("wrong result domain")
			}
			if !IsActive() {
				panic("application state not restored")
			}
		})
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	if called.Load() != 22 {
		t.Fatalf("calls = %d", called.Load())
	}
}

func TestHostFunctionPanicCleanup(t *testing.T) {
	for _, callback := range []bool{false, true} {
		t.Run(map[bool]string{false: "host", true: "callback"}[callback], func(t *testing.T) {
			var cleaned, applicationDeferred atomic.Bool
			operation := NewHostFunc(func(c HostContext, _ int) (int, error) {
				defer cleaned.Store(true)
				if callback {
					c.Reenter(func() { panic("callback diagnostics") })
				}
				panic(&hostIntegrationRecord{Text: "private host object"})
			})
			i := hostIntegrationInstance(t, true, func() {
				defer applicationDeferred.Store(true)
				_, _ = operation(1)
			})
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			var panicErr *PanicError
			if err := i.Wait(); !errors.As(err, &panicErr) || panicErr.Phase != "host operation" {
				t.Fatalf("Wait = %v", err)
			}
			if !cleaned.Load() || applicationDeferred.Load() {
				t.Fatal("panic cleanup crossed application boundary")
			}
			if callback && panicErr.Message != "callback diagnostics" {
				t.Fatalf("panic = %q", panicErr.Message)
			}
		})
	}
}

func TestHostContextValidation(t *testing.T) {
	var saved HostContext
	op := NewHostFunc(func(c HostContext, _ int) (int, error) { saved = c; return 1, nil })
	i := hostIntegrationInstance(t, false, func() { _, _ = op(1) })
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func(){func() { op(1) }, func() { saved.Allocator() }, func() { saved.Reenter(func() {}) }, func() { HostContext{}.Allocator() }, func() { Allocate(Allocator{}, func() int { return 1 }) }} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid context accepted")
				}
			}()
			fn()
		}()
	}
}

func TestHostWaiterCompletionAndSuspension(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{false: "parked", true: "early"}[early], func(t *testing.T) {
			completion := make(chan *Waiter, 1)
			var slot int
			op := NewHostFunc(func(c HostContext, _ int) (int, error) {
				w := NewWaiter()
				if early {
					slot = 42
					w.Wake()
					w.Wake()
				} else {
					completion <- w
				}
				w.Park()
				return slot, nil
			})
			var result atomic.Int32
			i := hostIntegrationInstance(t, true, func() {
				n, err := op(1)
				if err != nil {
					panic(err)
				}
				result.Store(int32(n))
			})
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			if !early {
				w := <-completion
				if err := i.Suspend(); err != nil {
					t.Fatal(err)
				}
				if i.PendingCalls() != 1 {
					t.Fatalf("pending=%d", i.PendingCalls())
				}
				slot = 42
				w.Wake()
				w.Wake()
				if result.Load() != 0 {
					t.Fatal("wake bypassed suspension")
				}
				if err := i.Resume(); err != nil {
					t.Fatal(err)
				}
			}
			if err := i.Wait(); err != nil {
				t.Fatal(err)
			}
			if result.Load() != 42 {
				t.Fatalf("result=%d", result.Load())
			}
		})
	}
}

func TestHostWaiterLateCompletion(t *testing.T) {
	var cleaned, resumed atomic.Bool
	completion := make(chan *Waiter, 1)
	op := NewHostFunc(func(c HostContext, _ int) (int, error) {
		defer cleaned.Store(true)
		w := NewWaiter()
		completion <- w
		w.Park()
		resumed.Store(true)
		return 1, nil
	})
	i := hostIntegrationInstance(t, true, func() { _, _ = op(1); resumed.Store(true) })
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	w := <-completion
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) || pending.PendingHostWork != 1 {
		t.Fatalf("Kill=%v", err)
	}
	if !cleaned.Load() || resumed.Load() {
		t.Fatal("revocation failed to unwind host cleanup")
	}
	w.Wake()
	w.Wake()
	<-i.Done()
	if err := i.Wait(); err != ErrRevoked {
		t.Fatalf("Wait=%v", err)
	}
	if resumed.Load() {
		t.Fatal("late wake revived isolate")
	}
}

func TestHostAsyncAllocationAndGC(t *testing.T) {
	op := NewHostFunc(func(c HostContext, _ int) (*hostIntegrationRecord, error) {
		w, a := NewWaiter(), c.Allocator()
		var value *hostIntegrationRecord
		var err error
		go func() {
			if IsActive() {
				panic("worker inherited application execution")
			}
			value, err = Allocate(a, func() *hostIntegrationRecord { return &hostIntegrationRecord{Bytes: make([]byte, 1024)} })
			w.Wake()
		}()
		w.Park()
		return value, err
	})
	stop := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() { close(stop); workers.Wait() }()
	for pass := 0; pass < 20; pass++ {
		i := hostIntegrationInstance(t, true, func() {
			value, err := op(1)
			if err != nil || hostIntegrationOwner(unsafe.Pointer(value)) == 0 {
				panic("bad asynchronous result")
			}
		})
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHostAllocateBudget(t *testing.T) {
	i, err := New(Config{Program: lifecycleProgram(func() {}), ResourceLimits: ResourceLimits{MaxMemoryBytes: 2 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	a := i.Allocator()
	before := i.Resources().TotalAllocatedBytes
	result, err := Allocate(a, func() []byte { return make([]byte, 128<<10) })
	if err != nil || len(result) != 128<<10 || i.Resources().TotalAllocatedBytes < before+(128<<10) {
		t.Fatalf("unaccounted allocation: %v %+v", err, i.Resources())
	}
	result = nil
	result, err = Allocate(a, func() []byte { return make([]byte, 4<<20) })
	if err != ErrRevoked || result != nil {
		t.Fatalf("budget rejection = %v, %d", err, len(result))
	}
	<-i.Done()
	var resource *ResourceLimitError
	if err := i.Wait(); !errors.As(err, &resource) {
		t.Fatalf("Wait=%v", err)
	}
}

func TestHostWaiterUseValidation(t *testing.T) {
	op := NewHostFunc(func(c HostContext, _ int) (int, error) {
		w := NewWaiter()
		copied := *w
		func() {
			defer func() {
				if recover() == nil {
					panic("copied waiter accepted")
				}
			}()
			copied.Wake()
		}()
		done := make(chan bool, 1)
		go func() { defer func() { done <- recover() != nil }(); w.Park() }()
		if !<-done {
			panic("wrong goroutine accepted")
		}
		w.Wake()
		w.Park()
		func() {
			defer func() {
				if recover() == nil {
					panic("duplicate park accepted")
				}
			}()
			w.Park()
		}()
		return 1, nil
	})
	i := hostIntegrationInstance(t, false, func() { _, _ = op(1) })
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestHostReadOnlyScratchAllocator(t *testing.T) {
	var a Allocator
	var scratchOwner, workflowOwner uintptr
	op := NewHostFunc(func(c HostContext, fn func()) (int, error) {
		a = c.Allocator()
		value, err := Allocate(a, func() []byte { return make([]byte, 256) })
		if err != nil {
			panic(err)
		}
		scratchOwner = hostIntegrationOwner(unsafe.Pointer(&value[0]))
		c.Reenter(fn)
		return 1, nil
	})
	i := hostIntegrationInstance(t, true, func() {
		workflow := make([]byte, 256)
		workflowOwner = hostIntegrationOwner(unsafe.Pointer(&workflow[0]))
		go func() {
			_, _ = ReadOnlyCall(2, nil)
			_, _ = op(func() {
				if !IsReadOnly() {
					panic("callback lost read-only role")
				}
				b := make([]byte, 256)
				if hostIntegrationOwner(unsafe.Pointer(&b[0])) != scratchOwner {
					panic("callback lost scratch owner")
				}
				runtime.KeepAlive(b)
			})
			Write(3, nil)
		}()
		_, _ = Call(1, nil)
		runtime.KeepAlive(workflow)
	})
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	first, second := resourceCommand(t, i), resourceCommand(t, i)
	if first.Op != 2 {
		first, second = second, first
	}
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := i.ResumeReadOnly(); err != nil {
		t.Fatal(err)
	}
	first.Reply(nil, nil)
	select {
	case <-i.Writes():
	case <-i.Done():
		t.Fatalf("query failed: %v", i.Wait())
	case <-time.After(5 * time.Second):
		t.Fatal("query did not finish")
	}
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	if scratchOwner == 0 || scratchOwner == workflowOwner {
		t.Fatalf("scratch=%d workflow=%d", scratchOwner, workflowOwner)
	}
	called := false
	if _, err := Allocate(a, func() int { called = true; return 1 }); err != ErrRevoked || called {
		t.Fatal("expired scratch allocator ran constructor")
	}
	second.Reply(nil, nil)
	if err := i.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestHostWaiterCompletionKillRace(t *testing.T) {
	for pass := 0; pass < 50; pass++ {
		ready := make(chan *Waiter, 1)
		var resumed atomic.Int32
		op := NewHostFunc(func(c HostContext, _ int) (int, error) {
			w := NewWaiter()
			ready <- w
			w.Park()
			return 1, nil
		})
		i := hostIntegrationInstance(t, true, func() { _, _ = op(1); resumed.Add(1) })
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		w := <-ready
		if err := i.Suspend(); err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		go func() { w.Wake(); w.Wake(); close(finished) }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := i.Kill(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		atDone := resumed.Load()
		<-finished
		if resumed.Load() != atDone {
			t.Fatal("completion revived a drained instance")
		}
	}
}

func TestHostConstructorChildDoesNotInheritScope(t *testing.T) {
	i := hostIntegrationInstance(t, false, func() {})
	result := make(chan uintptr, 1)
	_, err := Allocate(i.Allocator(), func() int {
		go func() {
			b := make([]byte, 1024)
			result <- hostIntegrationOwner(unsafe.Pointer(&b[0]))
		}()
		return 1
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner := <-result; owner != 0 {
		t.Fatalf("constructor child inherited owner %d", owner)
	}
}

func TestHostRegistrationRejectsPrivateClosure(t *testing.T) {
	i := hostIntegrationInstance(t, false, func() {})
	fn, err := Allocate(i.Allocator(), func() func(HostContext, int) (int, error) {
		captured := new(hostIntegrationRecord)
		return func(HostContext, int) (int, error) { captured.Text = "private"; return 1, nil }
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Error("registered a private closure")
		}
	}()
	NewHostFunc(fn)
}

func TestHostReentryPreservesOriginalRole(t *testing.T) {
	op := NewHostFunc(func(c HostContext, fn func()) (int, error) {
		RunInbound(func() { c.Reenter(fn) })
		return 1, nil
	})
	i := hostIntegrationInstance(t, true, func() {
		_, _ = op(func() {
			rejected := false
			func() { defer func() { rejected = recover() != nil }(); _, _ = ControlCall(77, nil) }()
			if !rejected {
				panic("reentry inherited host inbound scope")
			}
		})
	})
	go func() {
		for {
			select {
			case c := <-i.Commands():
				c.Reply(nil, nil)
			case <-i.Done():
				return
			}
		}
	}()
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestHostWaiterPreservesAdmissionRole(t *testing.T) {
	ready := make(chan *Waiter, 1)
	var resumed atomic.Bool
	op := NewHostFunc(func(c HostContext, _ int) (int, error) {
		RunInbound(func() { w := NewWaiter(); ready <- w; w.Park() })
		return 1, nil
	})
	i := hostIntegrationInstance(t, true, func() { _, _ = op(1); resumed.Store(true) })
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	w := <-ready
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	w.Wake()
	if err := i.ResumeInbound(); err != nil {
		t.Fatal(err)
	}
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	if resumed.Load() {
		t.Fatal("host helper scope bypassed application admission")
	}
	if err := i.Resume(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestHostRevocationCannotRecoverApplicationCallback(t *testing.T) {
	entered := make(chan struct{})
	var cleaned, recovered, applicationDeferred atomic.Bool
	op := NewHostFunc(func(c HostContext, fn func()) (int, error) { defer cleaned.Store(true); c.Reenter(fn); return 1, nil })
	i := hostIntegrationInstance(t, true, func() {
		defer applicationDeferred.Store(true)
		_, _ = op(func() {
			defer func() {
				if recover() != nil {
					recovered.Store(true)
				}
			}()
			close(entered)
			<-make(chan struct{})
		})
	})
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if !cleaned.Load() || recovered.Load() || applicationDeferred.Load() {
		t.Fatal("revocation crossed the cleanup boundary")
	}
}

func TestHostFunctionConcurrentCallers(t *testing.T) {
	const callers = 8
	ready, release := make(chan struct{}, callers), make(chan struct{})
	var mu sync.Mutex
	cache := make(map[string]int)
	operation := NewHostFunc(func(c HostContext, name string) (*hostIntegrationRecord, error) {
		mu.Lock()
		cache[strings.Clone(name)]++ // Retain a host copy of the borrowed key.
		mu.Unlock()
		ready <- struct{}{}
		<-release
		return Allocate(c.Allocator(), func() *hostIntegrationRecord {
			return &hostIntegrationRecord{Text: strings.Clone(name), Bytes: make([]byte, 128)}
		})
	})
	done := make(chan error, callers)
	for range callers {
		go func() {
			i := hostIntegrationInstance(t, true, func() {
				owned := make([]byte, 128)
				want := hostIntegrationOwner(unsafe.Pointer(&owned[0]))
				record, err := operation(strings.Repeat("customer", 32))
				if err != nil || hostIntegrationOwner(unsafe.Pointer(record)) != want || hostIntegrationOwner(unsafe.Pointer(&record.Bytes[0])) != want {
					panic("concurrent host calls mixed allocation domains")
				}
				runtime.KeepAlive(owned)
			})
			if err := i.Start(); err != nil {
				done <- err
				return
			}
			done <- i.Wait()
		}()
	}
	for range callers {
		<-ready
	}
	close(release)
	for range callers {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if cache[strings.Repeat("customer", 32)] != callers {
		t.Fatal("shared cache lost a synchronized update")
	}
}

func TestHostCallbackResourceCleanup(t *testing.T) {
	for _, stack := range []bool{false, true} {
		t.Run(map[bool]string{false: "heap", true: "stack"}[stack], func(t *testing.T) {
			var cleaned, continued atomic.Bool
			operation := NewHostFunc(func(c HostContext, fn func()) (int, error) {
				defer cleaned.Store(true)
				c.Reenter(fn)
				continued.Store(true)
				return 1, nil
			})
			i, err := New(Config{Deterministic: true, ResourceLimits: ResourceLimits{MaxMemoryBytes: 64 << 10}, Program: lifecycleProgram(func() {
				_, _ = operation(func() {
					defer func() {
						if recover() != nil {
							continued.Store(true)
						}
					}()
					if stack {
						resourceRecurse(10000)
					} else {
						runtime.KeepAlive(make([]byte, 8<<20))
					}
					continued.Store(true)
				})
				continued.Store(true)
			})})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-i.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("callback resource fault did not unwind")
			}
			var limit *ResourceLimitError
			if err := i.Wait(); !errors.As(err, &limit) || limit.Resource != "memory" {
				t.Fatalf("Wait=%v", err)
			}
			if !cleaned.Load() || continued.Load() {
				t.Fatal("resource fault abandoned host cleanup or resumed application code")
			}
			for range 3 {
				runtime.GC()
			}
			if usage := i.Resources(); usage.HeapBytes != 0 {
				t.Fatalf("callback cleanup retained private heap or allocation credit: %+v", usage)
			}
		})
	}
}

func TestHostReentryDoesNotInheritMetadataScope(t *testing.T) {
	operation := NewHostFunc(func(c HostContext, fn func()) (int, error) {
		previous := ownershipEnterMetadata()
		defer ownershipLeaveMetadata(previous)
		c.Reenter(fn)
		return 1, nil
	})
	i := hostIntegrationInstance(t, true, func() {
		_, _ = operation(func() {
			if !IsActive() {
				panic("reentry did not restore application execution")
			}
			if _, err := Call(97, nil); err != nil {
				panic(err)
			}
		})
	})
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case command := <-i.Commands():
		if command.Op != 97 {
			t.Fatal("wrong reentry operation")
		}
		command.Reply(nil, nil)
	case <-i.Done():
		t.Fatal(i.Wait())
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestHostConstructionDoesNotShareProcessPools(t *testing.T) {
	var pool sync.Pool
	pool.New = func() any { return new([256]byte) }
	host := pool.Get().(*[256]byte)
	pool.Put(host)
	for range 2 {
		instance := hostIntegrationInstance(t, false, func() {})
		value, err := Allocate(instance.Allocator(), func() *[256]byte {
			value := pool.Get().(*[256]byte)
			pool.Put(value)
			return value
		})
		if err != nil {
			t.Fatal(err)
		}
		if hostIntegrationOwner(unsafe.Pointer(value)) == 0 {
			t.Fatal("constructor borrowed a host pool entry")
		}
	}
	pool.New = nil
	for range 4 {
		if value := pool.Get(); value != nil && hostIntegrationOwner(unsafe.Pointer(value.(*[256]byte))) != 0 {
			t.Fatal("process pool retained a private constructor allocation")
		}
	}
}
