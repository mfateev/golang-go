// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateReadOnlyOwner
func isolateReadOnlyOwner() uintptr { return getg().isolateReadOnlyOwner }

//go:linkname isolateMarkReadOnlyService
func isolateMarkReadOnlyService() {
	gp := getg()
	if gp.isolateGroup == nil || !gp.isolateGroup.deterministic {
		panic("isolate: read-only service requires deterministic dispatch")
	}
	gp.isolateReadOnlyService = true
}

// The host prepares a separate allocation owner before delivering a query or
// validator. It shares the instance's resource account, not its writable heap.
//
//go:linkname isolatePrepareReadOnly
func isolatePrepareReadOnly(p unsafe.Pointer, owner uintptr) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || !group.deterministic || owner == 0 {
		panic("isolate: invalid read-only preparation")
	}
	if group.readOnlyAlloc != nil {
		return
	}
	alloc := newIsolateAllocHandle()
	alloc.cache.isolateOwner = owner
	alloc.cache.isolateResources = group.resources.account
	alloc.cache.isolateMetadataBytes = uint64(unsafe.Sizeof(mcache{}) + unsafe.Sizeof(isolateAllocHandle{}))
	group.resources.account.refs.Add(1)
	group.resources.account.addMetadata(alloc.cache.isolateMetadataBytes)
	group.readOnlyAlloc = alloc
}

//go:linkname isolateBeginReadOnly
func isolateBeginReadOnly() {
	gp := getg()
	if !gp.isolateReadOnlyService || gp.isolateGroup.readOnlyAlloc == nil {
		panic("isolate: missing read-only host admission")
	}
	if gp.isolateReadOnlyOwner != 0 {
		return
	}
	gp.isolateReadOnlyOwner = gp.isolateOwner
	gp.isolateOwner = gp.isolateGroup.readOnlyAlloc.cache.isolateOwner
	isolateReadOnlyLibraries()
}

func isolateCheckReadOnlyBlock() {
	gp := getg()
	if gp.isolateReadOnlyOwner != 0 && gp.isolateCallSelectNext == 0 && gp.isolateMetadataDepth == 0 && !gp.isolateRuntimeWait {
		panic("isolate: read-only handlers cannot use blocking channels or select")
	}
}

// These queue operations preserve the relative FIFO order of every workflow
// goroutine. A query cannot consume or release their execution token.
func isolateDispatchHasReadyLocked(group *isolateRevocationGroup) bool {
	if !group.dispatchReadOnly {
		return !group.dispatchQueue.empty()
	}
	for p := group.dispatchQueue.head; p != 0; p = p.ptr().schedlink {
		if p.ptr().isolateReadOnlyService {
			return true
		}
	}
	return false
}

func isolateDispatchPopLocked(group *isolateRevocationGroup) *g {
	if !group.dispatchReadOnly {
		return group.dispatchQueue.pop()
	}
	var kept gQueue
	var next *g
	for {
		next = group.dispatchQueue.pop()
		if next == nil || next.isolateReadOnlyService {
			break
		}
		kept.pushBack(next)
	}
	kept.pushBackAll(group.dispatchQueue)
	group.dispatchQueue = kept
	return next
}

// Freeze changes admission at the next park, including a suspension already
// in progress. Workflow continuations remain queued until Resume or Kill.
//
//go:linkname isolateFreezeWorkflow
func isolateFreezeWorkflow(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || !group.deterministic {
		panic("isolate: freezing is a deterministic host operation")
	}
	systemstack(func() {
		var waiter *g
		lock(&group.dispatchLock)
		group.dispatchReadOnly = true
		if group.dispatchToken == 0 && !isolateDispatchHasReadyLocked(group) && group.dispatchWaiter != 0 && group.dispatchJoining == 0 {
			group.dispatchPaused = true
			waiter = group.dispatchWaiter.ptr()
			group.dispatchWaiter = 0
		}
		unlock(&group.dispatchLock)
		if waiter != nil {
			ready(waiter, 0, false)
		}
	})
}

//go:linkname isolateResumeReadOnly
func isolateResumeReadOnly(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || !group.deterministic {
		panic("isolate: read-only dispatch is a deterministic host operation")
	}
	systemstack(func() {
		lock(&group.dispatchLock)
		if !group.dispatchPaused || group.dispatchWaiter != 0 || group.dispatchToken != 0 {
			throw("isolate: read-only dispatch requires suspension")
		}
		group.dispatchReadOnly = true
		group.dispatchPaused = false
		next := isolateDispatchPopLocked(group)
		if next != nil {
			next.isolateDispatchQueued = false
			group.dispatchToken.set(next)
		}
		unlock(&group.dispatchLock)
		if next != nil {
			runqput(getg().m.p.ptr(), next, false)
			wakep()
		}
	})
}

//go:linkname isolateInReadOnly
func isolateInReadOnly() bool { return getg().isolateReadOnlyOwner != 0 }

//go:linkname isolateReadOnlyWrite
func isolateReadOnlyWrite(p unsafe.Pointer, size uintptr) {
	gp := getg()
	if gp.isolateReadOnlyOwner != 0 && gp.isolateMetadataDepth == 0 {
		isolateCheckHeapAccess(p, size, true)
	}
}

//go:linkname isolateReadOnlySyncWait
func isolateReadOnlySyncWait() {
	gp := getg()
	if gp.isolateReadOnlyOwner != 0 && gp.isolateMetadataDepth == 0 {
		panic("isolate: read-only handlers cannot use blocking synchronization")
	}
}
