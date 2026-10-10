// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolateproto

import (
	"cmp"
	"fmt"
	"internal/isolateabi"
	"slices"
	"unsafe"
)

type packageDependency struct {
	Path string
	Key  unsafe.Pointer
}

// packageDescriptor is the compiler-owned metadata for one selected package.
// Discovery and selection are still supplied by the build.
type packageDescriptor struct {
	MetadataVersion uint64
	Path            string
	Key             unsafe.Pointer
	TypeSlot        unsafe.Pointer
	DependencyTask  unsafe.Pointer
	InitTask        unsafe.Pointer
}

// PackageInstance is one set of independent package globals for the tagged
// compiler/runtime probe. It is not the public isolate API.
type PackageInstance struct {
	table unsafe.Pointer
}

//go:linkname isolateNewPackageBases runtime.isolateE4NewPackageBases
func isolateNewPackageBases(keys, types []unsafe.Pointer) unsafe.Pointer

//go:linkname isolateSetPackageBases runtime.isolateE4SetPackageBases
func isolateSetPackageBases(unsafe.Pointer) unsafe.Pointer

//go:linkname isolateRunInitTask runtime.isolateE4RunInitTask
func isolateRunInitTask(unsafe.Pointer)

// NewPackageInstance allocates the selected packages' layouts and replays
// their initializers in dependency order. Its input is an explicit list of
// compiler-owned descriptors; whole-program discovery remains open.
func NewPackageInstance(descriptors []unsafe.Pointer, readOnlyLibraries ...[]unsafe.Pointer) (*PackageInstance, error) {
	ordered := make([]packageDescriptor, len(descriptors))
	for i, descriptor := range descriptors {
		if descriptor == nil {
			return nil, fmt.Errorf("isolateproto: nil package descriptor at index %d", i)
		}
		// Read the fixed prefix before interpreting any version-specific fields.
		if version := *(*uint64)(descriptor); version != isolateabi.MetadataVersion {
			return nil, fmt.Errorf("isolateproto: incompatible package metadata version %d, runtime requires %d; rebuild with a compatible toolchain", version, isolateabi.MetadataVersion)
		}
		ordered[i] = *(*packageDescriptor)(descriptor)
	}
	slices.SortFunc(ordered, func(a, b packageDescriptor) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	index := make(map[string]int, len(ordered))
	keys := make([]unsafe.Pointer, len(ordered))
	types := make([]unsafe.Pointer, len(ordered))
	seenKeys := make(map[unsafe.Pointer]bool, len(ordered))
	for i, spec := range ordered {
		if spec.Path == "" || spec.Key == nil || spec.TypeSlot == nil || spec.DependencyTask == nil || spec.InitTask == nil {
			return nil, fmt.Errorf("isolateproto: incomplete package state spec for %q", spec.Path)
		}
		if _, exists := index[spec.Path]; exists {
			return nil, fmt.Errorf("isolateproto: duplicate package %q", spec.Path)
		}
		if seenKeys[spec.Key] {
			return nil, fmt.Errorf("isolateproto: duplicate package key for %q", spec.Path)
		}
		index[spec.Path] = i
		seenKeys[spec.Key] = true
		keys[i] = spec.Key
		types[i] = *(*unsafe.Pointer)(spec.TypeSlot)
		if types[i] == nil {
			return nil, fmt.Errorf("isolateproto: missing package layout type for %q", spec.Path)
		}
	}

	state := make([]uint8, len(ordered))
	var initOrder []int
	var visit func(int) error
	visit = func(i int) error {
		if state[i] == 2 {
			return nil
		}
		if state[i] == 1 {
			return fmt.Errorf("isolateproto: package initialization cycle at %q", ordered[i].Path)
		}
		state[i] = 1
		depTask := ordered[i].DependencyTask
		count := *(*uint32)(depTask)
		deps := slices.Clone(unsafe.Slice((*packageDependency)(unsafe.Add(depTask, 8)), count))
		slices.SortFunc(deps, func(a, b packageDependency) int { return cmp.Compare(a.Path, b.Path) })
		for _, dep := range deps {
			j, ok := index[dep.Path]
			if !ok {
				return fmt.Errorf("isolateproto: selected dependency %q of %q is missing", dep.Path, ordered[i].Path)
			}
			if dep.Key != ordered[j].Key {
				return fmt.Errorf("isolateproto: selected dependency %q of %q has a mismatched package key", dep.Path, ordered[i].Path)
			}
			if err := visit(j); err != nil {
				return err
			}
		}
		state[i] = 2
		initOrder = append(initOrder, i)
		return nil
	}
	for i := range ordered {
		if err := visit(i); err != nil {
			return nil, err
		}
	}

	instance := &PackageInstance{table: isolateNewPackageBases(keys, types)}
	if len(readOnlyLibraries) != 0 {
		selected := make(map[unsafe.Pointer]bool)
		for _, descriptor := range readOnlyLibraries[0] {
			selected[(*packageDescriptor)(descriptor).Key] = true
		}
		var libraryKeys, libraryTypes, libraryTasks []unsafe.Pointer
		for _, i := range initOrder {
			if selected[keys[i]] {
				libraryKeys = append(libraryKeys, keys[i])
				libraryTypes = append(libraryTypes, types[i])
				libraryTasks = append(libraryTasks, ordered[i].InitTask)
			}
		}
		isolateSetReadOnlyLibraries(instance.table, libraryKeys, libraryTypes, libraryTasks)
	}
	instance.Run(func() {
		for _, i := range initOrder {
			isolateRunInitTask(ordered[i].InitTask)
		}
	})
	return instance, nil
}

// Run selects this instance for fn and any native child goroutines it starts.
func (instance *PackageInstance) Run(fn func()) {
	old := isolateSetPackageBases(instance.table)
	defer isolateSetPackageBases(old)
	fn()
}

//go:linkname isolateSetReadOnlyLibraries runtime.isolateE4SetReadOnlyLibraries
func isolateSetReadOnlyLibraries(unsafe.Pointer, []unsafe.Pointer, []unsafe.Pointer, []unsafe.Pointer)
