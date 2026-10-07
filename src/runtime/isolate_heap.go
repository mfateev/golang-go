// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goarch"
	"internal/runtime/maps"
	"internal/stringslite"
	"unsafe"
)

// isolateCheckHeapAccess validates complete ranges, including current-stack and
// linker data. Only linker read-only sections and registered canonical metadata
// may be shared. A service may borrow its caller's data without retaining it.
//
//go:linkname isolateCheckHeapAccess
func isolateCheckHeapAccess(p unsafe.Pointer, size uintptr, write bool) {
	if p == nil || size == 0 {
		return // Preserve the original operation's nil/bounds semantics.
	}
	gp := getg()
	owner := gp.isolateOwner
	var borrowed uintptr
	if !write {
		borrowed = isolateMetadataBorrowOwner()
	}
	end := uintptr(p) + size - 1
	if end < uintptr(p) {
		isolateOwnershipViolation("isolate: heap access range overflow")
	}
	// Go pointers to another goroutine's locals escape to the heap. The only
	// supported stack storage is this goroutine's complete current stack.
	if uintptr(p) >= gp.stack.lo && end < gp.stack.hi {
		return
	}
	for addr := uintptr(p); ; {
		s := spanOfHeap(addr)
		if s == nil {
			isolateCheckNonHeapAccess(addr, end, write)
			return
		}
		if s.isolateAllocOwner != owner && (borrowed == 0 || s.isolateAllocOwner != borrowed) {
			if !write && s.isolateAllocOwner == 0 && isolateReadOnlyTypeRange(p, size) {
				return
			}
			if write {
				isolateOwnershipViolation("isolate: write to foreign heap")
			}
			isolateOwnershipViolation("isolate: read from foreign heap")
		}
		if end < s.limit {
			return
		}
		addr = s.base() + s.npages*pageSize
		if addr > end {
			return
		}
	}
}

// The compiler supplies the exact declared extent of an approved metadata
// cell or constant table. Bounds remain checked for dynamically indexed tables;
// no neighboring global, write, or reference publication gains permission.
func isolateCheckHeapGlobalRead(p unsafe.Pointer, size uintptr, root unsafe.Pointer, extent uintptr) {
	addr, start := uintptr(p), uintptr(root)
	if size == 0 {
		return
	}
	if addr < start || size > extent || addr-start > extent-size {
		isolateOwnershipViolation("isolate: read exceeds approved global range")
	}
}

// Exact bounds come from every linked module, including ordinary host plugins.
// The first-module-only approximation would misclassify their immutable types,
// strings and generic dictionaries. Read-only permits reads, never mutation.
func isolateStaticRange(addr, end uintptr) (known, readonly bool) {
	if addr >= uintptr(unsafe.Pointer(&zeroVal[0])) && end < uintptr(unsafe.Pointer(&zeroVal[0]))+unsafe.Sizeof(zeroVal) {
		return true, true
	}
	if addr == uintptr(unsafe.Pointer(&zerobase)) && end == addr {
		return true, true
	}
	for md := &firstmoduledata; md != nil; md = md.next {
		if addr >= uintptr(unsafe.Pointer(md.pcHeader)) && end < md.epclntab ||
			addr >= md.rodata && end < md.erodata ||
			addr >= md.types && end < md.etypes ||
			addr >= md.relrodata && end < md.erelrodata ||
			addr >= md.funcdesc && end < md.efuncdesc {
			return true, true
		}
		for _, bounds := range [...][2]uintptr{
			{md.noptrdata, md.enoptrdata}, {md.data, md.edata},
			{md.bss, md.ebss}, {md.noptrbss, md.enoptrbss},
			{md.covctrs, md.ecovctrs},
		} {
			if addr >= bounds[0] && end < bounds[1] {
				return true, false
			}
		}
	}
	return false, false
}

