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
// loads, stores and typed moves. It grants read-only sharing only to explicitly
// registered canonical reflection descriptor roots. Static/stack memory, pointer publication, runtime
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
			if !write && s.isolateAllocOwner == 0 && isolateReadOnlyTypeRange(p, size) {
				return
			}
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

// Concatenation with more than five operands passes a slice of string headers.
// Validate the headers before inspecting them and all backing bytes before the
// concatenation helper can read, copy, or return an operand without copying it.
func isolateCheckHeapStrings(values []string) {
	isolateCheckHeapCopy(nil, len(values), unsafe.Pointer(unsafe.SliceData(values)), len(values), unsafe.Sizeof(string("")))
	for _, value := range values {
		isolateCheckHeapAccess(unsafe.Pointer(unsafe.StringData(value)), uintptr(len(value)), false)
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

// Generic map helpers read a typed key and copy its references into map-owned
// storage during assignment. The actual slots may be indirect or live in a
// different span from the header, so use the map's owner for every reference;
// never pretend that its slots start at dst + the key field's offset.
func isolateCheckHeapMapKey(typ *abi.MapType, dst, key unsafe.Pointer, publish bool) {
	isolateCheckHeapAccess(key, typ.Key.Size_, false)
	if !publish || typ.Key.PtrBytes == 0 || key == nil {
		return
	}
	mask := getGCMask(typ.Key)
	for word := uintptr(0); word < typ.Key.PtrBytes/goarch.PtrSize; word++ {
		if *addb(mask, word/8)&(1<<(word%8)) != 0 {
			value := *(*unsafe.Pointer)(add(key, word*goarch.PtrSize))
			isolateCheckHeapReference(dst, value)
		}
	}
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
		if source.isolateAllocOwner == 0 && isolateReadOnlyTypeRoot(value) {
			return
		}
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

// Only reflect's canonical type constructors call this trusted operation.
// Registering an arbitrary owner-zero object is not an application API. Type
// descriptors already have process lifetime in reflect's canonical caches;
// this registry neither retains an instance nor freezes its reachable graph.
// In particular, names, fields, closures and lazy GC masks need their separate
// provenance/accessor policies before application code can retain them.
//
//go:linkname isolatePublishType
func isolatePublishType(typ *abi.Type) {
	if typ == nil {
		return
	}
	p := unsafe.Pointer(typ)
	span := spanOfHeap(uintptr(p))
	if span == nil {
		return
	} // Linker metadata already has its static policy.
	if getg().isolateOwner != 0 || span.isolateAllocOwner != 0 {
		panic("isolate: canonical type must be process-owned")
	}
	base := isolateTypeObjectBase(p)
	if base != uintptr(p) {
		throw("isolate: canonical type is not an allocation root")
	}
	var size uintptr
	switch typ.Kind() {
	case abi.Pointer:
		size = unsafe.Sizeof(abi.PtrType{})
	case abi.Array:
		size = unsafe.Sizeof(abi.ArrayType{})
	case abi.Chan:
		size = unsafe.Sizeof(abi.ChanType{})
	case abi.Func:
		size = unsafe.Sizeof(abi.FuncType{})
	case abi.Slice:
		size = unsafe.Sizeof(abi.SliceType{})
	case abi.Struct:
		size = unsafe.Sizeof(abi.StructType{})
	case abi.Map:
		size = unsafe.Sizeof(abi.MapType{})
	default:
		throw("isolate: unexpected constructed type")
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	reflectOffs.isolateTypes[p] = size
	reflectOffsUnlock()
}

func isolateReadOnlyTypeRoot(p unsafe.Pointer) bool {
	reflectOffsLock()
	_, ok := reflectOffs.isolateTypes[p]
	reflectOffsUnlock()
	return ok
}

func isolateReadOnlyTypeRange(p unsafe.Pointer, size uintptr) bool {
	base := isolateTypeObjectBase(p)
	if base == 0 {
		return false
	}
	reflectOffsLock()
	extent := reflectOffs.isolateTypes[unsafe.Pointer(base)]
	reflectOffsUnlock()
	offset := uintptr(p) - base
	return offset < extent && size <= extent-offset
}

// findObject returns the size-class slot, including a malloc header when the
// allocation has one. Provenance is keyed by the Go data pointer, never that
// runtime header or the padding at the end of the slot.
func isolateTypeObjectBase(p unsafe.Pointer) uintptr {
	base, span, _ := findObject(uintptr(p), 0, 0)
	if span != nil && span.spanclass.sizeclass() != 0 && !span.spanclass.noscan() && !heapBitsInSpan(span.elemsize) {
		base += mallocHeaderSize
	}
	return base
}

// These checks run before compiler-lowered copy/append helpers. Validate the
// complete accessed ranges and every copied reference before any destination
// bytes change. A nil destination represents a new allocation under the caller.
func isolateCheckHeapCopy(dst unsafe.Pointer, dstLen int, src unsafe.Pointer, srcLen int, width uintptr) {
	n := dstLen
	if srcLen < n {
		n = srcLen
	}
	if n <= 0 || width == 0 {
		return
	}
	if uintptr(n) > ^uintptr(0)/width {
		panic("isolate: heap copy range overflow")
	}
	size := uintptr(n) * width
	isolateCheckHeapAccess(src, size, false)
	if dst != nil {
		isolateCheckHeapAccess(dst, size, true)
	}
}

func isolateCheckHeapSliceCopy(typ *abi.Type, dst unsafe.Pointer, dstLen int, src unsafe.Pointer, srcLen int, publish bool) {
	isolateCheckHeapCopy(dst, dstLen, src, srcLen, typ.Size_)
	n := dstLen
	if srcLen < n {
		n = srcLen
	}
	if !publish || n <= 0 || typ.PtrBytes == 0 {
		return
	}
	mask := getGCMask(typ)
	for i := 0; i < n; i++ {
		for word := uintptr(0); word < typ.PtrBytes/goarch.PtrSize; word++ {
			if *addb(mask, word/8)&(1<<(word%8)) == 0 {
				continue
			}
			offset := uintptr(i)*typ.Size_ + word*goarch.PtrSize
			target := dst
			if target != nil {
				target = add(target, offset)
			}
			isolateCheckHeapReference(target, *(*unsafe.Pointer)(add(src, offset)))
		}
	}
}
