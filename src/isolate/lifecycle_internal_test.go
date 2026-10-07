// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"iter"
	"math/rand"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
	"weak"
)

func lifecycleProgram(entry func()) Program {
	return Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main:     entry,
	}}
}

func lifecycleWait(t *testing.T, instance *Isolate) error {
	t.Helper()
	select {
	case <-instance.Done():
		if live := instance.boundary.LiveGoroutines(); live != 0 {
			t.Fatalf("Done closed with %d attached goroutines", live)
		}
		return instance.Wait()
	case <-time.After(5 * time.Second):
		t.Fatal("isolate did not finish cleanup")
		return nil
	}
}

func TestLifecyclePanicContainment(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		for _, phase := range []string{"main", "goroutine", "initialization", "initializer goroutine"} {
			t.Run(phase+"/"+map[bool]string{false: "concurrent", true: "deterministic"}[deterministic], func(t *testing.T) {
				fail := func() { panic("private panic payload") }
				program := lifecycleProgram(fail)
				if phase == "goroutine" {
					program.entry.Main = func() { go fail(); select {} }
				} else if phase == "initialization" || phase == "initializer goroutine" {
					program.entry.NewState = func() (func(func()), error) {
						if phase == "initialization" {
							fail()
						}
						go fail()
						select {}
					}
				}
				instance, err := New(Config{Program: program, Deterministic: deterministic})
				if phase == "main" || phase == "goroutine" {
					if err != nil {
						t.Fatal(err)
					}
					if err := instance.Start(); err != nil {
						t.Fatal(err)
					}
					err = lifecycleWait(t, instance)
				} else if instance != nil {
					t.Fatal("failed initializer returned an instance")
				}
				var failure *PanicError
				if !errors.As(err, &failure) || failure.Phase != phase || failure.Message != "private panic payload" || !strings.Contains(failure.Stack, "TestLifecyclePanicContainment") {
					t.Fatalf("panic diagnostic = %v", err)
				}
				if owner, heap := ownershipAllocationOrigin(unsafe.Pointer(failure)); !heap || owner != 0 {
					t.Fatalf("panic error allocated under owner %d (heap=%t)", owner, heap)
				}
				if owner, heap := ownershipAllocationOrigin(unsafe.Pointer(unsafe.StringData(failure.Message))); !heap || owner != 0 {
					t.Fatalf("panic message allocated under owner %d (heap=%t)", owner, heap)
				}
			})
		}
	}
}

type lifecyclePanicValue struct{ called *atomic.Bool }

func (v *lifecyclePanicValue) Error() string { v.called.Store(true); panic("diagnostic callback ran") }

func TestLifecyclePanicDoesNotInvokeUserFormatting(t *testing.T) {
	var called atomic.Bool
	instance, err := New(Config{Program: lifecycleProgram(func() { panic(&lifecyclePanicValue{called: &called}) })})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	var failure *PanicError
	if err := lifecycleWait(t, instance); !errors.As(err, &failure) || !strings.Contains(failure.Message, "lifecyclePanicValue") || called.Load() {
		t.Fatalf("panic formatting = %v, application method called=%t", err, called.Load())
	}
}

func TestLifecycleRecoveredPanicAndChildGoexit(t *testing.T) {
	var recovered, exited atomic.Bool
	instance, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer exited.Store(true)
			runtime.Goexit()
		}()
		<-finished
		func() {
			defer func() { recovered.Store(recover() == "recoverable") }()
			panic("recoverable")
		}()
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, instance); err != nil || !recovered.Load() || !exited.Load() {
		t.Fatalf("ordinary panic/Goexit behavior: err=%v recovered=%t exited=%t", err, recovered.Load(), exited.Load())
	}
}

