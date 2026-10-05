// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Interface equality invokes type algorithms that may live in uninstrumented
// runtime/library code. Validate the value bytes those algorithms can read,
// including string backing storage and nested interface boxes. Pointer/channel
// equality compares addresses; it does not read the objects they identify.
func isolateCheckHeapInterfaceEqual(typeword unsafe.Pointer, x, y unsafe.Pointer, nonempty bool) {
	if typeword == nil {
		return
	}
	var typ *_type
	if nonempty {
		typ = (*itab)(typeword).Type
	} else {
		typ = (*_type)(typeword)
	}
	isolateCheckHeapInterfaceData(typ, x)
	isolateCheckHeapInterfaceData(typ, y)
}

func isolateCheckHeapInterfaceData(typ *_type, data unsafe.Pointer) {
	if typ == nil || typ.Equal == nil || typ.IsDirectIface() {
		return // Preserve nil, direct comparison and uncomparable-type panics.
	}
	isolateCheckHeapComparable(typ, data)
}

func isolateCheckHeapComparable(typ *_type, data unsafe.Pointer) {
	isolateCheckHeapAccess(data, typ.Size_, false)
	if typ.PtrBytes == 0 {
		return
	}
	switch typ.Kind() {
	case abi.String:
		value := *(*string)(data)
		isolateCheckHeapAccess(unsafe.Pointer(unsafe.StringData(value)), uintptr(len(value)), false)
	case abi.Array:
		array := (*arraytype)(unsafe.Pointer(typ))
		if array.Elem.PtrBytes != 0 {
			for i := uintptr(0); i < array.Len; i++ {
				isolateCheckHeapComparable(array.Elem, add(data, i*array.Elem.Size_))
			}
		}
	case abi.Struct:
		for _, field := range (*structtype)(unsafe.Pointer(typ)).Fields {
			if field.Typ.PtrBytes != 0 && field.Name.Name() != "_" {
				isolateCheckHeapComparable(field.Typ, add(data, field.Offset))
			}
		}
	case abi.Interface:
		if len((*interfacetype)(unsafe.Pointer(typ)).Methods) == 0 {
			value := (*eface)(data)
			isolateCheckHeapInterfaceData(value._type, value.data)
		} else {
			value := (*iface)(data)
			if value.tab != nil {
				isolateCheckHeapInterfaceData(value.tab.Type, value.data)
			}
		}
	}
}
