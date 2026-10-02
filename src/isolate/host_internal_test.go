// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"os"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMainExitRevokesUnstartedChildren(t *testing.T) {
	var release atomic.Bool
	ready := make(chan struct{})
	childExited := make(chan struct{})
	var grandchildCreated atomic.Bool
	var grandchildRan atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go func() {
				defer close(childExited)
				close(ready)
				for !release.Load() {
				}
				grandchildCreated.Store(true)
				go func() { grandchildRan.Store(true) }()
			}()
			<-ready
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	release.Store(true)
	<-childExited
	deadline := time.Now().Add(time.Second)
	for i.boundary.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := i.boundary.LiveGoroutines(); got != 0 {
		t.Fatalf("after main exit, live goroutines = %d, want 0", got)
	}
	if !grandchildCreated.Load() {
		t.Fatal("already running child did not create a grandchild")
	}
	if grandchildRan.Load() {
		t.Fatal("grandchild started after main exit")
	}
}

func TestKillStopsCallWaiter(t *testing.T) {
	var deferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			defer deferred.Store(true)
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
	command := <-i.Commands()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after Kill = %v, want %v", err, errMainRevoked)
	}
	command.Reply(nil, nil)
	if deferred.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("Kill returned with deferred=%t, live=%d", deferred.Load(), i.boundary.LiveGoroutines())
	}
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("repeated Kill = %v", err)
	}
}

func TestProcessExitStopsOnlyIsolate(t *testing.T) {
	for _, tt := range []struct {
		name string
		code int
		exit func()
	}{
		{"os zero", 0, func() { os.Exit(0) }},
		{"os nonzero", 37, func() { os.Exit(37) }},
		{"syscall", 38, func() { syscall.Exit(38) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var deferred, resumed atomic.Bool
			program := Program{entry: isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main: func() {
					defer deferred.Store(true)
					tt.exit()
					resumed.Store(true)
				},
			}}
			i, err := New(Config{Program: program})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			err = i.Wait()
			if tt.code == 0 {
				if err != nil {
					t.Fatalf("Wait after Exit(0) = %v", err)
				}
			} else {
				var exitErr *ExitError
				if !errors.As(err, &exitErr) || exitErr.Code != tt.code {
					t.Fatalf("Wait after Exit(%d) = %v, want exit status", tt.code, err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := i.Kill(ctx); err != nil {
				t.Fatal(err)
			}
			if deferred.Load() || resumed.Load() {
				t.Fatalf("Exit ran user code: deferred=%t resumed=%t", deferred.Load(), resumed.Load())
			}
		})
	}
}

func TestProcessExitDuringInitialization(t *testing.T) {
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			os.Exit(39)
			return nil, nil
		},
		Main: func() { t.Error("main ran after initializer Exit") },
	}}
	i, err := New(Config{Program: program})
	if i != nil {
		t.Fatal("New returned an instance after initializer Exit")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 39 {
		t.Fatalf("New after initializer Exit = %v, want status 39", err)
	}
}

func TestChildExitDuringInitialization(t *testing.T) {
	var deferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			defer deferred.Store(true)
			go func() { os.Exit(41) }()
			var never chan struct{}
			<-never
			return nil, nil
		},
		Main: func() { t.Error("main ran after initializer child Exit") },
	}}
	i, err := New(Config{Program: program})
	if i != nil {
		t.Fatal("New returned an instance after initializer child Exit")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 41 {
		t.Fatalf("New after initializer child Exit = %v, want status 41", err)
	}
	if deferred.Load() {
		t.Fatal("initializer ran a defer after child Exit")
	}
}

