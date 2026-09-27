// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e5a && phase0_e5a_acceptance && linux && arm64

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// A target behind another waiter must be removed without waking or damaging
// that waiter. This exercises the per-address semaRoot waitlink list.
func TestIsolateNonHeadMutexWaitHardKillAcceptancePhase0(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	firstReady, firstDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(firstDone)
		runtime.IsolatePhase0RegisterTarget()
		close(firstReady)
		mu.Lock()
		mu.Unlock()
	}()
	<-firstReady
	waitForRegisteredTargetToBlock(t)

	secondReady, secondDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(secondDone)
		runtime.IsolatePhase0RegisterTarget()
		close(secondReady)
		mu.Lock()
		mu.Unlock()
	}()
	<-secondReady
	waitForRegisteredTargetToBlock(t)

	beforeDetach := runtime.IsolatePhase0NonHeadDetachCount()
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	if dead && runtime.IsolatePhase0NonHeadDetachCount() != beforeDetach+1 {
		t.Fatal("target was not removed from the non-head semaphore wait list")
	}
	mu.Unlock() // release only after the kill request completed
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first mutex waiter did not resume")
	}
	if !dead {
		select {
		case <-secondDone:
		case <-time.After(time.Second):
			t.Fatal("second mutex waiter did not resume after failed kill")
		}
		t.Fatalf("E5a open: non-head mutex waiter survived kill request after %s", time.Duration(elapsed))
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("non-head waiter kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	select {
	case <-secondDone:
		t.Fatal("hard kill ran the target's defer")
	default:
	}
}

// The remaining E5a gate: sync.Cond uses notifyList rather than semaRoot.
// A hard kill must unlink its ticketed waiter before the host signals.
func TestIsolateCondWaitHardKillAcceptancePhase0(t *testing.T) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	registered, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		mu.Lock()
		runtime.IsolatePhase0RegisterTarget()
		close(registered)
		cond.Wait()
		mu.Unlock()
	}()
	<-registered
	waitForRegisteredTargetToBlock(t)
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	cond.Signal() // cleanup or stale-wait-record check only after kill
	if !dead {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cond waiter did not resume after failed kill")
		}
		t.Fatalf("E5a open: sync.Cond waiter survived kill request after %s", time.Duration(elapsed))
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("cond-wait kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	select {
	case <-done:
		t.Fatal("hard kill ran the cond waiter's defer")
	default:
	}
}

// When the target owns the newest Cond ticket, killing it must not consume
// the Signal intended for the earlier live waiter.
func TestIsolateMultiCondWaitHardKillAcceptancePhase0(t *testing.T) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	startWaiter := func() <-chan struct{} {
		registered, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			mu.Lock()
			runtime.IsolatePhase0RegisterTarget()
			close(registered)
			cond.Wait()
			mu.Unlock()
		}()
		<-registered
		waitForRegisteredTargetToBlock(t)
		return done
	}
	firstDone := startWaiter()
	secondDone := startWaiter()
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	cond.Signal() // one signal must wake the surviving earlier waiter
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first Cond waiter did not resume")
	}
	if !dead {
		cond.Broadcast() // release the target when the experimental kill fails
		select {
		case <-secondDone:
		case <-time.After(time.Second):
			t.Fatal("second Cond waiter did not resume after failed kill")
		}
		t.Fatalf("E5a open: newest sync.Cond waiter survived hard kill after %s", time.Duration(elapsed))
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("multi-Cond kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	select {
	case <-secondDone:
		t.Fatal("hard kill ran the target's defer")
	default:
	}
	thirdReady, thirdDone := make(chan struct{}), make(chan struct{})
	go func() {
		mu.Lock()
		close(thirdReady)
		cond.Wait()
		mu.Unlock()
		close(thirdDone)
	}()
	<-thirdReady
	mu.Lock() // the third waiter has obtained a ticket before releasing mu
	mu.Unlock()
	cond.Signal()
	select {
	case <-thirdDone:
	case <-time.After(time.Second):
		t.Fatal("new Cond waiter did not resume after ticket reuse")
	}
}

// Killing an earlier Cond ticket still requires a way to preserve the next
// live waiter's Signal. This is the deliberately failing E5a acceptance case.
func TestIsolateNonTailCondWaitHardKillAcceptancePhase0(t *testing.T) {
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	firstReady, firstDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(firstDone)
		mu.Lock()
		runtime.IsolatePhase0RegisterTarget()
		close(firstReady)
		cond.Wait()
		mu.Unlock()
	}()
	<-firstReady
	waitForRegisteredTargetToBlock(t)

	secondReady, secondDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(secondDone)
		mu.Lock()
		close(secondReady)
		cond.Wait()
		mu.Unlock()
	}()
	<-secondReady
	deadline := time.Now().Add(time.Second)
	for runtime.IsolatePhase0CondWaiterCount() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("second Cond waiter did not join the notify list")
		}
		runtime.Gosched()
	}

	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	cond.Broadcast() // release any surviving waiter after the kill request
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second Cond waiter did not resume")
	}
	if !dead {
		select {
		case <-firstDone:
		case <-time.After(time.Second):
			t.Fatal("first Cond waiter did not resume after failed kill")
		}
		t.Fatalf("E5a open: earlier sync.Cond ticket survived hard kill after %s", time.Duration(elapsed))
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("non-tail Cond kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	select {
	case <-firstDone:
		t.Fatal("hard kill ran the target's defer")
	default:
	}
}

func waitForRegisteredTargetToBlock(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, _, waiting := runtime.IsolateSuspendLatencyPhase0()
		if waiting {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("mutex waiter did not block")
}
