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
