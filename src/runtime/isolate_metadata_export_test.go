// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

func IsolateMetadataGroupForTest() unsafe.Pointer          { return isolateNewGroup() }
func IsolateMetadataRevokeForTest(p unsafe.Pointer)        { isolateRevokeUnstarted(p) }
func IsolateMetadataLiveForTest(p unsafe.Pointer) int32    { return isolateGroupLive(p) }
func IsolateMetadataRunningForTest(p unsafe.Pointer) int32 { return isolateGroupRunning(p) }

func IsolateMetadataOwnerForTest() uintptr   { return isolateGetOwner() }
func IsolateMetadataDepthForTest() uint32    { return getg().isolateMetadataDepth }
func IsolateMetadataGCWorkersForTest() int32 { return gcBgMarkWorkerCount }
func IsolateMetadataScopeForTest(fn func()) {
	owner := isolateEnterMetadata()
	defer isolateLeaveMetadata(owner)
	fn()
}

func IsolateMetadataRunForTest(p unsafe.Pointer, owner uintptr, fn func()) {
	group := isolateSetGroup(p)
	defer isolateSetGroup(group)
	previous := isolateSetOwner(owner)
	defer isolateSetOwner(previous)
	fn()
}

func IsolateReadOnlyRunForTest(p unsafe.Pointer, owner, scratch uintptr, fn func()) {
	isolateEnableDeterminism(p)
	isolatePrepareReadOnly(p, scratch)
	IsolateMetadataRunForTest(p, owner, func() {
		gp := getg()
		defer func() {
			gp.isolateReadOnlyOwner = 0
			gp.isolateReadOnlyService = false
		}()
		isolateMarkReadOnlyService()
		isolateBeginReadOnly()
		fn()
	})
}

func IsolateAllocOriginForTest(p unsafe.Pointer) (uintptr, bool) {
	return isolateAllocOrigin(p)
}

func IsolateHeapAccessForTest(p unsafe.Pointer, size uintptr, write bool) {
	isolateCheckHeapAccess(p, size, write)
}

func IsolateHeapReferenceForTest(dst, value unsafe.Pointer) {
	isolateCheckHeapReference(dst, value)
}

func IsolateHeapMapForTest(value any, write bool) {
	e := efaceOf(&value)
	if e._type.Kind() != abi.Map {
		panic("invalid heap map test argument")
	}
	isolateCheckHeapMap(e.data, write)
}

// Build a recorded process graph without importing compiler-generated E4
// descriptors. The production entry accepts only the audited GOROOT packages.
func IsolateFrozenLibraryForTest(build func() any) (value any) {
	state := isolateBeginInitialization(nil, true)
	defer func() {
		state.entry.state = state
		isolateEndInitialization(state)
	}()
	value = build()
	state.entry.state = nil
	next := &isolateLibrarySnapshot{states: make(map[unsafe.Pointer]isolateFrozenLibrary), parts: make(map[unsafe.Pointer][]isolateTypePart)}
	if previous := isolateLoadLibraries(); previous != nil {
		for key, entry := range previous.states {
			next.states[key] = entry
		}
		for base, parts := range previous.parts {
			next.parts[base] = append([]isolateTypePart(nil), parts...)
		}
	}
	e := efaceOf(&value)
	isolatePublishLibraryGraph(next, []unsafe.Pointer{e.data}, state.allocations)
	if raceenabled {
		racerelease(unsafe.Pointer(next))
	}
	isolateLibraries.Store(next)
	return value
}

// Test-only provenance fixtures exercise array bounds and excluded mutable
// cells without importing the external protobuf module into runtime tests.
func IsolateMessageInfoLayoutForTest(p unsafe.Pointer, stride, readonly, count uintptr, roots []bool) {
	reflectOffsLock()
	defer reflectOffsUnlock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	if reflectOffs.isolateMessageInfoArrays == nil {
		reflectOffs.isolateMessageInfoArrays = make(map[unsafe.Pointer]isolateMessageInfoLayout)
	}
	reflectOffs.isolateMessageInfoArrays[p] = isolateMessageInfoLayout{stride, readonly, count}
	for i, allowed := range roots {
		if allowed {
			reflectOffs.isolateTypes[add(p, uintptr(i)*stride)] = readonly
		}
	}
}

func IsolateHeapMoveForTest(dst, src any) {
	d, s := efaceOf(&dst), efaceOf(&src)
	if d._type != s._type || d._type.Kind() != abi.Pointer {
		panic("invalid heap move test arguments")
	}
	isolateCheckHeapMove((*ptrtype)(unsafe.Pointer(d._type)).Elem, d.data, s.data)
}

func IsolateHeapMapKeyForTest(dst, key any, publish bool) {
	d, k := efaceOf(&dst), efaceOf(&key)
	if d._type.Kind() != abi.Map || k._type.Kind() != abi.Pointer {
		panic("invalid heap map key test arguments")
	}
	typ := (*abi.MapType)(unsafe.Pointer(d._type))
	if (*ptrtype)(unsafe.Pointer(k._type)).Elem != typ.Key {
		panic("incorrect heap map key test type")
	}
	isolateCheckHeapMap(d.data, publish)
	isolateCheckHeapMapKey(typ, d.data, k.data, publish)
}

//go:noinline
func IsolateMetadataBytesForTest(size int) []byte { return make([]byte, size) }

//go:noinline
func IsolatePointerSliceForTest(count int) []*int { return make([]*int, count) }

func IsolateCachePointerForTest() unsafe.Pointer {
	return unsafe.Pointer(getg().isolateGroup.alloc.cache)
}

