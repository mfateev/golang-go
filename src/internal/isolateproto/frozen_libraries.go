// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolateproto

import (
	"internal/isolateabi"
	"internal/isolatepolicy"
	"sync"
	"unsafe"
)

var frozenLibraryInit sync.Mutex

// PrepareReadOnlyLibraries is called by the compiler-generated process entry
// before registering isolate functions. Build independent snapshots rather
// than exposing mutable host globals. Process initialization and instance
// initialization retain separate variable bindings.
func PrepareReadOnlyLibraries(descriptors []unsafe.Pointer) {
	frozenLibraryInit.Lock()
	defer frozenLibraryInit.Unlock()
	var paths []string
	var keys, types, tasks []unsafe.Pointer
	for _, pointer := range descriptors {
		if pointer == nil || *(*uint64)(pointer) != isolateabi.MetadataVersion {
			panic("isolateproto: incompatible frozen library descriptor; rebuild with the current toolchain")
		}
		descriptor := (*packageDescriptor)(pointer)
		if isolatepolicy.FrozenLibraryPackage(descriptor.Path) {
			paths = append(paths, descriptor.Path)
			keys = append(keys, descriptor.Key)
			types = append(types, *(*unsafe.Pointer)(descriptor.TypeSlot))
			tasks = append(tasks, descriptor.InitTask)
		}
	}
	// The audited alias initializer depends on Unicode tables. The other
	// initializers use literals and stateless helpers; no application init runs.
	for i := range paths {
		if paths[i] == "unicode" {
			paths[0], paths[i] = paths[i], paths[0]
			keys[0], keys[i] = keys[i], keys[0]
			types[0], types[i] = types[i], types[0]
			tasks[0], tasks[i] = tasks[i], tasks[0]
			break
		}
	}
	isolatePrepareFrozenLibraries(paths, keys, types, tasks)
}

//go:linkname isolatePrepareFrozenLibraries runtime.isolatePrepareFrozenLibraries
func isolatePrepareFrozenLibraries([]string, []unsafe.Pointer, []unsafe.Pointer, []unsafe.Pointer)

//go:linkname isolateHasFrozenLibrary runtime.isolateHasFrozenLibrary
func isolateHasFrozenLibrary(unsafe.Pointer) bool
