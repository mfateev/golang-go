// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import (
	"internal/abi"
	"unsafe"
)

//go:linkname isolateMapOwner runtime.isolateGetOwner
func isolateMapOwner() uintptr

//go:linkname isolateMetadataBorrowOwner runtime.isolateMetadataBorrowOwner
func isolateMetadataBorrowOwner() uintptr

//go:linkname isolateOwnershipViolation runtime.isolateOwnershipViolation
func isolateOwnershipViolation(string)

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

// IsolateOwner includes maps whose header or first group lives on the stack.
// Compiler diagnostics must check this logical owner before invoking a lookup,
// even when no heap object is read (for example, a missing key or len).
func (m *Map) IsolateOwner() uintptr { return m.owner }

func (m *Map) checkIsolateWrite() {
	if m.owner != isolateMapOwner() {
		if borrowed := isolateReadOnlyOwner(); borrowed != 0 && m.owner == borrowed {
			panic("isolate: read-only handler cannot mutate workflow map")
		}
		isolateOwnershipViolation("isolate: map write crosses owner boundary")
	}
}

func (m *Map) checkIsolateRead() {
	if m.owner != 0 && m.owner != isolateMapOwner() {
		if m.owner == isolateMetadataBorrowOwner() || m.owner == isolateReadOnlyOwner() {
			return
		}
		isolateOwnershipViolation("isolate: map read crosses owner boundary")
	}
}

//go:linkname isolateCheckCloneGroup runtime.isolateCheckCloneGroup
func isolateCheckCloneGroup(*abi.MapType, unsafe.Pointer, unsafe.Pointer)

//go:linkname isolateReadOnlyOwner runtime.isolateReadOnlyOwner
func isolateReadOnlyOwner() uintptr
