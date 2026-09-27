// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase2b_revocation

package runtime_test

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func waitForIsolateGroupExit(t *testing.T, group unsafe.Pointer) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for runtime.IsolateTestGroupLive(group) != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if live := runtime.IsolateTestGroupLive(group); live != 0 {
		t.Fatalf("group still has %d live goroutines", live)
	}
}

func TestIsolateFirstDispatchRevocation(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	group := runtime.IsolateTestNewGroup()
	runtime.IsolateTestEnterGroup(group)
	var progressed atomic.Bool
	runtime.IsolateTestNoPreempt(func() {
		go func() { progressed.Store(true) }()
		runtime.IsolateTestRevokeGroup(group)
		runtime.IsolateTestLeaveGroup()
	})
	waitForIsolateGroupExit(t, group)
	if progressed.Load() {
		t.Fatal("revoked child executed its function")
	}
}

func TestIsolateGroupCompletion(t *testing.T) {
	group := runtime.IsolateTestNewGroup()
	runtime.IsolateTestEnterGroup(group)
	var progressed atomic.Bool
	go func() { progressed.Store(true) }()
	runtime.IsolateTestLeaveGroup()
	waitForIsolateGroupExit(t, group)
	if !progressed.Load() {
		t.Fatal("admitted child did not execute")
	}
}
