// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// isolateSelectRandn preserves ordinary randomized host selects. Inside an
// isolate, the shared replay-seeded generator supplies the shuffle draws.
func isolateSelectRandn(gp *g, n uint32) uint32 {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return cheaprandn(n)
	}
	value := uint64(0)
	if gp.isolateCallSelectNext != 0 {
		if gp.isolateCallSelectRemaining == 0 {
			throw("isolate: unexpected select in Call transport")
		}
		value = gp.isolateCallSelectValues[gp.isolateCallSelectNext-1]
		gp.isolateCallSelectNext++
		gp.isolateCallSelectRemaining--
	} else {
		value = isolateRandomUint64(gp)
	}
	return uint32(value % uint64(n))
}

// Top-level math/rand APIs share the replay stream with crypto/rand and select.
// Its publicly reproducible seed does not provide cryptographic security.
//
//go:linkname isolateRand
func isolateRand() uint64 {
	gp := getg()
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return rand()
	}
	return isolateRandomUint64(gp)
}

// math/rand retains its distribution facade and byte-read remainder in an opaque,
// GC-visible pointer. FIFO dispatch serializes initialization and all consumers.
// The pointer belongs to the group, so returning to it after suspension or a
// second Run must preserve the stream rather than reseeding it.
//
//go:linkname isolateRandLegacy
func isolateRandLegacy() (unsafe.Pointer, bool) {
	group := getg().isolateGroup
	if group == nil || !group.deterministic {
		return nil, false
	}
	if gp := getg(); gp.isolateReadOnlyOwner != 0 {
		return gp.isolateReadOnlyRandLegacy, true
	}
	if raceenabled && group.randLegacy != nil {
		raceacquire(unsafe.Pointer(&group.randLegacy))
	}
	return group.randLegacy, true
}

//go:linkname isolateSetRandLegacy
func isolateSetRandLegacy(p unsafe.Pointer) {
	gp := getg()
	group := gp.isolateGroup
	if gp.isolateReadOnlyOwner != 0 {
		if gp.isolateReadOnlyRandLegacy != nil || p == nil {
			throw("isolate: invalid read-only random initialization")
		}
		gp.isolateReadOnlyRandLegacy = p
		return
	}
	if group == nil || !group.deterministic || group.randLegacy != nil || p == nil {
		throw("isolate: invalid legacy random initialization")
	}
	if raceenabled {
		racerelease(unsafe.Pointer(&group.randLegacy))
	}
	group.randLegacy = p
}

// Call has two two-case selects. Reserve both polls before its first park:
// host receipt of the request must not consume workflow randomness later.
//
//go:linkname isolateCallSelectBegin
func isolateCallSelectBegin() {
	gp := getg()
	if gp.isolateGroup == nil || !gp.isolateGroup.deterministic {
		return
	}
	if gp.isolateCallSelectNext != 0 {
		throw("isolate: nested Call transport")
	}
	for i := range gp.isolateCallSelectValues {
		gp.isolateCallSelectValues[i] = isolateRandomUint64(gp)
	}
	gp.isolateCallSelectNext = 1
	gp.isolateCallSelectRemaining = 4
}

//go:linkname isolateCallSelectEnd
func isolateCallSelectEnd() {
	gp := getg()
	if gp.isolateCallSelectNext == 0 {
		return
	}
	if gp.isolateCallSelectRemaining != 0 {
		throw("isolate: incomplete Call select polls")
	}
	gp.isolateCallSelectNext = 0
}
