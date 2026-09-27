// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e5a && linux && arm64

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// This narrow probe detaches the sole sync.Mutex semaphore waiter while the
// mutex remains locked. The general E5a gate also requires non-head waiters.
func TestIsolateSingleMutexWaitKillPhase0(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()
	registered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.IsolatePhase0RegisterTarget()
		close(registered)
		mu.Lock()
		mu.Unlock()
	}()
	<-registered
	var waiting bool
	for attempts := 0; attempts < 100; attempts++ {
		_, _, waiting = runtime.IsolateSuspendLatencyPhase0()
		if waiting {
			break
		}
		runtime.Gosched()
	}
	if !waiting {
		mu.Unlock()
		<-done
		t.Fatal("mutex waiter did not park")
	}
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	mu.Unlock() // cleanup only; the kill must have completed before this
	if !dead {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("mutex waiter did not resume after cleanup")
		}
		t.Fatalf("single mutex waiter survived hard kill request after %s", time.Duration(elapsed))
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("mutex-wait hard kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
}
