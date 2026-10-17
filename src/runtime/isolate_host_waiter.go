// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

type isolateWaiterHandle struct{ waiter *isolateHostWaiter }

// All mutable fields are protected by group.parkLock. The group roots pending
// borrows until Wake; neither a discarded G nor a saved public handle is needed
// to keep that lifecycle record alive. There is no application data field.
type isolateHostWaiter struct {
	group                                    *isolateRevocationGroup
	context                                  *isolateHostContext
	handle                                   *isolateWaiterHandle
	next                                     *isolateHostWaiter
	caller                                   guintptr
	goid                                     uint64
	used, parked, completed, revoked, borrow bool
}

//go:linkname isolateNewHostWaiter
func isolateNewHostWaiter() unsafe.Pointer {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateMetadataDepth != 0 || gp.isolateConstruction != nil {
		panic("isolate: NewWaiter requires an isolate caller")
	}
	isolateDiscardIfRevoked()
	owner := gp.isolateOwner
	gp.isolateMetadataDepth++
	gp.isolateOwner = 0
	w := &isolateHostWaiter{group: gp.isolateGroup, caller: guintptr(unsafe.Pointer(gp)), goid: gp.goid}
	h := &isolateWaiterHandle{waiter: w}
	w.handle = h
	gp.isolateOwner = owner
	gp.isolateMetadataDepth--
	if isolateHostExecution(gp) {
		if !isolateAcquireHostWork(w.group) {
			panic(isolateConstructionRevoked{})
		}
		w.context, w.borrow = gp.isolateHost, true
	}
	w.group.resources.account.hostIntegration.Store(true)
	lockWithRank(&w.group.parkLock, lockRankIsolatePark)
	w.revoked = w.group.admission.Load()&isolateRevokedBit != 0
	if !w.revoked || w.borrow {
		w.next, w.group.hostWaiters = w.group.hostWaiters, w
	}
	unlock(&w.group.parkLock)
	return unsafe.Pointer(h)
}

func isolateValidateWaiter(p unsafe.Pointer) *isolateHostWaiter {
	h := (*isolateWaiterHandle)(p)
	if h == nil || h.waiter == nil || h.waiter.handle != h {
		panic("isolate: invalid or copied Waiter")
	}
	return h.waiter
}

//go:linkname isolateParkHostWaiter
func isolateParkHostWaiter(p unsafe.Pointer) {
	w := isolateValidateWaiter(p)
	gp := getg()
	if w.caller.ptr() != gp || w.goid != gp.goid {
		panic("isolate: Waiter.Park on wrong goroutine")
	}
	lockWithRank(&w.group.parkLock, lockRankIsolatePark)
	if w.used {
		unlock(&w.group.parkLock)
		panic("isolate: Waiter.Park called more than once")
	}
	w.used = true
	if !w.completed && !w.revoked {
		w.parked = true
		// parkLock remains held until gp is waiting. Wake and revocation cannot
		// publish a ready G before this park commits and transfers its token.
		gopark(isolateHostWaitCommit, unsafe.Pointer(w), waitReasonIsolateHost, traceBlockGeneric, 1)
	} else {
		unlock(&w.group.parkLock)
	}
	if raceenabled {
		raceacquire(unsafe.Pointer(w))
	}
	if w.group.admission.Load()&isolateRevokedBit != 0 {
		if gp.isolateHost != nil {
			Goexit()
		} // Unwind host cleanup to deferred Leave.
		isolateDiscardIfRevoked()
	}
}

func isolateHostWaitCommit(gp *g, p unsafe.Pointer) bool {
	w := (*isolateHostWaiter)(p)
	unlock(&w.group.parkLock)
	return true
}

//go:linkname isolateWakeHostWaiter
func isolateWakeHostWaiter(p unsafe.Pointer) {
	w := isolateValidateWaiter(p)
	lockWithRank(&w.group.parkLock, lockRankIsolatePark)
	if w.completed {
		unlock(&w.group.parkLock)
		return
	}
	if raceenabled {
		racerelease(unsafe.Pointer(w))
	}
	w.completed = true
	var target *g
	if w.parked {
		target, w.parked = w.caller.ptr(), false
	}
	link := &w.group.hostWaiters
	for *link != nil && *link != w {
		link = &(*link).next
	}
	if *link == w {
		*link = w.next
	}
	w.next, w.context = nil, nil
	borrow := w.borrow
	w.borrow = false
	unlock(&w.group.parkLock)
	if target != nil {
		goready(target, 1)
	}
	if borrow {
		isolateReleaseHostWork(w.group)
	}
}

func isolateRevokeHostWaiters(group *isolateRevocationGroup) {
	var readyList gList
	lockWithRank(&group.parkLock, lockRankIsolatePark)
	link := &group.hostWaiters
	for *link != nil {
		w := *link
		w.revoked = true
		if w.parked {
			w.parked = false
			readyList.push(w.caller.ptr())
		}
		if !w.borrow {
			*link, w.next = w.next, nil
		} else {
			link = &w.next
		}
	}
	unlock(&group.parkLock)
	for gp := readyList.pop(); gp != nil; gp = readyList.pop() {
		goready(gp, 1)
	}
}
