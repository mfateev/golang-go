// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"internal/stringslite"
	"unsafe"
)

//go:linkname isolateCheckAtomicValue
func isolateCheckAtomicValue(p unsafe.Pointer, value any, write bool) {
	isolateCheckHeapAccess(p, unsafe.Sizeof(eface{}), write)
	e := efaceOf(&value)
	if e._type == nil {
		return
	}
	// Copying an interface retains its data pointer even for direct types.
	// For boxed values, also inspect the fields copied by type algorithms.
	isolateCheckHeapReference(p, e.data)
	if !e._type.IsDirectIface() {
		isolateCheckHeapSliceCopy(e._type, p, 1, e.data, 1, true)
	}
}

// Comparison reads boxed values and comparable fields but does not retain a
// direct pointer/channel operand. Preserve pointer-identity CAS semantics.
//
//go:linkname isolateCheckAtomicCompare
func isolateCheckAtomicCompare(value any) {
	e := efaceOf(&value)
	isolateCheckHeapInterfaceData(e._type, e.data)
}

// Indirect scalar atomic calls reach assembly without a Go entry body. The
// compiler supplies pointer operands only; identity comparison operands are
// not published. Typed methods retain the checks in their Go implementations.
//
//go:linkname isolateCheckHeapAtomicCall
func isolateCheckHeapAtomicCall(pc uintptr, address, second, third unsafe.Pointer) {
	name := funcname(findfunc(pc))
	if !stringslite.HasPrefix(name, "sync/atomic.") {
		return
	}
	name = name[len("sync/atomic."):]
	var width uintptr
	switch {
	case stringslite.HasSuffix(name, "Int32"), stringslite.HasSuffix(name, "Uint32"):
		width = 4
	case stringslite.HasSuffix(name, "Int64"), stringslite.HasSuffix(name, "Uint64"):
		width = 8
	case stringslite.HasSuffix(name, "Uintptr"), stringslite.HasSuffix(name, "Pointer"):
		width = goarch.PtrSize
	default:
		return
	}
	write := !stringslite.HasPrefix(name, "Load")
	isolateCheckHeapAccess(address, width, write)
	if write && stringslite.HasSuffix(name, "Pointer") {
		value := second
		if stringslite.HasPrefix(name, "CompareAndSwap") {
			value = third
		}
		isolateCheckHeapReference(address, value)
	}
}
