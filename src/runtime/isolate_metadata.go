// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
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
	return gp.isolateMetadataOwner
}

// Descriptor names are immutable process strings. Copy their bytes before a
// metadata getter returns to its caller; this does not publish the backing graph.
func isolateCopyMetadataReturnString(owner uintptr, value string) string {
	if owner == ^uintptr(0) {
		owner = getg().isolateOwner
	}
	if owner == 0 && isolateCurrentInitialization() == nil || len(value) == 0 {
		return value
	}
	gp := getg()
	previous := gp.isolateOwner
	gp.isolateOwner = owner
	defer func() { gp.isolateOwner = previous }()
	return isolateCopyMetadataString(value)
}

// Copy string/byte defaults out of the pinned protoreflect.Value union.
// Primitive defaults contain only type metadata and scalar bits. Composite
// defaults are not legal protobuf scalar defaults and receive no privilege.
func isolateCopyMetadataProtoValue(owner uintptr, value any) any {
	if owner == ^uintptr(0) {
		owner = getg().isolateOwner
	}
	if owner == 0 && isolateCurrentInitialization() == nil {
		return value
	}
	e := efaceOf(&value)
	if e._type == nil || e._type.Kind() != abi.Struct || toRType(e._type).pkgpath() != "google.golang.org/protobuf/reflect/protoreflect" || toRType(e._type).name() != "Value" {
		throw("isolate: invalid protobuf default value")
	}
	type scalarValue struct {
		typ *abi.Type
		ptr unsafe.Pointer
		num uint64
	}
	if e._type.Size_ != unsafe.Sizeof(scalarValue{}) {
		throw("isolate: unexpected protobuf default layout")
	}
	source := *(*scalarValue)(e.data)
	if source.typ != nil {
		kind := source.typ.Kind()
		if kind == abi.Pointer || kind == abi.Interface || kind == abi.Map || kind == abi.Struct {
			isolateRejectEffect("unsupported composite protobuf default")
		}
	}
	gp := getg()
	previous := gp.isolateOwner
	gp.isolateOwner = owner
	defer func() { gp.isolateOwner = previous }()
	if source.typ != nil && source.typ.Kind() == abi.String {
		data := isolateCopyMetadataString(unsafe.String((*byte)(source.ptr), source.num))
		source.ptr = unsafe.Pointer(unsafe.StringData(data))
	} else if source.typ != nil && source.typ.Kind() == abi.Slice {
		elem := (*slicetype)(unsafe.Pointer(source.typ)).Elem
		if elem.Kind() != abi.Uint8 {
			isolateRejectEffect("unsupported protobuf default slice")
		}
		data := make([]byte, source.num)
		copy(data, unsafe.Slice((*byte)(source.ptr), source.num))
		source.ptr = unsafe.Pointer(unsafe.SliceData(data))
	}
	cloned := newobject(e._type)
	*(*scalarValue)(cloned) = source
	result := value
	efaceOf(&result).data = cloned
	return result
}

// Descriptor interfaces are opaque handles to pinned library implementations.
// Grant reference retention, never ordinary reads of their mutable lazy cells.
func isolatePublishMetadataReturn(owner uintptr, value any) any {
	if owner == 0 && isolateCurrentInitialization() == nil {
		return value
	}
	e := efaceOf(&value)
	if e._type == nil || e.data == nil {
		return value
	}
	typ := e._type
	if typ.Kind() != abi.Pointer {
		return value
	}
	if toRType((*ptrtype)(unsafe.Pointer(typ)).Elem).pkgpath() != "google.golang.org/protobuf/internal/filedesc" {
		return value
	}
	if s := spanOfHeap(uintptr(e.data)); s != nil && s.isolateAllocOwner == 0 {
		previous := getg().isolateOwner
		getg().isolateOwner = 0
		defer func() { getg().isolateOwner = previous }()
		reflectOffsLock()
		if reflectOffs.isolateTypes == nil {
			reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
		}
		if _, exists := reflectOffs.isolateTypes[e.data]; !exists {
			reflectOffs.isolateTypes[e.data] = 0
		}
		reflectOffsUnlock()
	}
	return value
}

func isolateEnterMessageInfoMetadata(p unsafe.Pointer) uintptr {
	if isolateActive() && p != nil {
		if s := spanOfHeap(uintptr(p)); s != nil && s.isolateAllocOwner != 0 {
			if s.isolateAllocOwner != getg().isolateOwner {
				isolateOwnershipViolation("isolate: foreign message info cache")
			}
			return ^uintptr(0) // Private cache initialization keeps the caller's owner.
		}
	}
	isolateCheckMetadataReceiver(p)
	return isolateEnterMetadata()
}

func isolateLeaveMessageInfoMetadata(owner uintptr) {
	if owner != ^uintptr(0) {
		isolateLeaveMetadata(owner)
	}
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
	if gp.isolateMetadataDepth == 0 {
		gp.isolateMetadataOwner = owner
	}
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
		gp.isolateMetadataOwner = 0
		isolateDiscardIfRevoked()
		isolateCopyMetadataPanic(gp, owner)
	}
}

// Called by the compiler for unsupported registry mutation, callbacks and legacy
// descriptor builders. Host implementations retain their original behavior.
func isolateRejectMetadataAPI(name string) {
	if isolateActive() {
		isolateRejectEffect("unaudited metadata operation " + name)
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
		isolateRejectEffect("unaudited metadata descriptor implementation")
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
	state := isolateCurrentInitialization()
	if (gp.isolateGroup == nil || gp.isolateMetadataDepth == 0) && state == nil {
		return
	}
	fn := findfunc(pc)
	if fn.valid() && fn.flag&abi.FuncFlagIsolateMetadataTrusted != 0 {
		return
	}
	name := funcname(fn)
	if gp.isolateGroup == nil && state != nil {
		if state.library {
			panic("isolate: unaudited frozen library callback " + name)
		}
		panic("isolate: unaudited protobuf cache callback " + name)
	}
	isolateRejectEffect("unaudited metadata callback " + name)
}

// Defer registration must preserve Go's nil-function behavior: a nil function
// panics when invoked, not when registered. Avoid loading its code pointer (or
// touching a closure at all on the ordinary host path) before saving the defer.
func isolateCheckMetadataClosure(fn unsafe.Pointer) {
	gp := getg()
	if (gp.isolateGroup == nil || gp.isolateMetadataDepth == 0) && isolateCurrentInitialization() == nil || fn == nil {
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
		if isolateCurrentInitialization() != nil && len(message) != 0 {
			clone, data := rawstring(len(message))
			copy(data, message)
			return clone
		}
		return message
	}
	if len(message) == 0 {
		return ""
	}
	origin, heap := isolateAllocOrigin(unsafe.Pointer(unsafe.StringData(message)))
	if !heap || origin == owner || (getg().isolateReadOnlyOwner != 0 && origin == getg().isolateReadOnlyOwner) {
		return message
	}
	if origin != 0 {
		isolateOwnershipViolation("isolate: metadata string belongs to another instance")
	}
	clone, data := rawstring(len(message))
	copy(data, message)
	return clone
}
