// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import _ "unsafe" // for go:linkname

//go:linkname isolateMapOwner runtime.isolateGetOwner
func isolateMapOwner() uintptr

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

// IsolateOwner includes maps whose header or first group lives on the stack.
// Compiler diagnostics must check this logical owner before invoking a lookup,
// even when no heap object is read (for example, a missing key or len).
func (m *Map) IsolateOwner() uintptr { return m.owner }

func (m *Map) checkIsolateWrite() {
	if m.owner != isolateMapOwner() {
		panic("isolate: map write crosses owner boundary")
	}
}

func (m *Map) checkIsolateRead() {
	if m.owner != 0 && m.owner != isolateMapOwner() {
		panic("isolate: map read crosses owner boundary")
	}
}
