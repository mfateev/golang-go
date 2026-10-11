// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateSetInboundService
func isolateSetInboundService(enabled bool) bool {
	gp := getg()
	if gp.isolateReadOnlyService || gp.isolateReadOnlyOwner != 0 {
		panic("isolate: read-only handlers cannot start inbound services")
	}
	previous := gp.isolateInboundService
	gp.isolateInboundService = enabled
	return previous
}

func isolateDispatchEligible(group *isolateRevocationGroup, gp *g) bool {
	return (!group.dispatchReadOnly || gp.isolateReadOnlyService) &&
		(!group.dispatchInbound || gp.isolateInboundService) &&
		(!group.dispatchPriority || gp.isolatePriority != 0 && !gp.isolateReadOnlyService)
}

// Inbound services share the writable workflow owner. Sending into workflow
// channels wakes receivers, but those continuations keep their FIFO positions
// until the host has finished admitting the complete batch of input.
//
//go:linkname isolateResumeInbound
func isolateResumeInbound(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || !group.deterministic {
		panic("isolate: inbound dispatch is a deterministic host operation")
	}
	systemstack(func() {
		lock(&group.dispatchLock)
		if !group.dispatchPaused || group.dispatchWaiter != 0 || group.dispatchToken != 0 {
			throw("isolate: inbound dispatch requires suspension")
		}
		group.dispatchReadOnly = false
		group.dispatchInbound = true
		group.dispatchPriority = false
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
