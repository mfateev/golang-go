// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

type isolateMessageInfoLayout struct {
	stride, readonly, count uintptr
}

func isolateSupportedProtoStruct(typ *abi.Type) bool {
	if typ.Kind() != abi.Struct {
		return false
	}
	for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
		if !field.Name.IsExported() {
			switch field.Name.Name() {
			case "state", "sizeCache", "unknownFields":
			default:
				return false // Extensions, weak fields and opaque layouts need their own value policy.
			}
		}
	}
	return true
}

func isolateCheckProtoResolver(value any) {
	if !isolateActive() {
		return
	}
	e := efaceOf(&value)
	if e._type == nil {
		return
	}
	reflectOffsLock()
	canonical := reflectOffs.isolateProtoGlobalTypes
	reflectOffsUnlock()
	if e.data != canonical {
		isolateRejectEffect("custom protobuf resolver")
	}
}

//go:linkname isolateIsGeneratedProtobuf
func isolateIsGeneratedProtobuf(value any) bool {
	typ := efaceOf(&value)._type
	if typ == nil {
		return false
	}
	for typ.Kind() == abi.Pointer {
		typ = (*ptrtype)(unsafe.Pointer(typ)).Elem
	}
	reflectOffsLock()
	valid := reflectOffs.isolateProtoInfos[typ] != nil && isolateSupportedProtoStruct(typ)
	reflectOffsUnlock()
	return valid
}

// Generated accessors resolve canonical metadata by Go type before reading
// package globals. Application globals still have ordinary private ownership.
func isolateCanonicalProtoInfo(value any) any {
	typ := efaceOf(&value)._type
	if typ == nil {
		isolateRejectEffect("missing generated protobuf type")
	}
	for typ.Kind() == abi.Pointer {
		typ = (*ptrtype)(unsafe.Pointer(typ)).Elem
	}
	reflectOffsLock()
	info := reflectOffs.isolateProtoInfos[typ]
	reflectOffsUnlock()
	if info == nil || typ.Kind() == abi.Struct && !isolateSupportedProtoStruct(typ) {
		isolateRejectEffect("unaudited generated protobuf type")
	}
	return info
}

// Copy the pinned immutable MessageInfo header into the caller's allocator.
// Reflection and binary coder caches are subsequently built under that owner.
func isolatePrivateMessageInfo(value any) any {
	gp := getg()
	if gp.isolateOwner == 0 {
		return value
	}
	e := efaceOf(&value)
	if e._type == nil || e.data == nil {
		return value
	}
	if e._type.Kind() != abi.Pointer {
		throw("isolate: invalid message info cache")
	}
	typ := (*ptrtype)(unsafe.Pointer(e._type)).Elem
	if toRType(typ).pkgpath() != "google.golang.org/protobuf/internal/impl" || toRType(typ).name() != "MessageInfo" {
		throw("isolate: unexpected message info cache type")
	}
	s := spanOfHeap(uintptr(e.data))
	if s != nil && s.isolateAllocOwner == gp.isolateOwner {
		return value
	}
	var extent uintptr
	for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
		switch field.Name.Name() {
		case "GoReflectType", "Desc", "Exporter", "OneofWrappers":
			extent = field.Offset + field.Typ.Size_
		}
	}
	if s == nil || (s.isolateAllocOwner != 0 && s.isolateAllocOwner != gp.isolateReadOnlyOwner) {
		isolateOwnershipViolation("isolate: foreign message info cache")
	}
	if s.isolateAllocOwner == 0 && !isolateReadOnlyTypeRange(e.data, extent) {
		isolateOwnershipViolation("isolate: unaudited message info cache")
	}
	clone := newobject(typ)
	if writeBarrier.enabled {
		bulkBarrierPreWrite(uintptr(clone), uintptr(e.data), extent, typ)
	}
	memmove(clone, e.data, extent)
	for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
		if field.Name.Name() == "OneofWrappers" {
			src := (*slice)(add(e.data, field.Offset))
			if src.len != 0 {
				item := (*slicetype)(unsafe.Pointer(field.Typ)).Elem
				array := mallocgc(uintptr(src.len)*item.Size_, item, true)
				if writeBarrier.enabled {
					bulkBarrierPreWrite(uintptr(array), uintptr(src.array), uintptr(src.len)*item.Size_, nil)
				}
				memmove(array, src.array, uintptr(src.len)*item.Size_)
				*(*slice)(add(clone, field.Offset)) = slice{array, src.len, src.len}
			}
		}
	}
	result := value
	efaceOf(&result).data = clone
	return result
}

