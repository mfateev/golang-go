// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// isolateDispatchReady gates publication to ordinary scheduler queues. Only
// the execution token holder is visible there; other runnable members stay
// in the group FIFO. No allocation, write barrier, or other lock is allowed
// while dispatchLock is held (it is a leaf runtime lock).
//
//go:nowritebarrierrec
func isolateDispatchReady(gp *g) bool {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return true
	}
	lock(&group.dispatchLock)
	if group.dispatchToken.ptr() == gp && !gp.isolateDispatchPark {
		unlock(&group.dispatchLock)
		return true // Preemption/runtime suspension resumes the same token.
	}
	if gp.isolateDispatchQueued {
		throw("isolate: duplicate runnable publication")
	}
	if group.dispatchToken == 0 && !group.dispatchPaused {
		group.dispatchToken.set(gp)
		unlock(&group.dispatchLock)
		return true
	}
	gp.isolateDispatchQueued = true
	group.dispatchQueue.pushBack(gp)
	unlock(&group.dispatchLock)
	return false
}

func isolateDispatchEnter(gp *g) {
	group := gp.isolateGroup
	if !group.deterministic {
		return
	}
	lock(&group.dispatchLock)
	if group.dispatchToken == 0 && !group.dispatchPaused {
		group.dispatchToken.set(gp)
		unlock(&group.dispatchLock)
		return
	}
	unlock(&group.dispatchLock)
	gopark(isolateDispatchJoin, unsafe.Pointer(group), waitReasonZero, traceBlockGeneric, 1)
}

func isolateDispatchJoin(gp *g, p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	lock(&group.dispatchLock)
	if group.dispatchToken == 0 && !group.dispatchPaused {
		group.dispatchToken.set(gp)
		unlock(&group.dispatchLock)
		return false
	}
	unlock(&group.dispatchLock)
	// This is a runnable member waiting for a dispatch token, not a user
	// synchronization wait. Keep its stack visible to the ordinary GC scan.
	casgstatus(gp, _Gwaiting, _Grunnable)
	trace := traceAcquire()
	if trace.ok() {
		trace.GoUnpark(gp, 0)
		traceRelease(trace)
	}
	if isolateDispatchReady(gp) {
		runqput(getg().m.p.ptr(), gp, false)
		wakep()
	}
	return true
}

func isolateDispatchLeave(gp *g) {
	if gp.isolateGroup.deterministic {
		systemstack(func() { isolateDispatchRelease(gp, false) })
	}
}

// Called only after a user park has committed, an explicit yield has dropped
// its M, or a goroutine has finished. The next token is reserved before its
// publication to the process queue, avoiding a false idle observation.
func isolateDispatchRelease(gp *g, requeue bool) {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return
	}
	var next, waiter *g
	lock(&group.dispatchLock)
	if group.dispatchToken.ptr() != gp {
		unlock(&group.dispatchLock)
		return // A joining member never owned the execution token.
	}
	gp.isolateDispatchPark = false
	if requeue {
		if gp.isolateDispatchQueued {
			throw("isolate: queued yielding goroutine")
		}
		gp.isolateDispatchQueued = true
		group.dispatchQueue.pushBack(gp)
	}
	group.dispatchToken = 0
	if !group.dispatchPaused {
		next = group.dispatchQueue.pop()
		if next != nil {
			next.isolateDispatchQueued = false
			group.dispatchToken.set(next)
		} else if group.dispatchWaiter != 0 {
			group.dispatchPaused = true
			waiter = group.dispatchWaiter.ptr()
			group.dispatchWaiter = 0
		}
	}
	unlock(&group.dispatchLock)
	if next != nil {
		runqput(getg().m.p.ptr(), next, false)
		wakep()
	}
	if waiter != nil {
		ready(waiter, 0, false)
	}
}

func isolateDispatchParkBegin(gp *g) bool {
	group := gp.isolateGroup
	if group == nil || !group.deterministic || gp.isolateRuntimeWait || gp.isolateMetadataDepth != 0 {
		return false
	}
	// Runtime-internal GC, stack, trace, and semaphore waits retain the token.
	// Wall-time-dependent housekeeping must not select a different user G.
	w := gp.waitreason
	if !(w.isChanWait() || w.isSyncWait() || w == waitReasonChanReceiveNilChan ||
		w == waitReasonChanSendNilChan || w == waitReasonSelectNoCases || w == waitReasonSleep) {
		return false
	}
	lock(&group.dispatchLock)
	transfer := group.dispatchToken.ptr() == gp
	if transfer {
		gp.isolateDispatchPark = true
	}
	unlock(&group.dispatchLock)
	return transfer
}

func isolateDispatchParkEnd(gp *g, committed bool) {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return
	}
	if committed && gp.isolateDispatchPark {
		isolateDispatchRelease(gp, false)
	} else if !committed {
		lock(&group.dispatchLock)
		gp.isolateDispatchPark = false
		unlock(&group.dispatchLock)
	}
}

//go:linkname isolateSuspend
func isolateSuspend(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if !group.deterministic {
		panic("isolate: suspension requires deterministic dispatch")
	}
	if getg().isolateGroup != nil {
		panic("isolate: suspension is a host operation")
	}
	gopark(isolateSuspendCommit, p, waitReasonZero, traceBlockGeneric, 1)
}

func isolateSuspendCommit(gp *g, p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	lock(&group.dispatchLock)
	if group.admission.Load()&isolateRevokedBit != 0 {
		unlock(&group.dispatchLock)
		return false
	}
	if group.dispatchWaiter != 0 {
		throw("isolate: concurrent suspension waiters")
	}
	if group.dispatchToken == 0 && group.dispatchQueue.empty() {
		group.dispatchPaused = true
		unlock(&group.dispatchLock)
		return false
	}
	group.dispatchWaiter.set(gp)
	unlock(&group.dispatchLock)
	return true
}

//go:linkname isolateResume
func isolateResume(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if !group.deterministic {
		panic("isolate: resume requires deterministic dispatch")
	}
	if getg().isolateGroup != nil {
		panic("isolate: resume is a host operation")
	}
	var next *g
	systemstack(func() {
		lock(&group.dispatchLock)
		if group.dispatchWaiter != 0 {
			throw("isolate: resume before suspension finished")
		}
		group.dispatchPaused = false
		if group.dispatchToken == 0 {
			next = group.dispatchQueue.pop()
			if next != nil {
				next.isolateDispatchQueued = false
				group.dispatchToken.set(next)
			}
		}
		unlock(&group.dispatchLock)
		if next != nil {
			runqput(getg().m.p.ptr(), next, false)
			wakep()
		}
	})
}

// Revocation must drain token waiters even if the host left the group paused.
func isolateDispatchRevoke(group *isolateRevocationGroup) {
	if !group.deterministic {
		return
	}
	systemstack(func() {
		var next, waiter *g
		lock(&group.dispatchLock)
		group.dispatchPaused = false
		waiter = group.dispatchWaiter.ptr()
		group.dispatchWaiter = 0
		if group.dispatchToken == 0 {
			next = group.dispatchQueue.pop()
			if next != nil {
				next.isolateDispatchQueued = false
				group.dispatchToken.set(next)
			}
		}
		unlock(&group.dispatchLock)
		if next != nil {
			runqput(getg().m.p.ptr(), next, false)
			wakep()
		}
		if waiter != nil {
			ready(waiter, 0, false)
		}
	})
}
