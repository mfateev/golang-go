// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

// This is an audited exception to the integer/string map-key rule for the
// pinned Temporal SDK's SearchAttributes collection. Its seven concrete keys
// contain a private name/valueType pair plus reflect.Type metadata derived from
// that pair. Ordering the metadata's address would differ between processes.
// Validate the exact SDK types/layout, then order by name and valueType only.
// No user method or comparison callback runs inside map iteration.
// Arbitrary interface keys and pointer-bearing structs remain unsupported.
//
//go:linkname maps_isolateSearchAttributeKeyType internal/runtime/maps.isolateSearchAttributeKeyType
func maps_isolateSearchAttributeKeyType(typ *abi.Type) bool {
	return typ.Kind() == abi.Interface && toRType(typ).pkgpath() == "go.temporal.io/sdk/internal" && toRType(typ).name() == "SearchAttributeKey"
}

//go:linkname maps_isolateSearchAttributeKey internal/runtime/maps.isolateSearchAttributeKey
func maps_isolateSearchAttributeKey(typ *abi.Type, key unsafe.Pointer) (string, int32) {
	if !maps_isolateSearchAttributeKeyType(typ) {
		panic("isolate: unsupported search attribute map key")
	}
	value := (*iface)(key)
	if value.tab == nil {
		panic("isolate: nil search attribute map key")
	}
	concrete := value.tab.Type
	rt := toRType(concrete)
	if concrete.Kind() != abi.Struct || rt.pkgpath() != "go.temporal.io/sdk/internal" {
		panic("isolate: unsupported search attribute map key")
	}
	var expected int32
	switch rt.name() {
	case "SearchAttributeKeyString":
		expected = 1
	case "SearchAttributeKeyKeyword":
		expected = 2
	case "SearchAttributeKeyInt64":
		expected = 3
	case "SearchAttributeKeyFloat64":
		expected = 4
	case "SearchAttributeKeyBool":
		expected = 5
	case "SearchAttributeKeyTime":
		expected = 6
	case "SearchAttributeKeyKeywordList":
		expected = 7
	default:
		panic("isolate: unsupported search attribute map key")
	}
	fields := (*structtype)(unsafe.Pointer(concrete)).Fields
	if len(fields) != 1 || fields[0].Name.Name() != "baseSearchAttributeKey" || fields[0].Typ.Kind() != abi.Struct {
		panic("isolate: incompatible search attribute SDK layout")
	}
	base := (*structtype)(unsafe.Pointer(fields[0].Typ))
	if len(base.Fields) != 3 || base.Fields[0].Name.Name() != "name" || base.Fields[0].Typ.Kind() != abi.String || base.Fields[1].Name.Name() != "valueType" || base.Fields[1].Typ.Kind() != abi.Int32 || base.Fields[2].Name.Name() != "reflectType" || base.Fields[2].Typ.Kind() != abi.Interface || toRType(base.Fields[2].Typ).pkgpath() != "reflect" || toRType(base.Fields[2].Typ).name() != "Type" {
		panic("isolate: incompatible search attribute SDK layout")
	}
	isolateCheckHeapComparable(concrete, value.data)
	data := add(value.data, fields[0].Offset)
	name := *(*string)(add(data, base.Fields[0].Offset))
	kind := *(*int32)(add(data, base.Fields[1].Offset))
	if kind != expected {
		panic("isolate: invalid search attribute SDK key")
	}
	return name, kind
}
