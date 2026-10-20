// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/byteorder"
	"internal/chacha8rand"
	"internal/runtime/sys"
	"unsafe"
)

// This stream is deterministic, not secret: its seed is replay input. It is
// shared by crypto/rand, top-level math/rand APIs and select polling. Map hash
// and scheduler entropy remain internal, unobservable implementation details.
// FIFO dispatch serializes readers, including readers in child goroutines.
type isolateRandomState struct {
	state chacha8rand.State
	buf   [8]byte
	left  int
}

//go:linkname isolateConfigureRandom runtime.isolateConfigureRandom
func isolateConfigureRandom(p unsafe.Pointer, seed [32]byte) bool {
	group := (*isolateRevocationGroup)(p)
	if isolateActive() || !group.deterministic || group.live.Load() != 0 || group.random != nil {
		return false
	}
	group.randomSeed = seed
	return true
}

//go:linkname isolateCryptoRandRead
func isolateCryptoRandRead(b []byte) bool {
	gp := getg()
	group := gp.isolateGroup
	if group == nil || !group.deterministic || gp.isolateMetadataDepth != 0 {
		return false
	}
	if len(b) == 0 {
		return true // Empty reads neither initialize nor advance the stream.
	}
	isolateCheckHeapAccess(unsafe.Pointer(unsafe.SliceData(b)), uintptr(len(b)), true)
	if raceenabled {
		racewriterangepc(unsafe.Pointer(unsafe.SliceData(b)), uintptr(len(b)), sys.GetCallerPC(), abi.FuncPCABIInternal(isolateCryptoRandRead))
	}
	r := isolateRandomGenerator(gp)
	r.read(b)
	return true
}

func isolateRandomGenerator(gp *g) *isolateRandomState {
	p := &gp.isolateGroup.random
	seed := gp.isolateGroup.randomSeed
	if gp.isolateReadOnlyService {
		// Read-only handlers get their own scratch generator. They cannot change
		// workflow randomness. The domain byte distinguishes their stream.
		p = &gp.isolateReadOnlyRandom
		seed[0] ^= 0x80
	}
	if *p == nil {
		r := new(isolateRandomState)
		r.state.Init(seed)
		*p = r
	}
	return *p
}

func (r *isolateRandomState) read(b []byte) {
	if r.left > 0 {
		n := copy(b, r.buf[len(r.buf)-r.left:])
		r.left -= n
		b = b[n:]
	}
	for len(b) >= 8 {
		byteorder.LEPutUint64(b, r.next())
		b = b[8:]
	}
	if len(b) > 0 {
		byteorder.LEPutUint64(r.buf[:], r.next())
		copy(b, r.buf[:])
		r.left = 8 - len(b)
	}
}

// Integer draws consume the same bytes as crypto/rand, including any remainder
// from a preceding partial read. This makes one stream observable across APIs.
func isolateRandomUint64(gp *g) uint64 {
	var b [8]byte
	isolateRandomGenerator(gp).read(b[:])
	return byteorder.LEUint64(b[:])
}

func (r *isolateRandomState) next() uint64 {
	for {
		if value, ok := r.state.Next(); ok {
			return value
		}
		r.state.Refill()
	}
}
