// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

type isolateFrozenLibrary struct {
	typ  *_type
	base unsafe.Pointer
}

type isolateLibrarySnapshot struct {
	states map[unsafe.Pointer]isolateFrozenLibrary
	parts  map[unsafe.Pointer][]isolateTypePart
}

var isolateLibraries atomic.Pointer[isolateLibrarySnapshot]

//go:linkname isolatePrivateRuneTable
func isolatePrivateRuneTable(table []rune) []rune {
	if getg().isolateOwner != 0 && len(table) != 0 && isolateReadOnlyLibraryRange(unsafe.Pointer(unsafe.SliceData(table)), uintptr(len(table))*4) {
		return append([]rune(nil), table...)
	}
	return table
}

func isolateLoadLibraries() *isolateLibrarySnapshot {
	snapshot := isolateLibraries.Load()
	if raceenabled && snapshot != nil {
		raceacquire(unsafe.Pointer(snapshot))
	}
	return snapshot
}

//go:linkname isolateHasFrozenLibrary
func isolateHasFrozenLibrary(key unsafe.Pointer) bool {
	snapshot := isolateLoadLibraries()
	if snapshot == nil {
		return false
	}
	_, found := snapshot.states[key]
	return found
}

// Variable bindings remain writable and private, including exported Unicode
// variables. The referenced default maps, ranges and slice data are frozen.
func isolateNewLibraryState(key unsafe.Pointer, typ *_type) unsafe.Pointer {
	base := newobject(typ)
	if snapshot := isolateLoadLibraries(); snapshot != nil {
		if state, found := snapshot.states[key]; found {
			if state.typ != typ {
				throw("isolate: frozen library layout mismatch")
			}
			typedmemmove(typ, base, state.base)
		}
	}
	return base
}

func isolateReadOnlyLibraryRange(p unsafe.Pointer, size uintptr) bool {
	snapshot := isolateLoadLibraries()
	if snapshot == nil || p == nil || size == 0 {
		return false
	}
	base := unsafe.Pointer(isolateTypeObjectBase(p))
	for _, part := range snapshot.parts[base] {
		if uintptr(p) >= uintptr(part.start) {
			offset := uintptr(p) - uintptr(part.start)
			if offset < part.size && size <= part.size-offset {
				return true
			}
		}
	}
	return false
}

// Follow only allocations recorded while audited table initializers execute.
// Publishing an owner-zero root never grants access to unrelated process
// objects, allocation padding, lazy caches, or another isolate's data.
func isolatePublishLibraryGraph(next *isolateLibrarySnapshot, roots []unsafe.Pointer, allocations *isolateInitializationAllocation) {
	byBase := make(map[unsafe.Pointer][]*isolateInitializationAllocation)
	for allocation := allocations; allocation != nil; allocation = allocation.next {
		base := unsafe.Pointer(isolateTypeObjectBase(allocation.p))
		byBase[base] = append(byBase[base], allocation)
	}
	visited := make(map[unsafe.Pointer]bool)
	var visit func(unsafe.Pointer)
	visit = func(p unsafe.Pointer) {
		if p == nil {
			return
		}
		if spanOfHeap(uintptr(p)) == nil {
			if known, readonly := isolateStaticRange(uintptr(p), uintptr(p)); known && readonly {
				return
			}
			panic("isolate: frozen library retains mutable process global")
		}
		if isolateReadOnlyLibraryRange(p, 1) {
			return // A dependency's already published immutable graph.
		}
		base := unsafe.Pointer(isolateTypeObjectBase(p))
		for _, allocation := range byBase[base] {
			if uintptr(p) < uintptr(allocation.p) || uintptr(p)-uintptr(allocation.p) >= allocation.size {
				continue
			}
			if visited[allocation.p] {
				return
			}
			visited[allocation.p] = true
			next.parts[base] = append(next.parts[base], isolateTypePart{allocation.p, allocation.size})
			typ := allocation.typ
			if typ == nil || typ.PtrBytes == 0 {
				return
			}
			mask := getGCMask(typ)
			for start := uintptr(0); start < allocation.size; start += typ.Size_ {
				for word := uintptr(0); word < typ.PtrBytes/goarch.PtrSize; word++ {
					offset := start + word*goarch.PtrSize
					if offset+goarch.PtrSize <= allocation.size && *addb(mask, word/8)&(1<<(word%8)) != 0 {
						visit(*(*unsafe.Pointer)(add(allocation.p, offset)))
					}
				}
			}
			return
		}
		panic("isolate: frozen library retains unrecorded process memory")
	}
	for _, root := range roots {
		visit(root)
	}
}
