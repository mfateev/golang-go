// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
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
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main:     func() { _, _ = isolatebridge.Current().Call(1, nil) },
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
	if i.boundary.LiveGoroutines() != 0 {
		t.Fatal("Kill returned with live goroutines")
	}
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("repeated Kill = %v", err)
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

func TestKillPendingOnWaitGroupWait(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) || pending.LiveGoroutines == 0 {
		t.Fatalf("Kill on WaitGroup.Wait = %v, want pending live goroutine", err)
	}
	wg.Done()
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
	var wg sync.WaitGroup
	var release atomic.Bool
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
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
	childExited := make(chan struct{})
	var callReturned atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go func() {
				defer close(childExited)
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
	<-childExited
	command.Reply(nil, nil)
	deadline := time.Now().Add(time.Second)
	for i.boundary.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if callReturned.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("after main exit, Call returned=%t, live=%d", callReturned.Load(), i.boundary.LiveGoroutines())
	}
}
