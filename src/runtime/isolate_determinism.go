// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// isolateSelectRandn preserves ordinary randomized host selects. Inside an
// isolate, SplitMix64 supplies a fixed, architecture-independent sequence.
// The sequence is not a security primitive. Dispatch serializes consumers;
// the atomic also permits safe runtime inspection of the sequence.
func isolateSelectRandn(gp *g, n uint32) uint32 {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return cheaprandn(n)
	}
	return uint32(isolateMix64(group.selectSeq.Add(1)) % uint64(n))
}

func isolateMix64(sequence uint64) uint64 {
	x := sequence * 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// These streams are workflow randomness, not runtime hash or scheduler entropy.
// Keeping them separate ensures allocations, GC, and selects cannot consume
// application random values. They are deliberately not cryptographic sources.
//
//go:linkname isolateRand
func isolateRand() uint64 {
	group := getg().isolateGroup
	if group == nil || !group.deterministic {
		return rand()
	}
	return isolateMix64(group.randSeq.Add(1))
}

// math/rand retains its Go 1 generator and byte-read remainder in an opaque,
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
	if raceenabled && group.randLegacy != nil {
		raceacquire(unsafe.Pointer(&group.randLegacy))
	}
	return group.randLegacy, true
}

//go:linkname isolateSetRandLegacy
func isolateSetRandLegacy(p unsafe.Pointer) {
	group := getg().isolateGroup
	if group == nil || !group.deterministic || group.randLegacy != nil || p == nil {
		throw("isolate: invalid legacy random initialization")
	}
	if raceenabled {
		racerelease(unsafe.Pointer(&group.randLegacy))
	}
	group.randLegacy = p
}