func TestChildExitRevokesMain(t *testing.T) {
	var mainResumed, mainDeferred, childDeferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			defer mainDeferred.Store(true)
			go func() {
				defer childDeferred.Store(true)
				os.Exit(40)
			}()
			_, _ = isolatebridge.Current().Call(1, nil)
			mainResumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	var exitErr *ExitError
	if err := i.Wait(); !errors.As(err, &exitErr) || exitErr.Code != 40 {
		t.Fatalf("Wait after child Exit = %v, want status 40", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if mainResumed.Load() || mainDeferred.Load() || childDeferred.Load() {
		t.Fatalf("child Exit ran user code: main resumed=%t, main deferred=%t, child deferred=%t", mainResumed.Load(), mainDeferred.Load(), childDeferred.Load())
	}
}

func TestBeginStopDefersWaiterScan(t *testing.T) {
	entered := make(chan struct{})
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			time.Sleep(time.Hour)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(i.boundary.WakeStoppedWaiters)
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := i.boundary.RunningGoroutines(); got != 0 {
		t.Fatalf("main did not park: running=%d", got)
	}
	i.boundary.BeginStop()
	if got := i.boundary.LiveGoroutines(); got != 1 {
		t.Fatalf("BeginStop scanned waiters: live=%d, want 1", got)
	}
	i.boundary.WakeStoppedWaiters()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill after waiter scan = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after waiter scan = %v, want %v", err, errMainRevoked)
	}
}

func TestKillWakesCondWait(t *testing.T) {
	entered := make(chan struct{})
	var resumed, deferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var mu sync.Mutex
			cond := sync.NewCond(&mu)
			mu.Lock()
			defer func() { deferred.Store(true); mu.Unlock() }()
			close(entered)
			cond.Wait()
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 {
		t.Fatal("Cond waiter did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on Cond wait = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after Cond revocation = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() || deferred.Load() {
		t.Fatalf("revoked Cond returned=%t, ran defer=%t", resumed.Load(), deferred.Load())
	}
}

func TestKillWakesMultipleCondWaiters(t *testing.T) {
	entered := make(chan struct{}, 2)
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var mu sync.Mutex
			cond := sync.NewCond(&mu)
			wait := func() {
				mu.Lock()
				defer mu.Unlock()
				entered <- struct{}{}
				cond.Wait()
				resumed.Store(true)
			}
			go wait()
			wait()
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 2 {
		t.Fatal("both Cond waiters did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill with two Cond waiters = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after Cond revocation = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("Cond waiter returned after revocation")
	}
}

func TestKillRacesCondSignal(t *testing.T) {
	entered := make(chan struct{})
	var signal atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var mu sync.Mutex
			cond := sync.NewCond(&mu)
			go func() {
				for !signal.Load() {
					runtime.Gosched()
				}
				cond.Signal()
			}()
			mu.Lock()
			defer mu.Unlock()
			close(entered)
			cond.Wait()
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	signal.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill racing Cond.Signal = %v", err)
	}
	if err := i.Wait(); err != nil && err != errMainRevoked {
		t.Fatalf("Wait after Cond.Signal race = %v", err)
	}
}

func TestKillWakesSemaphoreWaiters(t *testing.T) {
	cases := []struct {
		name string
		main func(chan<- struct{}, *atomic.Bool)
	}{
		{"Mutex", func(entered chan<- struct{}, resumed *atomic.Bool) {
			var mu sync.Mutex
			mu.Lock()
			for range 2 {
				go func() {
					entered <- struct{}{}
					mu.Lock()
					resumed.Store(true)
					mu.Unlock()
				}()
			}
			select {}
		}},
		{"RWMutex", func(entered chan<- struct{}, resumed *atomic.Bool) {
			var rw sync.RWMutex
			rw.Lock()
			go func() {
				entered <- struct{}{}
				rw.RLock()
				resumed.Store(true)
				rw.RUnlock()
			}()
			go func() {
				entered <- struct{}{}
				rw.Lock()
				resumed.Store(true)
				rw.Unlock()
			}()
			select {}
		}},
		{"WaitGroup", func(entered chan<- struct{}, resumed *atomic.Bool) {
			var wg sync.WaitGroup
			wg.Add(1)
			for range 2 {
				go func() {
					entered <- struct{}{}
					wg.Wait()
					resumed.Store(true)
				}()
			}
			select {}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{}, 2)
			var resumed atomic.Bool
			program := Program{entry: isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main:     func() { tc.main(entered, &resumed) },
			}}
			i, err := New(Config{Program: program})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			<-entered
			<-entered
			deadline := time.Now().Add(time.Second)
			for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if running, live := i.boundary.RunningGoroutines(), i.boundary.LiveGoroutines(); running != 0 || live != 3 {
				t.Fatalf("semaphore waiters not parked: running=%d live=%d", running, live)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := i.Kill(ctx); err != nil {
				t.Fatalf("Kill with semaphore waiters = %v", err)
			}
			if err := i.Wait(); err != errMainRevoked {
				t.Fatalf("Wait after semaphore revocation = %v, want %v", err, errMainRevoked)
			}
			if resumed.Load() {
				t.Fatal("semaphore waiter returned after revocation")
			}
		})
	}
}

func TestKillRacesMutexUnlock(t *testing.T) {
	entered := make(chan struct{})
	var release atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var mu sync.Mutex
			mu.Lock()
			go func() {
				close(entered)
				mu.Lock()
				mu.Unlock()
			}()
			go func() {
				for !release.Load() {
				}
				mu.Unlock()
			}()
			select {}
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if running, live := i.boundary.RunningGoroutines(), i.boundary.LiveGoroutines(); running != 1 || live != 3 {
		t.Fatalf("mutex waiter not parked: running=%d live=%d", running, live)
	}
	release.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill racing Mutex.Unlock = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after Mutex.Unlock race = %v, want %v", err, errMainRevoked)
	}
}

