// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// isolatePackageState maps immutable compiler keys to this instance's layouts.
// Its typed g reference participates in GC and is reset on goroutine reuse.
type isolatePackageState struct {
	keys                                    []unsafe.Pointer
	bases                                   []unsafe.Pointer
	libraryKeys, libraryTypes, libraryTasks []unsafe.Pointer
}

//go:linkname isolateNewPackageState
func isolateNewPackageState(keys, types []unsafe.Pointer) unsafe.Pointer {
	if len(keys) != len(types) {
		panic("isolate: package keys and types have different lengths")
	}
	table := &isolatePackageState{
		keys:  append([]unsafe.Pointer(nil), keys...),
		bases: make([]unsafe.Pointer, len(types)),
	}
	for i, typ := range types {
		if keys[i] == nil || typ == nil {
			panic("isolate: nil package key or type")
		}
		for j := range i {
			if keys[i] == keys[j] {
				panic("isolate: duplicate package key")
			}
		}
		table.bases[i] = isolateNewLibraryState(keys[i], (*_type)(typ))
	}
	return unsafe.Pointer(table)
}

//go:linkname isolateRunInitTask
func isolateRunInitTask(task unsafe.Pointer) {
	// The compiler emits an immutable count and function list for each
	// opted-in package. The process init task's completion state is separate.
	nfns := *(*uint32)(task)
	firstFunc := add(task, 8)
	for i := uint32(0); i < nfns; i++ {
		p := add(firstFunc, uintptr(i)*unsafe.Sizeof(uintptr(0)))
		f := *(*func())(unsafe.Pointer(&p))
		f()
	}
}

func (table *isolatePackageState) base(key unsafe.Pointer) unsafe.Pointer {
	for i, candidate := range table.keys {
		if candidate == key {
			return table.bases[i]
		}
	}
	panic("isolate: package has no state in selected isolate")
}

//go:linkname isolateSetPackageState
func isolateSetPackageState(table unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateState
	gp.isolateState = (*isolatePackageState)(table)
	return unsafe.Pointer(old)
}

//go:linkname isolateGetPackageBase
func isolateGetPackageBase(key unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	if gp.isolateMetadataDepth != 0 {
		return nil
	}
	if gp.isolateState != nil {
		return gp.isolateState.base(key)
	}
	return nil // Ordinary process initialization has no instance layout.
}

//go:linkname isolateSetReadOnlyLibraries
func isolateSetReadOnlyLibraries(p unsafe.Pointer, keys, types, tasks []unsafe.Pointer) {
	table := (*isolatePackageState)(p)
	table.libraryKeys = append([]unsafe.Pointer(nil), keys...)
	table.libraryTypes = append([]unsafe.Pointer(nil), types...)
	table.libraryTasks = append([]unsafe.Pointer(nil), tasks...)
}

// A read-only handler receives fresh standard-library caches. Reinitializing
// these layouts prevents cache writes from touching workflow memory; callbacks
// still run under the scratch owner and cannot mutate captured workflow data.
func isolateReadOnlyLibraries() {
	gp := getg()
	original := gp.isolateState
	if original == nil {
		return
	}
	table := &isolatePackageState{
		keys:  append([]unsafe.Pointer(nil), original.keys...),
		bases: append([]unsafe.Pointer(nil), original.bases...),
	}
	for i, key := range original.libraryKeys {
		for j, candidate := range table.keys {
			if candidate == key {
				table.bases[j] = isolateNewLibraryState(key, (*_type)(original.libraryTypes[i]))
				break
			}
		}
	}
	gp.isolateState = table
	for i, task := range original.libraryTasks {
		if !isolateHasFrozenLibrary(original.libraryKeys[i]) {
			isolateRunInitTask(task)
		}
	}
}