func TestLifecycleMainCompletionDrainsChildren(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var deferred, resumed atomic.Bool
		instance, err := New(Config{Deterministic: deterministic, Program: lifecycleProgram(func() {
			entered := make(chan struct{})
			go func() {
				defer deferred.Store(true)
				close(entered)
				_, _ = Call(71, nil)
				resumed.Store(true)
			}()
			<-entered
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.Start(); err != nil {
			t.Fatal(err)
		}
		if err := lifecycleWait(t, instance); err != nil || deferred.Load() || resumed.Load() {
			t.Fatalf("main completion err=%v deferred=%t resumed=%t", err, deferred.Load(), resumed.Load())
		}
		if instance.entry != nil || instance.runState != nil {
			t.Fatal("completed instance retained entry or private package runner")
		}
	}
}

func TestLifecycleNewContextCancelsInitializer(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var returned, deferred atomic.Bool
		entered := make(chan struct{})
		program := lifecycleProgram(func() { t.Error("canceled startup ran main") })
		program.entry.NewState = func() (func(func()), error) {
			defer deferred.Store(true)
			close(entered)
			select {}
			returned.Store(true)
			return nil, nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-entered; cancel() }()
		instance, err := NewContext(ctx, Config{Program: program, Deterministic: deterministic})
		if instance != nil || !errors.Is(err, context.Canceled) || returned.Load() || deferred.Load() {
			t.Fatalf("NewContext = %v, %v, returned=%t deferred=%t", instance, err, returned.Load(), deferred.Load())
		}
	}
}

func TestLifecycleStartupPendingCleanupNotification(t *testing.T) {
	var release atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	program := lifecycleProgram(func() {})
	program.entry.NewState = func() (func(func()), error) {
		entered := make(chan struct{})
		go func() {
			close(entered)
			for !release.Load() {
			}
			runtime.Gosched() // Return through a supported revocation fence.
		}()
		<-entered
		return nil, errors.New("initializer error")
	}
	instance, err := NewContext(ctx, Config{Program: program})
	var failure *InitializationError
	if instance != nil || !errors.As(err, &failure) || !errors.Is(err, errInitializerFailed) || failure.Pending.LiveGoroutines == 0 || failure.Pending.GoroutineID == 0 {
		release.Store(true)
		t.Fatalf("pending startup = %v, %v", instance, err)
	}
	select {
	case <-failure.Done:
		t.Fatal("startup cleanup completed while its child was still active")
	default:
	}
	release.Store(true)
	select {
	case <-failure.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("pending initializer did not finish cleanup")
	}
}

func TestLifecycleRetainedInstanceReleasesPrivateRunner(t *testing.T) {
	var state weak.Pointer[[]byte]
	program := lifecycleProgram(func() {})
	program.entry.NewState = func() (func(func()), error) {
		data := make([]byte, 1<<20)
		state = weak.Make(&data)
		return func(fn func()) { fn(); runtime.KeepAlive(data) }, nil
	}
	instance, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, instance); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		runtime.GC()
		if state.Value() == nil {
			runtime.KeepAlive(instance)
			return
		}
		runtime.Gosched()
	}
	t.Fatal("retained completed instance kept its private package runner alive")
}

func TestLifecycleConcurrentKillAndLateReply(t *testing.T) {
	for range 100 {
		var after atomic.Bool
		instance, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
			_, _ = Call(19, []byte("request"))
			after.Store(true)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := instance.Start(); err != nil {
			t.Fatal(err)
		}
		command := <-instance.Commands()
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		results := make(chan error, 8)
		for range 8 {
			go func() { results <- instance.Kill(ctx) }()
		}
		for range 8 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		command.Reply([]byte("late reply"), nil)
		if err := lifecycleWait(t, instance); err != ErrRevoked || after.Load() {
			t.Fatalf("after kill: err=%v executed=%t", err, after.Load())
		}
	}
}

func TestLifecycleUnstartedIteratorReleasesState(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent", true: "deterministic"}[deterministic], func(t *testing.T) {
			lifecycleIteratorReleasesState(t, deterministic)
		})
	}
}

