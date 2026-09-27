// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

// isolateFirstDispatchRevoked admits a newly created goroutine only if its
// inherited group has not been revoked. Running and previously parked
// goroutines are deliberately outside this first-dispatch experiment.
func isolateFirstDispatchRevoked(gp *g) bool {
	group := gp.isolateGroup
	return group != nil && !gp.isolateStarted && group.revoked.Load()
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
