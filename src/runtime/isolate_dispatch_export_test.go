// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// Reserve a counted admission without publishing its join commit. This lets
// tests install the host suspension waiter in that exact admission window.
func IsolateDispatchReserveJoinForTest(phase string) unsafe.Pointer {
	p := isolateNewGroup()
	group := (*isolateRevocationGroup)(p)
	isolateEnableDeterminism(p)
	group.dispatchReadOnly = phase == "read-only"
	group.dispatchInbound = phase == "inbound"
	group.dispatchPriority = phase == "priority"
	group.dispatchJoining = 1
	group.live.Add(1)
	return p
}

func IsolateDispatchCommitJoinForTest(p unsafe.Pointer) {
	gp := getg()
	group := (*isolateRevocationGroup)(p)
	gp.isolateGroup = group
	group.running.Add(1)
	isolateAttachResources(gp)
	defer isolateSetGroup(nil)
	gopark(isolateDispatchJoin, p, waitReasonZero, traceBlockGeneric, 1)
}

func IsolateDispatchHasWaiterForTest(p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	lock(&group.dispatchLock)
	waiting := group.dispatchWaiter != 0
	unlock(&group.dispatchLock)
	return waiting
}

func IsolateDispatchSuspendForTest(p unsafe.Pointer) { isolateSuspend(p) }
func IsolateDispatchResumeForTest(p unsafe.Pointer)  { isolateResume(p) }