func isolateCheckNonHeapAccess(addr, end uintptr, write bool) {
	if isolateReadOnlyItabRange(addr, end) {
		if write {
			isolateOwnershipViolation("isolate: write to read-only memory")
		}
		return
	}
	if known, readonly := isolateStaticRange(addr, end); known {
		if readonly {
			if write {
				isolateOwnershipViolation("isolate: write to read-only memory")
			}
			return
		}
		if getg().isolateOwner == 0 {
			return // Process globals, including audited service state.
		}
		isolateOwnershipViolation("isolate: access to process global")
	}
	// Manual spans contain runtime stacks and runtime-managed storage, not
	// application objects. No private goroutine may borrow another stack.
	if s := spanOf(addr); s != nil && s.state.get() == mSpanManual {
		isolateOwnershipViolation("isolate: access to foreign stack or runtime memory")
	}
	if getg().isolateGroup != nil || getg().isolateOwner != 0 {
		isolateOwnershipViolation("isolate: access to unclassified memory")
	}
	// Preserve ordinary host access to non-Go memory (C, mmap, device buffers).
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
	if !write {
		if borrowed := isolateMetadataBorrowOwner(); borrowed != 0 && m.IsolateOwner() == borrowed {
			return
		}
	}
	if write {
		isolateOwnershipViolation("isolate: map write crosses owner boundary")
	}
	isolateOwnershipViolation("isolate: map read crosses owner boundary")
}

// Generic map helpers read a typed key and copy its references into map-owned
// storage during assignment. The actual slots may be indirect or live in a
// different span from the header, so use the map's owner for every reference;
// never pretend that its slots start at dst + the key field's offset.
func isolateCheckHeapMapKey(typ *abi.MapType, dst, key unsafe.Pointer, publish bool) {
	// Hashing/equality can follow strings and nested interface boxes through
	// uninstrumented type algorithms, just like ordinary interface equality.
	isolateCheckHeapComparable(typ.Key, key)
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
	isolateCheckHeapReferenceTo(dst, value, false)
}

// A compiler-known stack destination must not be exposed to a safepoint
// before its aggregate initialization is complete. This validates the value
// with the same transient-stack borrowing and lifetime rules.
func isolateCheckHeapStackReference(value unsafe.Pointer) {
	isolateCheckHeapReferenceTo(nil, value, true)
}

func isolateCheckHeapReferenceTo(dst, value unsafe.Pointer, stack bool) {
	if value == nil {
		return
	}
	gp := getg()
	owner := gp.isolateOwner
	stack = stack || dst != nil && uintptr(dst) >= gp.stack.lo && uintptr(dst) < gp.stack.hi
	if target := spanOfHeap(uintptr(dst)); target != nil {
		owner = target.isolateAllocOwner
	} else if stack {
		// A service may hold both process metadata and its borrowed caller
		// references in transient stack locals. This does not authorize a
		// publication into process caches or an instance heap.
		if gp.isolateMetadataDepth != 0 {
			if source := spanOfHeap(uintptr(value)); source != nil &&
				(source.isolateAllocOwner == 0 || source.isolateAllocOwner == isolateMetadataBorrowOwner()) {
				return
			}
		}
	} else if dst != nil {
		if known, readonly := isolateStaticRange(uintptr(dst), uintptr(dst)); known {
			if readonly {
				isolateOwnershipViolation("isolate: write to read-only memory")
			}
			owner = 0
		} else {
			isolateCheckHeapAccess(dst, 1, true)
		}
	}
	source := spanOfHeap(uintptr(value))
	if source == nil {
		addr := uintptr(value)
		if addr >= gp.stack.lo && addr < gp.stack.hi {
			if stack {
				return // Stack-local references do not outlive their storage.
			}
			isolateOwnershipViolation("isolate: stack reference publication")
		}
		if known, readonly := isolateStaticRange(addr, addr); known {
			if readonly || owner == 0 {
				return
			}
			isolateOwnershipViolation("isolate: process global reference publication")
		}
		if isolateReadOnlyItabRange(addr, addr) {
			return
		}
		// reflect.Type.Method constructs a function value from its immutable
		// entry address. This permits code references, never code reads/writes.
		if fn := findfunc(addr); fn.valid() && fn.entry() == addr {
			return
		}
		isolateCheckNonHeapAccess(addr, addr, false)
		return
	}
	if source.isolateAllocOwner != owner {
		if source.isolateAllocOwner == 0 && isolateReadOnlyTypeRoot(value) {
			return
		}
		isolateOwnershipViolation("isolate: foreign heap reference publication")
	}
}