// Only the pinned protoregistry initializer publishes its two process roots.
// A zero readable extent permits opaque references in marshal options, while
// registry fields remain inaccessible outside audited metadata methods.
func isolatePublishProtoRegistry(value any) {
	gp := getg()
	if gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateMetadataDepth != 0 {
		panic("isolate: canonical protobuf registries require process initialization")
	}
	e := efaceOf(&value)
	if e._type == nil || e._type.Kind() != abi.Pointer || e.data == nil {
		throw("isolate: invalid canonical protobuf registry")
	}
	typ := (*ptrtype)(unsafe.Pointer(e._type)).Elem
	if toRType(typ).pkgpath() != "google.golang.org/protobuf/reflect/protoregistry" ||
		(toRType(typ).name() != "Types" && toRType(typ).name() != "Files") {
		throw("isolate: unexpected canonical protobuf registry type")
	}
	span := spanOfHeap(uintptr(e.data))
	if span == nil || span.isolateAllocOwner != 0 || isolateTypeObjectBase(e.data) != uintptr(e.data) {
		throw("isolate: canonical protobuf registry must be a process allocation root")
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	reflectOffs.isolateTypes[e.data] = 0
	if toRType(typ).name() == "Types" {
		reflectOffs.isolateProtoGlobalTypes = e.data
	}
	reflectOffsUnlock()
}

// Only the compiler's pinned filetype.Builder.Build return hook calls this.
// The builder has populated the table and registered its non-map-entry types
// in the default process registry. Neither arbitrary owner-zero values nor
// custom/private registries receive provenance. Registration retains no guest
// data: these process tables already have lifetime in GlobalTypes.
func isolatePublishMessageInfos(values any) {
	gp := getg()
	if gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateMetadataDepth != 0 {
		panic("isolate: canonical message infos require process initialization")
	}
	e := efaceOf(&values)
	if e._type == nil || e._type.Kind() != abi.Slice {
		throw("isolate: invalid canonical message info table")
	}
	typ := (*slicetype)(unsafe.Pointer(e._type)).Elem
	if typ.Kind() != abi.Struct || toRType(typ).pkgpath() != "google.golang.org/protobuf/internal/impl" || toRType(typ).string() != "impl.MessageInfo" {
		throw("isolate: unexpected canonical message info type")
	}
	var descOffset uintptr
	var readonly uintptr
	immutableFields := 0
	found := false
	for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
		switch field.Name.Name() {
		case "GoReflectType", "Desc", "Exporter", "OneofWrappers":
			immutableFields++
			if end := field.Offset + field.Typ.Size_; end > readonly {
				readonly = end
			}
		default:
			// Private initialization cells may change in shared services.
			// Never make these observable by ordinary application loads.
			if immutableFields != 4 || field.Offset < readonly {
				throw("isolate: unexpected message info immutable prefix")
			}
		}
		if field.Name.Name() == "Desc" && field.Typ.Kind() == abi.Interface {
			descOffset, found = field.Offset, true
		}
	}
	if !found || immutableFields != 4 {
		throw("isolate: canonical message info descriptor field missing")
	}
	infos := *(*slice)(e.data)
	if infos.len == 0 {
		return
	}
	if typ.Size_ == 0 || uintptr(infos.len) > ^uintptr(0)/typ.Size_ {
		throw("isolate: canonical message info table size overflow")
	}
	span := spanOfHeap(uintptr(infos.array))
	if span == nil {
		return // Static tables retain the separate linker-memory policy.
	}
	if span.isolateAllocOwner != 0 {
		isolateOwnershipViolation("isolate: canonical message info table must be process-owned")
	}
	if isolateTypeObjectBase(infos.array) != uintptr(infos.array) {
		return // An interior slice cannot grant its entire allocation provenance.
	}
	// A process table must not retain private references in its copied headers,
	// even if an earlier uninstrumented caller manufactured an invalid table.
	isolateCheckHeapSliceCopy(typ, nil, infos.len, infos.array, infos.len, true)
	// Verify the completed descriptors before publishing any element. Empty
	// map-entry slots have no Go message type and receive no reference privilege.
	for i := 0; i < infos.len; i++ {
		p := add(infos.array, uintptr(i)*typ.Size_)
		desc := (*iface)(add(p, descOffset))
		if desc.tab == nil {
			continue
		}
		dt := desc.tab.Type
		if dt.Kind() != abi.Pointer {
			throw("isolate: invalid canonical message descriptor")
		}
		dt = (*ptrtype)(unsafe.Pointer(dt)).Elem
		if toRType(dt).pkgpath() != "google.golang.org/protobuf/internal/filedesc" || toRType(dt).string() != "filedesc.Message" {
			throw("isolate: unaudited canonical message descriptor")
		}
		if ds := spanOfHeap(uintptr(desc.data)); ds != nil && ds.isolateAllocOwner != 0 {
			isolateOwnershipViolation("isolate: canonical message descriptor must be process-owned")
		}
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	if reflectOffs.isolateMessageInfoArrays == nil {
		reflectOffs.isolateMessageInfoArrays = make(map[unsafe.Pointer]isolateMessageInfoLayout)
	}
	if reflectOffs.isolateProtoInfos == nil {
		reflectOffs.isolateProtoInfos = make(map[*abi.Type]any)
	}
	if reflectOffs.isolateProtoValueTypes == nil {
		reflectOffs.isolateProtoValueTypes = make(map[*abi.Type]bool)
	}
	reflectOffs.isolateMessageInfoArrays[infos.array] = isolateMessageInfoLayout{typ.Size_, readonly, uintptr(infos.len)}
	for i := 0; i < infos.len; i++ {
		p := add(infos.array, uintptr(i)*typ.Size_)
		if (*iface)(add(p, descOffset)).tab != nil {
			reflectOffs.isolateTypes[p] = readonly
			reflectOffs.isolateTypes[(*iface)(add(p, descOffset)).data] = 0
			for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
				switch field.Name.Name() {
				case "GoReflectType":
					value := (*iface)(add(p, field.Offset))
					if value.tab != nil {
						goType := (*abi.Type)(value.data)
						reflectOffs.isolateProtoValueTypes[goType] = true
						info := any(nil)
						efaceOf(&info)._type = resolveTypeOff(unsafe.Pointer(typ), typ.PtrToThis)
						efaceOf(&info).data = p
						reflectOffs.isolateProtoInfos[(*ptrtype)(unsafe.Pointer(goType)).Elem] = info
					}
				case "OneofWrappers":
					for _, value := range *(*[]any)(add(p, field.Offset)) {
						reflectOffs.isolateProtoValueTypes[efaceOf(&value)._type] = true
					}
				}
			}
		}
	}
	reflectOffsUnlock()
}