func TestKillWakesChannelWait(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			<-release
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("main did not park on channel")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on channel wait = %v", err)
	}
	select {
	case release <- struct{}{}:
		t.Fatal("revoked receive remained queued")
	default:
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked channel receive = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked channel receive")
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatalf("Kill after exit = %v", err)
	}
}

func TestKillWakesMultipleChannelWaiters(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	var resumed atomic.Bool
	wait := func() {
		entered <- struct{}{}
		<-release
		resumed.Store(true)
	}
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go wait()
			wait()
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 2 {
		t.Fatal("both channel waiters did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill with two channel waiters = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after channel revocation = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("channel returned=%t, live=%d", resumed.Load(), i.boundary.LiveGoroutines())
	}
}

func TestKillWakesTimerChannelReceive(t *testing.T) {
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			<-time.After(time.Hour)
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("timer channel receive did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill during timer receive = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after timer channel revocation = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("timer channel receive returned to user code")
	}
}

func TestKillWakesSelectWait(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	other := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			select {
			case <-release:
			case <-other:
			}
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("main did not park in select")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on select wait = %v", err)
	}
	for _, ch := range []chan struct{}{release, other} {
		select {
		case ch <- struct{}{}:
			t.Fatal("revoked select receive remained queued")
		default:
		}
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked select = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked select")
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatalf("Kill after exit = %v", err)
	}
}

func TestKillWakesTimerSelect(t *testing.T) {
	entered := make(chan struct{})
	other := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			select {
			case <-time.After(time.Hour):
			case <-other:
			}
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("timer select did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill during timer select = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after timer select revocation = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("timer select returned to user code")
	}
}

func TestKillWakesWaitGroupWait(t *testing.T) {
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var wg sync.WaitGroup
			wg.Add(1)
			close(entered)
			wg.Wait()
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("main did not park in WaitGroup.Wait")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on WaitGroup.Wait = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked WaitGroup.Wait = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked WaitGroup.Wait")
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatalf("Kill after WaitGroup.Wait exit = %v", err)
	}
}

func TestRevokedWaitGroupReadyPath(t *testing.T) {
	var release atomic.Bool
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var wg sync.WaitGroup
			close(entered)
			for !release.Load() {
			}
			wg.Wait() // The zero-count path must also observe revocation.
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) || pending.LiveGoroutines == 0 {
		t.Fatalf("Kill before ready WaitGroup.Wait = %v, want pending", err)
	}
	release.Store(true)
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked ready WaitGroup.Wait = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked ready WaitGroup.Wait")
	}
}

func TestKillStopsGoschedLoop(t *testing.T) {
	started := make(chan struct{})
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(started)
			for {
				runtime.Gosched()
			}
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on Gosched loop = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked Gosched loop = %v, want %v", err, errMainRevoked)
	}
}

func TestKillWakesSleep(t *testing.T) {
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			time.Sleep(time.Hour)
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("main did not park in Sleep")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill during Sleep = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked Sleep = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked Sleep")
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatalf("Kill after Sleep stopped = %v", err)
	}
}

