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
	if group == nil || gp.isolateStarted {
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
	for {
		state := group.admission.Load()
		if state&isolateRevokedBit != 0 {
			return
		}
		if group.admission.CompareAndSwap(state, state|isolateRevokedBit) {
			isolateRevokePollWaiters(group)
			isolateRevokeSleepWaiters(group)
			return
		}
	}
}

// isolateExitIfRevoked is a provisional boundary fence. After a blocking
// operation, its caller must first release the waiter's runtime records and
// restore any library state needed by Goexit and the caller's defers.
func isolateExitIfRevoked() {
	group := getg().isolateGroup
	if group != nil && group.admission.Load()&isolateRevokedBit != 0 {
		Goexit()
	}
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