func IsolateCachePresentForTest(owner uintptr) bool {
	found := false
	systemstack(func() {
		lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
		for c := isolateAllocRegistry.head; c != nil; c = c.isolateNext {
			if atomic.Loaduintptr(&c.isolateOwner) == owner {
				found = true
				break
			}
		}
		unlock(&isolateAllocRegistry.lock)
	})
	return found
}

func IsolateMetadataSetExitForTest(group unsafe.Pointer, fn func(int)) {
	isolateSetGroupExit(group, fn)
}

func IsolateMetadataDeterministicForTest(group unsafe.Pointer) bool {
	return isolateEnableDeterminism(group)
}
func IsolateMetadataSuspendForTest(group unsafe.Pointer) { isolateSuspend(group) }
func IsolateMetadataSuspendWaitingForTest(p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	var waiting bool
	systemstack(func() {
		lock(&group.dispatchLock)
		waiting = group.dispatchWaiter != 0
		unlock(&group.dispatchLock)
	})
	return waiting
}

// Model both Darwin preemption callers even on non-Darwin test machines.
func ExecPreemptionLockOrderForTest() {
	systemstack(func() {
		lock(&sched.lock)
		lock(&allpLock)
		execLock.rlock()
		execLock.runlock()
		unlock(&allpLock)
		unlock(&sched.lock)
	})
}

func IsolateHeapSliceCopyForTest(dst, src any, publish bool) {
	d, s := efaceOf(&dst), efaceOf(&src)
	if d._type != s._type || d._type.Kind() != abi.Slice {
		panic("invalid heap slice copy test arguments")
	}
	target, source := (*slice)(d.data), (*slice)(s.data)
	isolateCheckHeapSliceCopy((*slicetype)(unsafe.Pointer(d._type)).Elem, target.array, target.len, source.array, source.len, publish)
}

func IsolateHeapNewSliceCopyForTest(src any, length int, publish bool) {
	s := efaceOf(&src)
	if s._type.Kind() != abi.Slice {
		panic("invalid heap new slice copy test argument")
	}
	source := (*slice)(s.data)
	isolateCheckHeapSliceCopy((*slicetype)(unsafe.Pointer(s._type)).Elem, nil, length, source.array, source.len, publish)
}

func IsolateCurrentGForTest() unsafe.Pointer { return unsafe.Pointer(getg()) }
func IsolateStackAccessForTest(other unsafe.Pointer, crossEnd bool) {
	gp := getg()
	if other != nil {
		gp = (*g)(other)
	}
	addr, size := gp.stack.hi-1, uintptr(1)
	if crossEnd {
		size = 2
	}
	isolateCheckHeapAccess(unsafe.Pointer(addr), size, false)
}
func IsolateStackPublicationForTest(dst unsafe.Pointer) {
	isolateCheckHeapReference(dst, unsafe.Pointer(getg().stack.hi-1))
}

func IsolateBoundaryBytesForTest(src []byte) []byte  { return isolateCopyBoundaryBytes(src) }
func IsolateBoundaryStringForTest(src string) string { return isolateCopyBoundaryString(src) }
func IsolateItabTableForTest() (uintptr, uintptr) {
	lock(&itabLock)
	defer unlock(&itabLock)
	size := itabTable.size
	owner, _ := isolateAllocOrigin(unsafe.Pointer(itabTable))
	return size, owner
}

func IsolateInterfaceCacheOwnersForTest() (uintptr, uintptr) {
	var value any = new(any)
	typ := efaceOf(&value)._type
	assertion := buildTypeAssertCache(&emptyTypeAssertCache, typ, nil)
	switching := buildInterfaceSwitchCache(&emptyInterfaceSwitchCache, typ, 0, nil)
	a, _ := isolateAllocOrigin(unsafe.Pointer(assertion))
	b, _ := isolateAllocOrigin(unsafe.Pointer(switching))
	return a, b
}

func IsolateItabBoundsForTest(value interface{ Marker() }) (unsafe.Pointer, uintptr) {
	iface := *(*iface)(unsafe.Pointer(&value))
	return unsafe.Pointer(iface.tab), unsafe.Sizeof(itab{})
}

func IsolateRunningPanicDefersForTest() uint32 { return runningPanicDefers.Load() }

func IsolatePrivateProtoScopeForTest(p unsafe.Pointer, fn func()) {
	owner := isolateEnterMessageInfoMetadata(p)
	defer isolateLeaveMessageInfoMetadata(owner)
	fn()
}

func IsolateProtoCacheForTest(info any, build func()) {
	state := isolateBeginInitialization(info, false)
	defer isolateEndInitialization(state)
	build()
	isolatePublishProtoCache(info)
}

func IsolateInitializationScopeForTest(fn func()) {
	state := isolateBeginInitialization(nil, false)
	defer isolateEndInitialization(state)
	fn()
}

func IsolateInitializationPauseForTest(fn func()) {
	state := isolateCurrentInitialization()
	state.entry.state = nil
	defer func() { state.entry.state = state }()
	fn()
}

func IsolateInitializationActiveForTest() bool {
	return isolateInitializationForG(getg()) != nil
}

func IsolateInitializationRecordedForTest(p unsafe.Pointer) bool {
	state := isolateCurrentInitialization()
	if state != nil {
		for a := state.allocations; a != nil; a = a.next {
			if uintptr(p) >= uintptr(a.p) && uintptr(p)-uintptr(a.p) < a.size {
				return true
			}
		}
	}
	return false
}

func IsolateInitializationEntriesForTest() int {
	n := 0
	for entry := isolateInitializations.head.Load(); entry != nil; entry = entry.next.Load() {
		n++
	}
	return n
}

func IsolateMetadataCallbackForTest(pc uintptr) { isolateCheckMetadataCall(pc) }
func IsolateMetadataReturnStringForTest(owner uintptr, value string) string {
	return isolateCopyMetadataReturnString(owner, value)
}
