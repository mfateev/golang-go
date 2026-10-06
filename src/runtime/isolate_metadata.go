// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

// Audited services may inspect only their original caller's private data.
// Sharing this query with collection helpers keeps their logical ownership
// checks consistent with compiler heap diagnostics. It grants no writes or
// permission to retain borrowed references in process caches.
//
//go:linkname isolateMetadataBorrowOwner
func isolateMetadataBorrowOwner() uintptr {
	gp := getg()
	if gp.isolateMetadataDepth == 0 || gp.isolateGroup == nil {
		return 0
	}
	return atomic.Loaduintptr(&gp.isolateGroup.alloc.cache.isolateOwner)
}

// Audited metadata builders publish immutable types into process registries.
// This is not a general escape hatch for application state or converters.
// Enter before taking a service lock and defer Leave before deferring Unlock:
// the locks must be released before the outermost Leave discards a revoked G.
// The group and boundary remain attached, so process API restrictions and live
// accounting still apply. A service may not start application goroutines or
// call the host. Runtime housekeeping goroutines retain process ownership.
//
//go:linkname isolateEnterMetadata
func isolateEnterMetadata() uintptr {
	gp := getg()
	if gp.isolateGroup == nil && gp.isolateOwner == 0 && gp.isolateMetadataDepth == 0 {
		return 0
	}
	isolateDiscardIfRevoked()
	if gp.isolateMetadataDepth == ^uint32(0) {
		throw("isolate: metadata nesting overflow")
	}
	owner := gp.isolateOwner
	gp.isolateMetadataDepth++
	gp.isolateOwner = 0
	return owner
}

//go:linkname isolateLeaveMetadata
func isolateLeaveMetadata(owner uintptr) {
	gp := getg()
	if gp.isolateMetadataDepth == 0 {
		if owner != 0 {
			throw("isolate: metadata scope underflow")
		}
		return // Ordinary host execution did not enter a scope.
	}
	if gp.isolateOwner != 0 || gp.isolateMetadataDepth > 1 && owner != 0 {
		throw("isolate: metadata owner changed inside service")
	}
	gp.isolateOwner = owner
	gp.isolateMetadataDepth--
	if gp.isolateMetadataDepth == 0 {
		isolateDiscardIfRevoked()
		isolateCopyMetadataPanic(gp, owner)
	}
}

// Called by the compiler for unsupported registry mutation, callbacks and legacy
// descriptor builders. Host implementations retain their original behavior.
func isolateRejectMetadataAPI(name string) {
	if isolateActive() {
		panic("isolate: unaudited metadata operation " + name)
	}
}

// A shared operation must never promote a caller's mutable cache or descriptor
// to the process owner. Nil receivers retain their ordinary method semantics.
func isolateCheckMetadataReceiver(p unsafe.Pointer) {
	if !isolateActive() || p == nil {
		return
	}
	if s := spanOfHeap(uintptr(p)); s != nil {
		if s.isolateAllocOwner == 0 {
			return
		}
	} else if isGoPointerWithoutSpan(p) {
		return
	}
	isolateOwnershipViolation("isolate: metadata service requires a process-owned receiver")
}

func isolateCheckMetadataDescriptor(value any) {
	if !isolateActive() {
		return
	}
	e := efaceOf(&value)
	if e._type == nil {
		return
	}
	typ := e._type
	if typ.Kind() == abi.Pointer {
		typ = (*ptrtype)(unsafe.Pointer(typ)).Elem
	}
	if toRType(typ).pkgpath() != "google.golang.org/protobuf/internal/filedesc" {
		panic("isolate: unaudited metadata descriptor implementation")
	}
	isolateCheckMetadataReceiver(e.data)
}

// Service scopes must not propagate to user callbacks hidden behind interfaces
// or function values. The compiler inserts this at dynamic call sites in an
// isolate build; the ordinary host and application paths return immediately.
//
//go:linkname isolateCheckMetadataCall
func isolateCheckMetadataCall(pc uintptr) {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateMetadataDepth == 0 {
		return
	}
	fn := findfunc(pc)
	if fn.valid() && fn.flag&abi.FuncFlagIsolateMetadataTrusted != 0 {
		return
	}
	name := funcname(fn)
	panic("isolate: unaudited metadata callback " + name)
}

// Defer registration must preserve Go's nil-function behavior: a nil function
// panics when invoked, not when registered. Avoid loading its code pointer (or
// touching a closure at all on the ordinary host path) before saving the defer.
func isolateCheckMetadataClosure(fn unsafe.Pointer) {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateMetadataDepth == 0 || fn == nil {
		return
	}
	isolateCheckHeapAccess(fn, unsafe.Sizeof(uintptr(0)), false)
	isolateCheckMetadataCall(*(*uintptr)(fn))
}

// A string panic created under the service owner must not publish either its
// interface box or backing bytes to the recovering application. Clone at the
// outermost exit, after lock cleanup and restoring the caller's allocator.
// Named strings retain their exact dynamic type. Other payload types still
// need their own transfer policy; arbitrary process objects are not immutable.
func isolateCopyMetadataPanic(gp *g, owner uintptr) {
	if owner == 0 || gp._panic == nil || gp._panic.recovered {
		return
	}
	arg := efaceOf(&gp._panic.arg)
	if arg._type == nil || arg._type.Kind() != abi.String {
		return
	}
	message := *(*string)(arg.data)
	boxOwner, boxHeap := isolateAllocOrigin(arg.data)
	textOwner, textHeap := isolateAllocOrigin(unsafe.Pointer(unsafe.StringData(message)))
	if boxHeap && boxOwner != 0 && boxOwner != owner || textHeap && textOwner != 0 && textOwner != owner {
		return // Never absorb another instance's allocation into the caller.
	}
	if (!boxHeap || boxOwner == owner) && (!textHeap || textOwner == owner) {
		return
	}
	// This trusted transfer copies directly in runtime code. It must not
	// grant a library-wide exemption for reading arbitrary process objects.
	clone, data := rawstring(len(message))
	copy(data, message)
	box := newobject(arg._type)
	*(*string)(box) = clone
	arg.data = box
}

// Reflection exposes names and tags as ordinary Go strings. Copy process-heap
// backing storage into an instance without extending immutable sharing to a
// type's entire metadata graph. Static strings and the caller's own strings
// retain their storage. Builders and ordinary host callers remain owner zero.
//
//go:linkname isolateCopyMetadataString
func isolateCopyMetadataString(message string) string {
	owner := getg().isolateOwner
	if owner == 0 {
		return message
	}
	if len(message) == 0 {
		return ""
	}
	origin, heap := isolateAllocOrigin(unsafe.Pointer(unsafe.StringData(message)))
	if !heap || origin == owner {
		return message
	}
	if origin != 0 {
		isolateOwnershipViolation("isolate: metadata string belongs to another instance")
	}
	clone, data := rawstring(len(message))
	copy(data, message)
	return clone
}
