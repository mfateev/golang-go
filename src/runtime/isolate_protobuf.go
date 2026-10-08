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
	if reflectOffs.isolateProtoValueTypes == nil {
		reflectOffs.isolateProtoValueTypes = make(map[*abi.Type]bool)
	}
	reflectOffs.isolateMessageInfoArrays[infos.array] = isolateMessageInfoLayout{typ.Size_, readonly, uintptr(infos.len)}
	for i := 0; i < infos.len; i++ {
		p := add(infos.array, uintptr(i)*typ.Size_)
		if (*iface)(add(p, descOffset)).tab != nil {
			reflectOffs.isolateTypes[p] = readonly
			for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
				switch field.Name.Name() {
				case "GoReflectType":
					value := (*iface)(add(p, field.Offset))
					if value.tab != nil {
						reflectOffs.isolateProtoValueTypes[(*abi.Type)(value.data)] = true
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
	if !valid {
		isolateRejectEffect("unaudited protobuf clone type")
	}
}
