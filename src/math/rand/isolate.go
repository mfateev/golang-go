// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rand

import "unsafe"

//go:linkname isolateRandLegacy runtime.isolateRandLegacy
func isolateRandLegacy() (unsafe.Pointer, bool)

//go:linkname isolateSetRandLegacy runtime.isolateSetRandLegacy
func isolateSetRandLegacy(unsafe.Pointer)

func isolateGlobalRand() (*Rand, bool) {
	p, deterministic := isolateRandLegacy()
	if !deterministic {
		return nil, false
	}
	if p != nil {
		return (*Rand)(p), true
	}
	// Each deterministic instance starts at seed 1, independently of the
	// host generator, GODEBUG, and the runtime's hashing and select streams.
	r := New(new(lockedSource))
	r.Seed(1)
	isolateSetRandLegacy(unsafe.Pointer(r))
	return r, true
}
