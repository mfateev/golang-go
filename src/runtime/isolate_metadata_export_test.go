// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

func IsolateMetadataGroupForTest() unsafe.Pointer          { return isolateNewGroup() }
func IsolateMetadataRevokeForTest(p unsafe.Pointer)        { isolateRevokeUnstarted(p) }
func IsolateMetadataLiveForTest(p unsafe.Pointer) int32    { return isolateGroupLive(p) }
func IsolateMetadataRunningForTest(p unsafe.Pointer) int32 { return isolateGroupRunning(p) }

func IsolateMetadataOwnerForTest() uintptr   { return isolateGetOwner() }
func IsolateMetadataDepthForTest() uint32    { return getg().isolateMetadataDepth }
func IsolateMetadataGCWorkersForTest() int32 { return gcBgMarkWorkerCount }
func IsolateMetadataScopeForTest(fn func()) {
	owner := isolateEnterMetadata()
	defer isolateLeaveMetadata(owner)
	fn()
}

func IsolateMetadataRunForTest(p unsafe.Pointer, owner uintptr, fn func()) {
	group := isolateSetGroup(p)
	defer isolateSetGroup(group)
	previous := isolateSetOwner(owner)
	defer isolateSetOwner(previous)
	fn()
}

func IsolateAllocOriginForTest(p unsafe.Pointer) (uintptr, bool) {
	return isolateAllocOrigin(p)
}

//go:noinline
func IsolateMetadataBytesForTest(size int) []byte { return make([]byte, size) }

//go:noinline
func IsolatePointerSliceForTest(count int) []*int { return make([]*int, count) }

func IsolateCachePointerForTest() unsafe.Pointer {
	return unsafe.Pointer(getg().isolateGroup.alloc.cache)
}

func IsolateCachePresentForTest(owner uintptr) bool {
	found := false
	systemstack(func() {
		lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
		for c := isolateAllocRegistry.head; c != nil; c = c.isolateNext {
			if atomic.Loaduintptr(&c.isolateOwner) == owner {
				found = true
				break
			}
		}
		unlock(&isolateAllocRegistry.lock)
	})
	return found
}

func IsolateMetadataSetExitForTest(group unsafe.Pointer, fn func(int)) {
	isolateSetGroupExit(group, fn)
}

func IsolateMetadataDeterministicForTest(group unsafe.Pointer) bool {
	return isolateEnableDeterminism(group)
}
func IsolateMetadataSuspendForTest(group unsafe.Pointer) { isolateSuspend(group) }
func IsolateMetadataSuspendWaitingForTest(p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	var waiting bool
	systemstack(func() {
		lock(&group.dispatchLock)
		waiting = group.dispatchWaiter != 0
		unlock(&group.dispatchLock)
	})
	return waiting
}

// Model both Darwin preemption callers even on non-Darwin test machines.
func ExecPreemptionLockOrderForTest() {
	systemstack(func() {
		lock(&sched.lock)
		lock(&allpLock)
		execLock.rlock()
		execLock.runlock()
		unlock(&allpLock)
		unlock(&sched.lock)
	})
}
