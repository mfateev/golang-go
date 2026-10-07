// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import _ "unsafe" // for go:linkname

const isolateRevokedBit = uint64(1) << 63

// isolateFirstDispatchRevoked admits a newly created goroutine only if its
// inherited group has not been revoked. The CAS linearizes admission with
// revocation across Ps. Running and previously parked goroutines are outside
// this first-dispatch experiment.
func isolateFirstDispatchRevoked(gp *g) bool {
	group := gp.isolateGroup
	if group == nil || gp.isolateStarted || gp.isolateMetadataDepth != 0 {
		return false
	}
	for {
		state := group.admission.Load()
		if state&isolateRevokedBit != 0 {
			return true
		}
		if state == isolateRevokedBit-1 {
			throw("isolate: admission count overflow")
		}
		if group.admission.CompareAndSwap(state, state+1) {
			gp.isolateAdmitted = true
			return false
		}
	}
}

func (group *isolateRevocationGroup) revoke() {
	if group.markRevoked() {
		group.wakeRevoked()
	}
}

// markRevoked publishes the durable admission fence. Waiter scanning is
// separately claimed by wakeRevoked, so a host Kill can finish waking a group
// whose ownership fault has already published this fence.
func (group *isolateRevocationGroup) markRevoked() bool {
	for {
		state := group.admission.Load()
		if state&isolateRevokedBit != 0 {
			return false
		}
		if group.admission.CompareAndSwap(state, state|isolateRevokedBit) {
			isolateDispatchRevoke(group)
			return true
		}
	}
}

func (group *isolateRevocationGroup) wakeRevoked() {
	if group.admission.Load()&isolateRevokedBit == 0 {
		throw("isolate: waking group before revocation")
	}
	if !group.wakeStarted.CompareAndSwap(0, 1) {
		return
	}
	isolateRevokePollWaiters(group)
	isolateRevokeParkWaiters(group)
}

// isolateExitIfRevoked is a provisional boundary fence. After a blocking
// operation, its caller must first release the waiter's runtime records and
// restore any library state needed by Goexit and the caller's defers. Channel
// and select paths use hard discard instead after cleaning their wait records.
func isolateExitIfRevoked() {
	group := getg().isolateGroup
	if group != nil && getg().isolateMetadataDepth == 0 && group.admission.Load()&isolateRevokedBit != 0 {
		Goexit()
	}
}

// isolateDiscardIfRevoked ends the current G without running Go defers. It is
// for waits on isolate-owned synchronization objects after their runtime wait
// records have been removed. Running defers can touch a lock abandoned by
// another goroutine in the same revoked isolate.
//
//go:linkname isolateDiscardIfRevoked
func isolateDiscardIfRevoked() {
	group := getg().isolateGroup
	if group == nil || getg().isolateMetadataDepth != 0 || group.admission.Load()&isolateRevokedBit == 0 {
		return
	}
	gp := getg()
	isolateNotifyResourceViolation(group)
	isolatePrepareDiscard(gp)
	mcall(isolateDiscard0)
}

func isolatePrepareDiscard(gp *g) {
	// Discard skips recovery/fatalpanic bookkeeping. Every remaining non-Goexit
	// panic still owns one increment, including when discarding from newstack.
	for p := gp._panic; p != nil; p = p.link {
		if !p.goexit && !p.deferreturn {
			runningPanicDefers.Add(-1)
		}
	}
	if raceenabled {
		if gp.bubble != nil {
			racereleasemergeg(gp, gp.bubble.raceaddr())
		}
		isolateRaceReleaseCleanup(gp)
		racectxend(gp.racectx)
	}
	trace := traceAcquire()
	if trace.ok() {
		trace.GoEnd()
		traceRelease(trace)
	}
}

func isolateDiscard0(gp *g) {
	gdestroy(gp)
	schedule()
	throw("isolate: schedule returned after discard")
}

// isolateExit revokes the current instance and ends this G without running
// user defers. The trusted host callback closes Call and publishes the exit
// status before the G is destroyed.
//
//go:linkname isolateExit
func isolateExit(code int) {
	if getg().isolateMetadataDepth != 0 {
		panic("isolate: metadata services cannot exit an instance")
	}
	group := getg().isolateGroup
	if group == nil {
		panic("isolate: Exit without a runtime group")
	}
	if group.exit != nil {
		group.exit(code)
	} else {
		group.revoke()
	}
	isolateDiscardIfRevoked()
	throw("isolate: Exit returned after revocation")
}

//go:linkname sync_runtime_isolateExitIfRevoked sync.runtime_isolateExitIfRevoked
func sync_runtime_isolateExitIfRevoked() {
	isolateExitIfRevoked()
}

// isolateTerminateBeforeStart runs on g0 after execute has made gp current
// and marked it running. No user instruction or defer has executed. This
// narrow path has no channel or timer waiter to detach.
func isolateTerminateBeforeStart(gp *g) {
	if gp.waiting != nil || gp._defer != nil {
		throw("isolate: pre-start goroutine unexpectedly owns a waiter or defer")
	}
	if raceenabled {
		if gp.bubble != nil {
			racereleasemergeg(gp, gp.bubble.raceaddr())
		}
		isolateRaceReleaseCleanup(gp)
		racectxend(gp.racectx)
	}
	trace := traceAcquire()
	if trace.ok() {
		trace.GoEnd()
		traceRelease(trace)
	}
	gdestroy(gp)
	schedule()
	throw("isolate: schedule returned after pre-start termination")
}

// Stack growth also serves runtime paths that cannot use write barriers. This
// terminal branch is admitted only for an application G with a running P and
// no runtime lock, allocator critical section, metadata service or wait record.
// Its P remains attached through notification and gdestroy, exactly as for
// ordinary discard on g0. It never returns into the stack-growth caller.
//
//go:yeswritebarrierrec
//go:systemstack
func isolateDiscardResourceStack(gp *g) {
	mp := getg().m
	if mp.p == 0 || mp.p.ptr().status != _Prunning || !isolateResourceCanDiscard(gp) {
		throw("isolate: unsafe resource discard during stack growth")
	}
	isolateNotifyResourceViolation(gp.isolateGroup)
	isolatePrepareDiscard(gp)
	isolateDiscard0(gp)
}
