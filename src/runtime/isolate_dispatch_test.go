// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"time"
)

func TestDeterministicSuspensionDuringFilteredJoin(t *testing.T) {
	for _, phase := range []string{"read-only", "inbound", "priority"} {
		t.Run(phase, func(t *testing.T) { testSuspensionDuringFilteredJoin(t, phase) })
	}
}

func testSuspensionDuringFilteredJoin(t *testing.T, phase string) {
	p := runtime.IsolateDispatchReserveJoinForTest(phase)
	defer runtime.IsolateMetadataRevokeForTest(p)
	suspended := make(chan struct{})
	go func() {
		runtime.IsolateDispatchSuspendForTest(p)
		close(suspended)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !runtime.IsolateDispatchHasWaiterForTest(p) {
		if time.Now().After(deadline) {
			t.Fatal("host did not begin waiting for the counted join")
		}
		runtime.Gosched()
	}
	joined := make(chan struct{})
	go func() {
		runtime.IsolateDispatchCommitJoinForTest(p)
		close(joined)
	}()
	select {
	case <-suspended:
	case <-time.After(5 * time.Second):
		t.Fatal("filtered join did not notify the host that the group is idle")
	}
	select {
	case <-joined:
		t.Fatalf("ordinary join ran during %s dispatch", phase)
	default:
	}
	runtime.IsolateDispatchResumeForTest(p)
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("queued join did not run after ordinary dispatch resumed")
	}
}