func TestKillWakesMultipleSleepers(t *testing.T) {
	entered := make(chan struct{}, 2)
	var resumed atomic.Bool
	sleep := func() {
		entered <- struct{}{}
		time.Sleep(time.Hour)
		resumed.Store(true)
	}
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go sleep()
			sleep()
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.LiveGoroutines() != 2 || i.boundary.RunningGoroutines() != 0 {
		t.Fatal("both sleepers did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill with two sleepers = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked sleep = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("sleep returned=%t, live=%d", resumed.Load(), i.boundary.LiveGoroutines())
	}
}

func TestKillWakesPermanentParks(t *testing.T) {
	var nilChannel chan int
	for _, tc := range []struct {
		name string
		wait func()
	}{
		{"nil receive", func() { <-nilChannel }},
		{"nil send", func() { nilChannel <- 1 }},
		{"empty select", func() { select {} }},
		{"empty reflected select", func() { reflect.Select(nil) }},
		{"all-nil select", func() {
			select {
			case <-nilChannel:
			case nilChannel <- 1:
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan struct{})
			var resumed atomic.Bool
			program := Program{entry: isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main: func() {
					close(entered)
					tc.wait()
					resumed.Store(true)
				},
			}}
			i, err := New(Config{Program: program})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			<-entered
			deadline := time.Now().Add(time.Second)
			for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
				t.Fatal("main did not park")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := i.Kill(ctx); err != nil {
				t.Fatalf("Kill during permanent park = %v", err)
			}
			if err := i.Wait(); err != errMainRevoked {
				t.Fatalf("Wait after permanent park = %v, want %v", err, errMainRevoked)
			}
			if resumed.Load() {
				t.Fatal("permanent park returned to user code")
			}
		})
	}
}

func TestKillPreventsLateMainEntry(t *testing.T) {
	runnerEntered := make(chan struct{})
	var releaseRunner atomic.Bool
	var mainRan atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			return func(fn func()) {
				close(runnerEntered)
				for !releaseRunner.Load() {
				}
				fn()
			}, nil
		},
		Main: func() { mainRan.Store(true) },
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-runnerEntered
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) {
		t.Fatalf("Kill before main entry = %v, want pending", err)
	}
	releaseRunner.Store(true)
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	if mainRan.Load() {
		t.Fatal("main entered after revocation")
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestKillBeforeStart(t *testing.T) {
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main:     func() { t.Error("revoked program ran") },
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Kill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err == nil {
		t.Fatal("Start succeeded after Kill")
	}
}

func TestConcurrentStartKill(t *testing.T) {
	for range 100 {
		var killReturned atomic.Bool
		var ranAfterKill atomic.Bool
		program := Program{entry: isolatebridge.ProgramEntry{
			NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
			Main: func() {
				if killReturned.Load() {
					ranAfterKill.Store(true)
				}
			},
		}}
		i, err := New(Config{Program: program})
		if err != nil {
			t.Fatal(err)
		}
		startResult := make(chan error, 1)
		killResult := make(chan error, 1)
		go func() { startResult <- i.Start() }()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := i.Kill(ctx)
			if err == nil {
				killReturned.Store(true)
			}
			killResult <- err
		}()
		startErr := <-startResult
		if err := <-killResult; err != nil {
			t.Fatal(err)
		}
		if startErr == nil {
			<-i.Done()
		}
		if ranAfterKill.Load() || i.boundary.LiveGoroutines() != 0 {
			t.Fatalf("Kill returned before main stopped: ran=%t, live=%d", ranAfterKill.Load(), i.boundary.LiveGoroutines())
		}
	}
}

func TestMainExitStopsCallWaiter(t *testing.T) {
	releaseMain := make(chan struct{})
	var callReturned, childDeferred atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go func() {
				defer childDeferred.Store(true)
				_, _ = isolatebridge.Current().Call(1, nil)
				callReturned.Store(true)
			}()
			<-releaseMain
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	command := <-i.Commands()
	close(releaseMain)
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	command.Reply(nil, nil)
	deadline := time.Now().Add(time.Second)
	for i.boundary.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if callReturned.Load() || childDeferred.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("after main exit, Call returned=%t, deferred=%t, live=%d", callReturned.Load(), childDeferred.Load(), i.boundary.LiveGoroutines())
	}
}
