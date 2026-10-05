// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/maps"
	"unsafe"
)

// isolateCheckHeapAccess is the initial compiler diagnostic for ordinary heap
// loads, stores and typed moves. It deliberately grants no immutable-heap
// exemption to owner zero. Static/stack memory, pointer publication, runtime
// collection operations and trusted transport need their separate policies;
// this diagnostic is not yet enabled by normal isolate builds.
func isolateCheckHeapAccess(p unsafe.Pointer, size uintptr, write bool) {
	if p == nil || size == 0 {
		return // Preserve the original operation's nil/bounds semantics.
	}
	gp := getg()
	owner := gp.isolateOwner
	var borrowed uintptr
	if gp.isolateMetadataDepth != 0 && !write && gp.isolateGroup != nil {
		// Builders may inspect their caller's private arguments. They may not
		// mutate them or read another instance's data under service privileges.
		borrowed = atomic.Loaduintptr(&gp.isolateGroup.alloc.cache.isolateOwner)
	}
	end := uintptr(p) + size - 1
	if end < uintptr(p) {
		panic("isolate: heap access range overflow")
	}
	for addr := uintptr(p); ; {
		s := spanOfHeap(addr)
		if s == nil {
			return // Static and stack checks are a separate compiler gate.
		}
		if s.isolateAllocOwner != owner && (borrowed == 0 || s.isolateAllocOwner != borrowed) {
			if write {
				panic("isolate: write to foreign heap")
			}
			panic("isolate: read from foreign heap")
		}
		// Ordinary Go objects occupy one span. Continue across heap spans for
		// larger ranges; non-heap addresses have their separate policy above.
		if end < s.limit {
			return
		}
		addr = s.base() + s.npages*pageSize
		if addr > end {
			return
		}
	}
}

// Validate every select operand before selectgo takes channel locks or queues
// a sender/receiver. A foreign channel in an unchosen case is still invalid.
func isolateCheckHeapSelect(cases unsafe.Pointer, count int) {
	for _, cas := range unsafe.Slice((*scase)(cases), count) {
		isolateCheckHeapAccess(unsafe.Pointer(cas.c), 1, true)
	}
}

func isolateCheckHeapMap(p unsafe.Pointer, write bool) {
	if p == nil {
		return
	}
	m := (*maps.Map)(p)
	gp := getg()
	if m.IsolateOwner() == gp.isolateOwner {
		return
	}
	if !write && gp.isolateMetadataDepth != 0 && gp.isolateGroup != nil &&
		m.IsolateOwner() == atomic.Loaduintptr(&gp.isolateGroup.alloc.cache.isolateOwner) {
		return
	}
	if write {
		panic("isolate: map write crosses owner boundary")
	}
	panic("isolate: map read crosses owner boundary")
}

// The level-two diagnostic validates stored references as well as the memory
// accessed by the store. A builder's read-only borrowing does not authorize
// retaining the borrowed object in a process cache.
func isolateCheckHeapReference(dst, value unsafe.Pointer) {
	if value == nil {
		return
	}
	source := spanOfHeap(uintptr(value))
	if source == nil {
		return // Static references still need the manifest's separate policy.
	}
	owner := getg().isolateOwner // Ordinary stack slots belong to their caller.
	if target := spanOfHeap(uintptr(dst)); target != nil {
		owner = target.isolateAllocOwner
	} else if isGoPointerWithoutSpan(dst) {
		owner = 0 // A linker-allocated global is process state.
	}
	if source.isolateAllocOwner != owner {
		panic("isolate: foreign heap reference publication")
	}
}

func isolateCheckHeapMove(typ *abi.Type, dst, src unsafe.Pointer) {
	if typ.PtrBytes == 0 || dst == nil || src == nil {
		return
	}
	mask := getGCMask(typ)
	for word := uintptr(0); word < typ.PtrBytes/goarch.PtrSize; word++ {
		if *addb(mask, word/8)&(1<<(word%8)) != 0 {
			offset := word * goarch.PtrSize
			isolateCheckHeapReference(add(dst, offset), *(*unsafe.Pointer)(add(src, offset)))
		}
	}
}
