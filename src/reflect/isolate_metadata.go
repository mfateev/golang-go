// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect

import (
	"internal/abi"
	"unsafe"
)

// Only type construction uses the process metadata scope. Value allocation,
// reflective calls, frame pools, and caller-provided predicates stay under the
// caller's owner. Type constructors copy input descriptions into immutable
// metadata; they never publish input slices or decoded application values.
//
//go:linkname isolateEnterMetadata runtime.isolateEnterMetadata
func isolateEnterMetadata() uintptr

//go:linkname isolateLeaveMetadata runtime.isolateLeaveMetadata
func isolateLeaveMetadata(uintptr)

// Record only the canonical result, after cache insertion and shared-lock
// cleanup. Borrowed input descriptions are never registered.
//
//go:linkname isolatePublishType runtime.isolatePublishType
func isolatePublishType(*abi.Type)

//go:linkname isolateCopyMetadataString runtime.isolateCopyMetadataString
func isolateCopyMetadataString(string) string

// Reflective dispatch crosses an assembly trampoline, so its target needs the
// same callback and raw-atomic checks as a compiler-generated indirect call.
//
//go:linkname isolateCheckMetadataCall runtime.isolateCheckMetadataCall
func isolateCheckMetadataCall(uintptr)

//go:linkname isolateCheckHeapAtomicCall runtime.isolateCheckHeapAtomicCall
//go:noescape
func isolateCheckHeapAtomicCall(uintptr, unsafe.Pointer, unsafe.Pointer, unsafe.Pointer)

//go:linkname isolateCheckHeapAccess runtime.isolateCheckHeapAccess
//go:noescape
func isolateCheckHeapAccess(unsafe.Pointer, uintptr, bool)

func isolateCheckReflectCall(fn unsafe.Pointer, in []Value) {
	isolateCheckHeapAccess(fn, unsafe.Sizeof(uintptr(0)), false)
	pc := *(*uintptr)(fn)
	isolateCheckMetadataCall(pc)
	if len(in) == 0 || in[0].Kind() != Pointer && in[0].Kind() != UnsafePointer {
		return
	}
	var pointers [3]unsafe.Pointer
	for i := 0; i < len(in) && i < len(pointers); i++ {
		if in[i].Kind() == Pointer || in[i].Kind() == UnsafePointer {
			pointers[i] = in[i].UnsafePointer()
		}
	}
	isolateCheckHeapAtomicCall(pc, pointers[0], pointers[1], pointers[2])
}
