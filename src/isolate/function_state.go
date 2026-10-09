// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolate

import (
	"internal/isolatebridge"
	"internal/isolateproto"
	"unsafe"
)

func newSupportedState(a, b isolatebridge.FunctionEntry) (func(func()), error) {
	packages, libraries := a.StateDescriptors()
	extraPackages, extraLibraries := b.StateDescriptors()
	// Descriptors are immutable linker metadata. Duplicate dependencies retain
	// one layout and one initializer, even when reached by both entry points.
	union := func(a, b []unsafe.Pointer) []unsafe.Pointer {
		seen := make(map[unsafe.Pointer]bool, len(a)+len(b))
		result := make([]unsafe.Pointer, 0, len(a)+len(b))
		for _, group := range [][]unsafe.Pointer{a, b} {
			for _, p := range group {
				if !seen[p] {
					seen[p] = true
					result = append(result, p)
				}
			}
		}
		return result
	}
	state, err := isolateproto.NewPackageInstance(union(packages, extraPackages), union(libraries, extraLibraries))
	if err != nil {
		return nil, err
	}
	return state.Run, nil
}
