// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// These hooks bind the provisional isolate host transport to one goroutine.
// A child goroutine inherits the pointer in newproc.

//go:linkname isolateGetBoundary
func isolateGetBoundary() unsafe.Pointer {
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
	return gp.isolateOwner != 0 || gp.isolateBoundary != nil || gp.isolateE4Bases != nil
}

//go:linkname isolateGetOwner
func isolateGetOwner() uintptr {
	return getg().isolateOwner
}

//go:linkname isolateSetOwner
func isolateSetOwner(id uintptr) uintptr {
	gp := getg()
	old := gp.isolateOwner
	gp.isolateOwner = id
	return old
}

// These hooks attach the existing first-dispatch experiment to an actual
// trusted instance. A live count alone is not quiescence or kill support.

//go:linkname isolateNewGroup
func isolateNewGroup() unsafe.Pointer {
	return unsafe.Pointer(new(isolateRevocationGroup))
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
		old.live.Add(-1)
	}
	gp.isolateGroup = next
	if next != nil {
		next.live.Add(1)
	}
	return unsafe.Pointer(old)
}

//go:linkname isolateGroupLive
func isolateGroupLive(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).live.Load()
}

// isolateLargeAllocOrigin is a diagnostic for live large heap objects. Small
// object spans still mix allocation contexts and cannot report an owner.
//
//go:linkname isolateLargeAllocOrigin
func isolateLargeAllocOrigin(p unsafe.Pointer) (uintptr, bool) {
	s := spanOfHeap(uintptr(p))
	if s == nil || s.spanclass.sizeclass() != 0 {
		return 0, false
	}
	return s.isolateAllocOwner, true
}
