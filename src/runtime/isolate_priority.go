// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateSetSpawnPriority
func isolateSetSpawnPriority(priority uint8) uint8 {
	gp := getg()
	if priority > 1 {
		panic("isolate: invalid goroutine priority")
	}
	if gp.isolateReadOnlyService || gp.isolateReadOnlyOwner != 0 {
		panic("isolate: read-only handlers cannot start priority goroutines")
	}
	previous := gp.isolateSpawnPriority
	gp.isolateSpawnPriority = priority
	return previous
}

// Priority admission lets the host run one accepted update to a blocking
// boundary before validating the next, without releasing ordinary workflow
// continuations. Normal dispatch also always prefers ready priority goroutines.
//
//go:linkname isolateResumePriority
func isolateResumePriority(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || !group.deterministic {
		panic("isolate: priority dispatch is a deterministic host operation")
	}
	systemstack(func() {
		lock(&group.dispatchLock)
		if !group.dispatchPaused || group.dispatchWaiter != 0 || group.dispatchToken != 0 {
			throw("isolate: priority dispatch requires suspension")
		}
		group.dispatchReadOnly = false
		group.dispatchInbound = false
		group.dispatchPriority = true
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
