// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// These hooks bind the provisional isolate host transport to one goroutine.
// A child goroutine inherits the pointer in newproc.

//go:linkname isolateGetBoundary
func isolateGetBoundary() unsafe.Pointer {
	if getg().isolateMetadataDepth != 0 {
		panic("isolate: metadata services cannot call the host")
	}
	return getg().isolateBoundary
}

//go:linkname isolateSetBoundary
func isolateSetBoundary(p unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateBoundary
	gp.isolateBoundary = p
	return old
}

//go:linkname isolateActive
func isolateActive() bool {
	gp := getg()
	return gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateBoundary != nil || gp.isolateE4Bases != nil
}

func isolateRejectProcessAPI(name string) {
	if isolateActive() {
		panic("runtime." + name + " is unavailable in an isolate")
	}
}

//go:linkname isolateGetOwner
func isolateGetOwner() uintptr {
	return getg().isolateOwner
}

//go:linkname isolateSetOwner
func isolateSetOwner(id uintptr) uintptr {
	gp := getg()
	old := gp.isolateOwner
	if id != 0 && gp.isolateGroup != nil {
		cache := gp.isolateGroup.alloc.cache
		lock(&cache.isolateLock)
		if cache.isolateOwner != 0 && cache.isolateOwner != id {
			throw("isolate: group allocation owner changed")
		}
		if cache.isolateOwner == 0 {
			atomic.Storeuintptr(&cache.isolateOwner, id)
		}
		unlock(&cache.isolateLock)
	}
	gp.isolateOwner = id
	return old
}

// These hooks attach the existing first-dispatch experiment to an actual
// trusted instance. A live count alone is not quiescence or kill support.

//go:linkname isolateNewGroup
func isolateNewGroup() unsafe.Pointer {
	if isolateActive() {
		panic("isolate: cannot create a runtime group inside an isolate")
	}
	return unsafe.Pointer(&isolateRevocationGroup{alloc: newIsolateAllocHandle()})
}

//go:linkname isolateEnableDeterminism
func isolateEnableDeterminism(p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	if group.live.Load() != 0 {
		return false
	}
	group.deterministic = true
	return true
}

//go:linkname isolateDeterministic
func isolateDeterministic() bool {
	group := getg().isolateGroup
	return group != nil && group.deterministic && getg().isolateMetadataDepth == 0
}

//go:linkname isolateSetClock
func isolateSetClock(p unsafe.Pointer, unixNano int64) bool {
	group := (*isolateRevocationGroup)(p)
	for {
		old := group.clockNS.Load()
		if group.clockSet.Load() && unixNano < old {
			return false
		}
		if group.clockNS.CompareAndSwap(old, unixNano) {
			group.clockSet.Store(true)
			return true
		}
	}
}

//go:linkname isolateClockEnabled
func isolateClockEnabled() bool {
	group := getg().isolateGroup
	return group != nil && group.clockSet.Load()
}

//go:linkname isolateSetTimerSleep
func isolateSetTimerSleep(p unsafe.Pointer, fn func(int64) error) bool {
	group := (*isolateRevocationGroup)(p)
	if group.live.Load() != 0 || group.timerSleep != nil || fn == nil {
		return false
	}
	group.timerSleep = fn
	return true
}

// The time package must not import the higher-level byte transport. Its
// runtime hook calls the immutable service registered on the current group.
//
//go:linkname isolateTimerSleep
func isolateTimerSleep(ns int64) error {
	gp := getg()
	group := gp.isolateGroup
	if group == nil || !group.clockSet.Load() || group.timerSleep == nil {
		panic("time: isolate timer transport is not configured")
	}
	if gp.isolateBoundary == nil {
		// Initializers have an owner and clock but no host command consumer.
		// Preserve Call's entry requirement instead of deadlocking New.
		panic("isolate: Call outside an active isolate")
	}
	return group.timerSleep(ns)
}

//go:linkname isolateTimerChannel
func isolateTimerChannel(p unsafe.Pointer) {
	c := (*hchan)(p)
	if !isolateClockEnabled() || c == nil || c.dataqsiz != 1 || c.qcount != 0 {
		throw("isolate: invalid timer channel")
	}
	c.isolateTimer = true // Before publishing the timer or its channel.
}

//go:linkname isolateSetGroupExit
func isolateSetGroupExit(p unsafe.Pointer, fn func(int)) {
	(*isolateRevocationGroup)(p).exit = fn
}

//go:linkname isolateSetGroup
func isolateSetGroup(p unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateGroup
	next := (*isolateRevocationGroup)(p)
	if old == next {
		return unsafe.Pointer(old)
	}
	if old != nil && next != nil {
		panic("isolate: cannot nest different goroutine groups")
	}
	if old != nil {
		isolateDispatchLeave(gp)
		old.running.Add(-1)
		old.live.Add(-1)
	}
	gp.isolateGroup = next
	if next != nil {
		next.live.Add(1)
		next.running.Add(1)
		isolateDispatchEnter(gp)
	}
	return unsafe.Pointer(old)
}

//go:linkname isolateGroupLive
func isolateGroupLive(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).live.Load()
}

//go:linkname isolateGroupRunning
func isolateGroupRunning(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).running.Load()
}

//go:linkname isolateGroupRunnable
func isolateGroupRunnable(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).runnable.Load()
}

//go:linkname isolateRevokeUnstarted
func isolateRevokeUnstarted(p unsafe.Pointer) {
	(*isolateRevocationGroup)(p).revoke()
}

//go:linkname isolateMarkRevoked
func isolateMarkRevoked(p unsafe.Pointer) bool {
	return (*isolateRevocationGroup)(p).markRevoked()
}

//go:linkname isolateWakeRevoked
func isolateWakeRevoked(p unsafe.Pointer) {
	(*isolateRevocationGroup)(p).wakeRevoked()
}

// isolateLargeAllocOrigin preserves the original large-object diagnostic.
//
//go:linkname isolateLargeAllocOrigin
func isolateLargeAllocOrigin(p unsafe.Pointer) (uintptr, bool) {
	s := spanOfHeap(uintptr(p))
	if s == nil || s.spanclass.sizeclass() != 0 {
		return 0, false
	}
	return s.isolateAllocOwner, true
}

// isolateAllocOrigin reports an object's homogeneous span owner, including
// small objects and interior pointers. Static and stack addresses are excluded.
//
//go:linkname isolateAllocOrigin
func isolateAllocOrigin(p unsafe.Pointer) (uintptr, bool) {
	s := spanOfHeap(uintptr(p))
	if s == nil {
		return 0, false
	}
	return s.isolateAllocOwner, true
}
