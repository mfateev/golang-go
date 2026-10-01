// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"runtime"
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
					runtime.Gosched()
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

func TestKillPendingOnChannelWait(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err = i.Kill(ctx)
	var pending *KillPendingError
	if !errors.As(err, &pending) || pending.LiveGoroutines == 0 {
		t.Fatalf("Kill on channel wait = %v, want pending live goroutine", err)
	}
	close(release)
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

func TestKillPendingOnSelectWait(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) || pending.LiveGoroutines == 0 {
		t.Fatalf("Kill on select wait = %v, want pending live goroutine", err)
	}
	close(release)
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

func TestKillPendingSleepStopsAtTimer(t *testing.T) {
	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			time.Sleep(200 * time.Millisecond)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	var pending *KillPendingError
	if err := i.Kill(ctx); !errors.As(err, &pending) {
		t.Fatalf("Kill during Sleep = %v, want pending", err)
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

func TestKillPreventsLateMainEntry(t *testing.T) {
	runnerEntered := make(chan struct{})
	var releaseRunner atomic.Bool
	var mainRan atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			return func(fn func()) {
				close(runnerEntered)
				for !releaseRunner.Load() {
					runtime.Gosched()
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
