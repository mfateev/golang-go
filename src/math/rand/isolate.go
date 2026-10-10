// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rand

import "unsafe"

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

//go:linkname isolateFMA32 math.isolateRandFMA32
func isolateFMA32(x, y, z float32) float32

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
	// The runtime source draws from the same replay-seeded stream as
	// math/rand/v2, crypto/rand and select. Its Read mutex also protects Rand's
	// byte remainder when native goroutines take turns through FIFO dispatch.
	r := New(new(runtimeSource))
	isolateSetRandLegacy(unsafe.Pointer(r))
	return r, true
}
