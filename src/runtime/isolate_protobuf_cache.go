// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/atomic"
	"unsafe"
)

// Only the pinned MessageInfo initializer can collect allocations. Publication
// follows its reachable immutable cache fields, not arbitrary process graphs.
// Requested extents keep array padding and neighboring tiny objects private.
type isolateProtoAllocation struct {
	p    unsafe.Pointer
	size uintptr
	typ  *abi.Type
	next *isolateProtoAllocation
}

type isolateProtoInitialization struct {
	info        any
	allocations *isolateProtoAllocation
	parent      *isolateProtoInitialization
	entry       *isolateInitializationEntry
	library     bool // Audited GOROOT snapshot, using the same allocation recorder.
}

type isolateProtoSnapshot struct {
	parts map[unsafe.Pointer][]isolateTypePart
	infos map[*abi.Type]any
}

var isolateProtoPublished atomic.Pointer[isolateProtoSnapshot]

func isolateBeginProtoInitialization(info any) unsafe.Pointer {
	if !isolateCanonicalMessageInfo(info) {
		return nil // Ordinary host custom/legacy metadata keeps its behavior.
	}
	return unsafe.Pointer(isolateBeginInitialization(info, false))
}

func isolateEndProtoInitialization(token unsafe.Pointer) {
	if token != nil {
		isolateEndInitialization((*isolateProtoInitialization)(token))
	}
}

func isolateRecordProtoAllocation(p unsafe.Pointer, size uintptr, typ *abi.Type) {
	gp := getg()
	state := isolateCurrentInitialization()
	if state == nil || gp.isolateOwner != 0 {
		return
	}
	state.entry.state = nil // Do not collect the collector itself.
	record := &isolateProtoAllocation{p: p, size: size, typ: typ, next: state.allocations}
	state.allocations = record
	state.entry.state = state
}

func isolateProtoCacheBuilding() bool {
	state := isolateCurrentInitialization()
	return state != nil && !state.library
}

// Called immediately before initDone's release store, while initMu is held.
// Publication uses a copy of the registry so subsequent reads require no lock.
func isolatePublishProtoCache(info any) {
	state := isolateCurrentInitialization()
	if state == nil || efaceOf(&state.info).data != efaceOf(&info).data {
		return
	}
	state.entry.state = nil
	defer func() { state.entry.state = state }()
	e := efaceOf(&info)
	typ := (*ptrtype)(unsafe.Pointer(e._type)).Elem
	var goType *abi.Type
	allocations := make(map[unsafe.Pointer][]*isolateProtoAllocation)
	for a := state.allocations; a != nil; a = a.next {
		base := unsafe.Pointer(isolateTypeObjectBase(a.p))
		allocations[base] = append(allocations[base], a)
	}
	parts := make(map[unsafe.Pointer][]isolateTypePart)
	visited := make(map[unsafe.Pointer]bool)
	var scan func(unsafe.Pointer, uintptr, *abi.Type)
	var visit func(unsafe.Pointer, *abi.Type)
	addPart := func(p unsafe.Pointer, size uintptr) {
		if size != 0 {
			base := unsafe.Pointer(isolateTypeObjectBase(p))
			parts[base] = append(parts[base], isolateTypePart{p, size})
		}
	}
	visit = func(p unsafe.Pointer, parentType *abi.Type) {
		if p == nil || visited[p] {
			return
		}
		visited[p] = true
		if s := spanOfHeap(uintptr(p)); s == nil {
			return // Code, immutable linker data and checked metadata globals.
		} else if s.isolateAllocOwner != 0 {
			isolateOwnershipViolation("isolate: shared protobuf cache retains private memory")
		}
		// Descriptors and reflection types remain opaque/exact metadata handles.
		// In particular, their other mutable lazy cells are not frozen here.
		if isolateReadOnlyTypeRoot(p) || isolateReadOnlyTypeRange(p, 1) || isolateReadOnlyProtoRange(p, 1) {
			return
		}
		for _, a := range allocations[unsafe.Pointer(isolateTypeObjectBase(p))] {
			if uintptr(p) >= uintptr(a.p) && uintptr(p)-uintptr(a.p) < a.size {
				if !visited[a.p] || a.p == p {
					visited[a.p] = true
					addPart(a.p, a.size)
					scan(a.p, a.size, a.typ)
				}
				return
			}
		}
		isolateOwnershipViolation("isolate: shared protobuf cache references unaudited process memory in " + toRType(parentType).string())
	}
	scan = func(p unsafe.Pointer, size uintptr, t *abi.Type) {
		if t == nil || t.PtrBytes == 0 {
			return
		}
		mask := getGCMask(t)
		for start := uintptr(0); start < size; start += t.Size_ {
			for word := uintptr(0); word < t.PtrBytes/goarch.PtrSize; word++ {
				offset := start + word*goarch.PtrSize
				if offset+goarch.PtrSize <= size && *addb(mask, word/8)&(1<<(word%8)) != 0 {
					visit(*(*unsafe.Pointer)(add(p, offset)), t)
				}
			}
		}
	}
	for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
		p := add(e.data, field.Offset)
		switch field.Name.Name() {
		case "GoReflectType":
			pointerType := (*abi.Type)((*iface)(p).data)
			goType = (*ptrtype)(unsafe.Pointer(pointerType)).Elem
			addPart(p, field.Typ.Size_)
		case "Desc", "Exporter", "OneofWrappers":
			addPart(p, field.Typ.Size_)
		case "reflectMessageInfo":
			for _, inner := range (*structtype)(unsafe.Pointer(field.Typ)).Fields {
				if inner.Name.Name() != "nilMessage" {
					part := add(p, inner.Offset)
					addPart(part, inner.Typ.Size_)
					scan(part, inner.Typ.Size_, inner.Typ)
				}
			}
		case "coderMessageInfo":
			addPart(p, field.Typ.Size_)
			scan(p, field.Typ.Size_, field.Typ)
		}
	}
	reflectOffsLock()
	previous := isolateProtoPublished.Load()
	next := &isolateProtoSnapshot{parts: make(map[unsafe.Pointer][]isolateTypePart), infos: make(map[*abi.Type]any)}
	if previous != nil {
		for p, ranges := range previous.parts {
			next.parts[p] = ranges
		}
		for t, info := range previous.infos {
			next.infos[t] = info
		}
	}
	for p, ranges := range parts {
		copyRanges := make([]isolateTypePart, 0, len(next.parts[p])+len(ranges))
		copyRanges = append(copyRanges, next.parts[p]...)
		next.parts[p] = append(copyRanges, ranges...)
	}
	if goType != nil {
		next.infos[goType] = info
	}
	if raceenabled {
		racerelease(unsafe.Pointer(next))
	}
	isolateProtoPublished.Store(next)
	reflectOffsUnlock()
}

func isolateLoadProtoSnapshot() *isolateProtoSnapshot {
	snapshot := isolateProtoPublished.Load()
	if raceenabled && snapshot != nil {
		raceacquire(unsafe.Pointer(snapshot))
	}
	return snapshot
}

func isolateReadOnlyProtoRange(p unsafe.Pointer, size uintptr) bool {
	snapshot := isolateLoadProtoSnapshot()
	if snapshot == nil {
		return false
	}
	base := isolateTypeObjectBase(p)
	for _, part := range snapshot.parts[unsafe.Pointer(base)] {
		if uintptr(p) >= uintptr(part.start) {
			offset := uintptr(p) - uintptr(part.start)
			if offset < part.size && size <= part.size-offset {
				return true
			}
		}
	}
	return false
}
