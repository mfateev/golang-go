// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package runtime

import "unsafe"

// These functions are an opt-in E4 probe for a per-goroutine global base.
// They do not redirect ordinary Go package-global accesses.

//go:linkname isolateE4SetBase
func isolateE4SetBase(base unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateE4Base
	gp.isolateE4Base = base
	return old
}

//go:linkname isolateE4GetBase
func isolateE4GetBase() unsafe.Pointer {
	return getg().isolateE4Base
}

//go:linkname isolateE4NewState
func isolateE4NewState(typ unsafe.Pointer) unsafe.Pointer {
	return newobject((*_type)(typ))
}
