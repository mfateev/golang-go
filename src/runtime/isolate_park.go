// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// isolateParkForever parks without a channel, timer, or other runtime wait
// record. A group member registers so revocation can wake it; a process G
// retains the ordinary permanent park.
func isolateParkForever(reason waitReason, traceReason traceBlockReason, traceskip int) {
	gp := getg()
	group := gp.isolateGroup
	if group == nil {
		gopark(nil, nil, reason, traceReason, traceskip+1)
		throw("isolate: permanent park returned without a group")
	}
	if !group.registerPark(gp, isolateForeverRegistered, nil) {
		isolateDiscardIfRevoked()
		throw("isolate: rejected permanent park without revocation")
	}
	gopark(isolateForeverCommit, nil, reason, traceReason, traceskip+1)
	if gp.isolateParkState != isolateParkNone {
		group.unregisterPark(gp)
	}
	isolateDiscardIfRevoked()
	throw("isolate: permanent park resumed without revocation")
}

// isolateForeverCommit runs on g0 after gp becomes waiting. Revocation can
// ready gp only after this callback has marked the park as committed.
func isolateForeverCommit(gp *g, _ unsafe.Pointer) bool {
	group := gp.isolateGroup
	lockWithRank(&group.parkLock, lockRankIsolatePark)
	if group.admission.Load()&isolateRevokedBit != 0 {
		group.removePark(gp)
		unlock(&group.parkLock)
		return false
	}
	if gp.isolateParkState != isolateForeverRegistered {
		throw("isolate: permanent park committed in wrong state")
	}
	gp.isolateParkState = isolateForeverParked
	unlock(&group.parkLock)
	return true
}
