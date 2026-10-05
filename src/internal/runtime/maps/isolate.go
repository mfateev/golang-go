// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import _ "unsafe" // for go:linkname

//go:linkname isolateMapOwner runtime.isolateGetOwner
func isolateMapOwner() uintptr

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

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
