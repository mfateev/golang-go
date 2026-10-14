// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package runtime

import (
	"internal/isolatepolicy"
	"unsafe"
)

//go:linkname isolatePrepareFrozenLibraries
func isolatePrepareFrozenLibraries(paths []string, keys, types, tasks []unsafe.Pointer) {
	gp := getg()
	if gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateMetadataDepth != 0 || gp.isolateProtoInit != nil {
		throw("isolate: frozen libraries require process initialization")
	}
	if len(paths) != len(keys) || len(keys) != len(types) || len(types) != len(tasks) {
		throw("isolate: inconsistent frozen library descriptors")
	}
	if len(keys) == 0 {
		return
	}
	table := &isolateE4PackageBases{keys: append([]unsafe.Pointer(nil), keys...), bases: make([]unsafe.Pointer, len(keys))}
	previous := isolateLoadLibraries()
	next := &isolateLibrarySnapshot{states: make(map[unsafe.Pointer]isolateFrozenLibrary), parts: make(map[unsafe.Pointer][]isolateTypePart)}
	if previous != nil {
		for key, state := range previous.states {
			next.states[key] = state
		}
		for base, parts := range previous.parts {
			next.parts[base] = append([]isolateTypePart(nil), parts...)
		}
	}
	state := &isolateProtoInitialization{library: true}
	gp.isolateProtoInit = state
	oldBases := gp.isolateE4Bases
	gp.isolateE4Bases = unsafe.Pointer(table)
	defer func() {
		gp.isolateProtoInit = nil
		gp.isolateE4Bases = oldBases
	}()
	var roots []unsafe.Pointer
	for i, key := range keys {
		if !isolatepolicy.FrozenLibraryPackage(paths[i]) || key == nil || types[i] == nil || tasks[i] == nil {
			throw("isolate: unaudited frozen library descriptor")
		}
		typ := (*_type)(types[i])
		if existing, found := next.states[key]; found {
			if existing.typ != typ {
				throw("isolate: frozen library layout mismatch")
			}
			table.bases[i] = existing.base
		} else {
			table.bases[i] = newobject(typ)
		}
	}
	for i, key := range keys {
		if _, found := next.states[key]; !found {
			isolateE4RunInitTask(tasks[i])
			roots = append(roots, table.bases[i])
		}
	}
	gp.isolateProtoInit = nil // Freeze only reachable initializer allocations.
	gp.isolateE4Bases = oldBases
	isolatePublishLibraryGraph(next, roots, state.allocations)
	for i, key := range keys {
		next.states[key] = isolateFrozenLibrary{typ: (*_type)(types[i]), base: table.bases[i]}
	}
	if raceenabled {
		racerelease(unsafe.Pointer(next))
	}
	isolateLibraries.Store(next)
}