//go:linkname isolateCheckProtoValueType
func isolateCheckProtoValueType(typ *abi.Type) {
	reflectOffsLock()
	valid := reflectOffs.isolateProtoValueTypes[typ]
	reflectOffsUnlock()
	if !valid || typ.Kind() == abi.Pointer && !isolateSupportedProtoStruct((*ptrtype)(unsafe.Pointer(typ)).Elem) {
		isolateRejectEffect("unaudited protobuf clone type")
	}
}

// The pinned builder registers generated enums with the same process registry.
// EnumInfo has only immutable GoReflectType/Desc fields in this revision.
func isolatePublishEnumInfos(values any) {
	gp := getg()
	if gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateMetadataDepth != 0 {
		panic("isolate: canonical enum infos require process initialization")
	}
	e := efaceOf(&values)
	if e._type == nil || e._type.Kind() != abi.Slice {
		throw("isolate: invalid enum info table")
	}
	typ := (*slicetype)(unsafe.Pointer(e._type)).Elem
	if typ.Kind() != abi.Struct || toRType(typ).pkgpath() != "google.golang.org/protobuf/internal/impl" || toRType(typ).name() != "EnumInfo" {
		throw("isolate: unexpected enum info type")
	}
	fields := (*structtype)(unsafe.Pointer(typ)).Fields
	if len(fields) != 2 || fields[0].Name.Name() != "GoReflectType" || fields[1].Name.Name() != "Desc" {
		throw("isolate: unexpected enum info layout")
	}
	items := *(*slice)(e.data)
	if items.len == 0 {
		return
	}
	if span := spanOfHeap(uintptr(items.array)); span == nil || span.isolateAllocOwner != 0 || isolateTypeObjectBase(items.array) != uintptr(items.array) {
		throw("isolate: invalid enum info allocation")
	}
	isolateCheckHeapSliceCopy(typ, nil, items.len, items.array, items.len, true)
	// Validate all descriptors before granting any immutable enum record.
	for i := 0; i < items.len; i++ {
		p := add(items.array, uintptr(i)*typ.Size_)
		desc := (*iface)(add(p, fields[1].Offset))
		if desc.tab == nil {
			continue
		}
		dt := desc.tab.Type
		if dt.Kind() != abi.Pointer {
			throw("isolate: invalid canonical enum descriptor")
		}
		dt = (*ptrtype)(unsafe.Pointer(dt)).Elem
		if toRType(dt).pkgpath() != "google.golang.org/protobuf/internal/filedesc" || toRType(dt).name() != "Enum" {
			throw("isolate: unaudited canonical enum descriptor")
		}
		if span := spanOfHeap(uintptr(desc.data)); span != nil && span.isolateAllocOwner != 0 {
			isolateOwnershipViolation("isolate: canonical enum descriptor must be process-owned")
		}
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	if reflectOffs.isolateProtoInfos == nil {
		reflectOffs.isolateProtoInfos = make(map[*abi.Type]any)
	}
	for i := 0; i < items.len; i++ {
		p := add(items.array, uintptr(i)*typ.Size_)
		goType := (*abi.Type)((*iface)(add(p, fields[0].Offset)).data)
		desc := (*iface)(add(p, fields[1].Offset))
		if goType == nil || desc.tab == nil {
			continue
		}
		if goType.Kind() != abi.Int32 {
			throw("isolate: unexpected generated enum Go type")
		}
		info := any(nil)
		efaceOf(&info)._type = resolveTypeOff(unsafe.Pointer(typ), typ.PtrToThis)
		efaceOf(&info).data = p
		reflectOffs.isolateProtoInfos[goType] = info
		reflectOffs.isolateTypes[p] = typ.Size_
		reflectOffs.isolateTypes[desc.data] = 0
	}
	reflectOffsUnlock()
}