func lifecycleIteratorReleasesState(t *testing.T, deterministic bool) {
	var state weak.Pointer[[]byte]
	var release atomic.Bool
	entered := make(chan struct{})
	instance, err := New(Config{Deterministic: deterministic, Program: lifecycleProgram(func() {
		data := make([]byte, 1<<20)
		state = weak.Make(&data)
		next, stop := iter.Pull(func(yield func(int) bool) {
			yield(len(data))
			runtime.KeepAlive(data)
		})
		// In deterministic mode, keep the execution token so corostart has
		// not run. Concurrent mode also covers a parked iterator handshake.
		close(entered)
		for !release.Load() {
		}
		runtime.Gosched()
		runtime.KeepAlive(next)
		runtime.KeepAlive(stop)
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	var pending *KillPendingError
	err = instance.Kill(ctx)
	cancel()
	if !errors.As(err, &pending) {
		release.Store(true)
		t.Fatalf("active iterator owner = %v, want pending", err)
	}
	release.Store(true)
	if err := lifecycleWait(t, instance); err != ErrRevoked {
		t.Fatalf("revoked iterator = %v", err)
	}
	for range 5 {
		runtime.GC()
		if state.Value() == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("unstarted discarded iterator retained private state")
}

func TestLifecycleRetainedHandlesReleaseAllocatorCaches(t *testing.T) {
	for range 3 {
		runtime.GC()
		runtime.Gosched()
	}
	baseline := isolateAllocCacheCount()
	held := make([]*Isolate, 64)
	for index := range held {
		instance, err := New(Config{Program: lifecycleProgram(func() {
			data := make([]byte, 64<<10)
			data[0] = 1
			runtime.KeepAlive(data)
		})})
		if err != nil {
			t.Fatal(err)
		}
		held[index] = instance
		if err := instance.Start(); err != nil {
			t.Fatal(err)
		}
		if err := lifecycleWait(t, instance); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		caches := isolateAllocCacheCount()
		if caches <= baseline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retained completed handles kept caches alive: %d, baseline %d", caches, baseline)
		}
		runtime.Gosched()
	}
	runtime.KeepAlive(held)
}

// Retaining copied diagnostics must not retain the arbitrary private panic value.
func TestLifecyclePanicDiagnosticReleasesPrivateValue(t *testing.T) {
	var reference weak.Pointer[[]byte]
	instance, err := New(Config{Program: lifecycleProgram(func() {
		data := make([]byte, 1<<20)
		reference = weak.Make(&data)
		panic(&data)
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	var failure *PanicError
	if err := lifecycleWait(t, instance); !errors.As(err, &failure) {
		t.Fatalf("panic = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for reference.Value() != nil {
		runtime.GC()
		if time.Now().After(deadline) {
			t.Fatal("retained panic diagnostics retained private value")
		}
		runtime.Gosched()
	}
	runtime.KeepAlive(failure)
	runtime.KeepAlive(instance)
}

func TestLifecycleKillWhileStartWaitsForInitializerChild(t *testing.T) {
	var release, ran atomic.Bool
	entered := make(chan struct{})
	program := lifecycleProgram(func() { ran.Store(true) })
	program.entry.NewState = func() (func(func()), error) {
		go func() {
			close(entered)
			for !release.Load() {
			}
			runtime.Gosched()
		}()
		return func(fn func()) { fn() }, nil
	}
	i, err := New(Config{Program: program, Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	<-entered // The initializer's child holds the deterministic execution token.
	started := make(chan error, 1)
	go func() { started <- i.Start() }()
	select {
	case err := <-started:
		if err != nil {
			release.Store(true)
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		release.Store(true)
		t.Fatal("Start waited for a busy initializer child while holding its lifecycle lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	var pending *KillPendingError
	err = i.Kill(ctx)
	cancel()
	if !errors.As(err, &pending) {
		release.Store(true)
		t.Fatalf("busy initializer child kill = %v", err)
	}
	release.Store(true)
	if err := lifecycleWait(t, i); err != ErrRevoked || ran.Load() {
		t.Fatalf("queued root after kill: %v, ran=%t", err, ran.Load())
	}
}

//go:linkname lifecycleRandLegacy runtime.isolateRandLegacy
func lifecycleRandLegacy() (unsafe.Pointer, bool)

func TestLifecycleRetainedHandleReleasesRandomState(t *testing.T) {
	var reference weak.Pointer[byte]
	instance, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
		_ = rand.Uint64()
		pointer, ok := lifecycleRandLegacy()
		if !ok || pointer == nil {
			panic("missing private generator")
		}
		reference = weak.Make((*byte)(pointer))
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, instance); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for reference.Value() != nil {
		runtime.GC()
		if time.Now().After(deadline) {
			t.Fatal("retained host handle kept private random state")
		}
		runtime.Gosched()
	}
	runtime.KeepAlive(instance)
}

func TestLifecycleStartDoesNotPublishFalseIdle(t *testing.T) {
	for range 500 {
		i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
			for range 10 {
				runtime.Gosched()
			}
			_, _ = Call(23, nil)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		if err := i.Suspend(); err != nil {
			t.Fatal(err)
		}
		if i.PendingCalls() != 1 {
			_ = i.Kill(context.Background())
			t.Fatal("Start published idle before its root was registered with dispatch")
		}
		if err := i.Kill(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

// Host receipt wakes a Call's transport continuation. Its delayed second poll
// must not perturb application select randomness in another goroutine.
func TestLifecycleCallPollsIgnoreHostReceiptTiming(t *testing.T) {
	var expected [100]int
	for iteration := range 20 {
		fast := iteration%2 == 0
		var release atomic.Bool
		observed := make(chan [100]int, 1)
		i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
			entered := make(chan struct{})
			go func() { close(entered); _, _ = Call(41, nil) }()
			<-entered
			for !release.Load() {
			}
			runtime.Gosched() // Run a delivered transport continuation, if present.
			closed := make(chan struct{})
			close(closed)
			var trace [100]int
			for n := range trace {
				select {
				case <-closed:
					trace[n] = 0
				case <-closed:
					trace[n] = 1
				case <-closed:
					trace[n] = 2
				}
			}
			observed <- trace
			select {}
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var command *Command
		if fast {
			command = <-i.Commands()
		}
		release.Store(true)
		trace := <-observed
		if !fast {
			command = <-i.Commands()
		}
		if err := i.Kill(context.Background()); err != nil {
			t.Fatal(err)
		}
		command.Reply(nil, nil) // Retained delivery cannot revive the caller.
		if iteration == 0 {
			expected = trace
		} else if trace != expected {
			t.Fatalf("host receipt changed select trace (fast=%t)", fast)
		}
	}
}
