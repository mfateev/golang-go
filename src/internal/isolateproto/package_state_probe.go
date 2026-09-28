// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolateproto

import (
	"fmt"
	"slices"
	"unsafe"
)

// PackageInitSpec is a tagged host-side probe description for one opted-in
// package. DependencyTask is the compiler's list of direct imports selected
// for isolate state. Discovery and selection are still supplied by the build.
type PackageInitSpec struct {
	Path           string
	Key            unsafe.Pointer
	Type           unsafe.Pointer
	DependencyTask unsafe.Pointer
	InitTask       unsafe.Pointer
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
// their initializers in dependency order. Its input is an explicit manifest;
// whole-program package selection remains open.
func NewPackageInstance(specs []PackageInitSpec) (*PackageInstance, error) {
	ordered := slices.Clone(specs)
	slices.SortFunc(ordered, func(a, b PackageInitSpec) int {
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
		if spec.Path == "" || spec.Key == nil || spec.Type == nil || spec.DependencyTask == nil || spec.InitTask == nil {
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
		types[i] = spec.Type
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
		deps := slices.Clone(unsafe.Slice((*string)(unsafe.Add(depTask, 8)), count))
		slices.Sort(deps)
		for _, path := range deps {
			j, ok := index[path]
			if !ok {
				return fmt.Errorf("isolateproto: selected dependency %q of %q is missing", path, ordered[i].Path)
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
