// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

// isolateSelectRandn preserves ordinary randomized host selects. Inside an
// isolate, SplitMix64 supplies a fixed, architecture-independent sequence.
// The sequence is not a security primitive. Dispatch serializes consumers;
// the atomic also permits safe runtime inspection of the sequence.
func isolateSelectRandn(gp *g, n uint32) uint32 {
	group := gp.isolateGroup
	if group == nil || !group.deterministic {
		return cheaprandn(n)
	}
	x := group.selectSeq.Add(1) * 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return uint32(x % uint64(n))
}