func isolateCheckHeapMove(typ *abi.Type, dst, src unsafe.Pointer) {
	if typ.PtrBytes == 0 || dst == nil || src == nil {
		return
	}
	isolateCheckHeapMoveTo(typ, dst, src, false)
}

func isolateCheckHeapStackMove(typ *abi.Type, src unsafe.Pointer) {
	if typ.PtrBytes == 0 || src == nil {
		return
	}
	isolateCheckHeapMoveTo(typ, nil, src, true)
}

func isolateCheckHeapMoveTo(typ *abi.Type, dst, src unsafe.Pointer, stack bool) {
	mask := getGCMask(typ)
	for word := uintptr(0); word < typ.PtrBytes/goarch.PtrSize; word++ {
		if *addb(mask, word/8)&(1<<(word%8)) != 0 {
			offset := word * goarch.PtrSize
			isolateCheckHeapReferenceTo(dst, *(*unsafe.Pointer)(add(src, offset)), stack)
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
		isolateOwnershipViolation("isolate: canonical type must be process-owned")
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
		ft := (*abi.FuncType)(p)
		size = unsafe.Sizeof(abi.FuncType{}) + uintptr(ft.InCount+uint16(ft.NumOut()))*goarch.PtrSize
		if typ.TFlag&abi.TFlagUncommon != 0 {
			size += unsafe.Sizeof(abi.UncommonType{})
		}
	case abi.Slice:
		size = unsafe.Sizeof(abi.SliceType{})
	case abi.Struct:
		size = unsafe.Sizeof(abi.StructType{})
	case abi.Map:
		size = unsafe.Sizeof(abi.MapType{})
	default:
		throw("isolate: unexpected constructed type")
	}
	if ut := typ.Uncommon(); ut != nil {
		methodsEnd := uintptr(unsafe.Pointer(ut)) - uintptr(p) + uintptr(ut.Moff) + uintptr(ut.Mcount)*unsafe.Sizeof(abi.Method{})
		if methodsEnd > size {
			size = methodsEnd
		}
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	reflectOffs.isolateTypes[p] = size
	reflectOffsUnlock()
	// Constructors create these exact immutable side objects under the process
	// owner. Publish only these layouts; GC masks and lazy caches stay private
	// runtime/service state. No arbitrary owner-zero graph is traversed.
	if typ.Kind() == abi.Struct {
		st := (*abi.StructType)(p)
		isolatePublishTypePart(unsafe.Pointer(unsafe.SliceData(st.Fields)), uintptr(len(st.Fields))*unsafe.Sizeof(abi.StructField{}))
		isolatePublishTypeName(st.PkgPath)
		for _, field := range st.Fields {
			isolatePublishTypeName(field.Name)
		}
	}
	if typ.Equal != nil {
		fn := *(*unsafe.Pointer)(unsafe.Pointer(&typ.Equal))
		switch typ.Kind() {
		case abi.Struct:
			isolatePublishTypeAlgorithm(fn, "reflect.StructOf.func", 2*goarch.PtrSize)
		case abi.Array:
			isolatePublishTypeAlgorithm(fn, "reflect.ArrayOf.func", 4*goarch.PtrSize)
		}
	}
	if typ.Kind() == abi.Map {
		fn := *(*unsafe.Pointer)(unsafe.Pointer(&(*abi.MapType)(p).Hasher))
		isolatePublishTypeAlgorithm(fn, "reflect.MapOf.func", 2*goarch.PtrSize)
	}
}

type isolateTypePart struct {
	start unsafe.Pointer
	size  uintptr
}

func isolatePublishTypePart(p unsafe.Pointer, size uintptr) {
	if p == nil || size == 0 {
		return
	}
	if s := spanOfHeap(uintptr(p)); s == nil {
		return
	} else if getg().isolateOwner != 0 || s.isolateAllocOwner != 0 {
		throw("isolate: private canonical type part")
	}
	base := unsafe.Pointer(isolateTypeObjectBase(p))
	reflectOffsLock()
	if reflectOffs.isolateTypeParts == nil {
		reflectOffs.isolateTypeParts = make(map[unsafe.Pointer][]isolateTypePart)
	}
	parts := reflectOffs.isolateTypeParts[base]
	for _, part := range parts {
		if part.start == p && part.size == size {
			reflectOffsUnlock()
			return
		}
	}
	reflectOffs.isolateTypeParts[base] = append(parts, isolateTypePart{p, size})
	// A tiny-packed name has an exact interior root. This approves only the
	// named object's bytes, never the rest of its shared tiny block.
	reflectOffs.isolateTypes[p] = size
	reflectOffsUnlock()
}

func isolatePublishTypeName(n abi.Name) {
	if n.Bytes == nil {
		return
	}
	i, count := n.ReadVarint(1)
	size := 1 + i + count
	flags := *n.Bytes
	if flags&2 != 0 {
		i, count = n.ReadVarint(size)
		size += i + count
	}
	if flags&4 != 0 {
		size += 4
	}
	isolatePublishTypePart(unsafe.Pointer(n.Bytes), uintptr(size))
}

func isolatePublishTypeAlgorithm(fn unsafe.Pointer, prefix string, size uintptr) {
	if fn == nil || spanOfHeap(uintptr(fn)) == nil {
		return
	}
	pc := *(*uintptr)(fn)
	if !stringslite.HasPrefix(funcname(findfunc(pc)), prefix) {
		throw("isolate: unaudited type algorithm")
	}
	isolatePublishTypePart(fn, size)
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
	messageInfos := reflectOffs.isolateMessageInfoArrays[unsafe.Pointer(base)]
	for _, part := range reflectOffs.isolateTypeParts[unsafe.Pointer(base)] {
		if uintptr(p) >= uintptr(part.start) {
			offset := uintptr(p) - uintptr(part.start)
			if offset < part.size && size <= part.size-offset {
				reflectOffsUnlock()
				return true
			}
		}
	}
	reflectOffsUnlock()
	offset := uintptr(p) - base
	if messageInfos.stride != 0 {
		if offset/messageInfos.stride >= messageInfos.count {
			return false
		}
		within := offset % messageInfos.stride
		return within < messageInfos.readonly && size <= messageInfos.readonly-within
	}
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
		isolateOwnershipViolation("isolate: heap copy range overflow")
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

// Clone copies group bytes through uninstrumented runtime moves, and duplicates
// indirect key/value allocations. Validate before any copied reference appears
// in the new group. Ordinary iteration would impose unrelated deterministic
// key restrictions, so inspect the physical slots using their ABI layout.
//
//go:linkname isolateCheckCloneGroup
func isolateCheckCloneGroup(typ *abi.MapType, dst, src unsafe.Pointer) {
	isolateCheckHeapSliceCopy(typ.Group, dst, 1, src, 1, true)
	for i := uintptr(0); i < abi.MapGroupSlots; i++ {
		if typ.IndirectKey() {
			p := *(*unsafe.Pointer)(add(src, typ.KeysOff+i*typ.KeyStride))
			if p != nil {
				isolateCheckHeapSliceCopy(typ.Key, nil, 1, p, 1, true)
			}
		}
		if typ.IndirectElem() {
			p := *(*unsafe.Pointer)(add(src, typ.ElemsOff+i*typ.ElemStride))
			if p != nil {
				isolateCheckHeapSliceCopy(typ.Elem, nil, 1, p, 1, true)
			}
		}
	}
}
