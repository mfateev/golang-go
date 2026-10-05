// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package runtime

import "unsafe"

// These functions are an opt-in E4 probe for a per-goroutine global base.
// They do not redirect ordinary Go package-global accesses.

//go:linkname isolateE4SetBase
func isolateE4SetBase(base unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateE4Base
	gp.isolateE4Base = base
	return old
}

//go:linkname isolateE4GetBase
func isolateE4GetBase() unsafe.Pointer {
	return getg().isolateE4Base
}

//go:linkname isolateE4NewState
func isolateE4NewState(typ unsafe.Pointer) unsafe.Pointer {
	return newobject((*_type)(typ))
}

// isolateE4PackageBases is a tagged probe for selecting two independently
// compiled package layouts in one goroutine. A later whole-program layout
// should replace this table with one base and linker-assigned offsets.
type isolateE4PackageBases struct {
	keys  []unsafe.Pointer
	bases []unsafe.Pointer
}

//go:linkname isolateE4NewPackageBases
func isolateE4NewPackageBases(keys, types []unsafe.Pointer) unsafe.Pointer {
	if len(keys) != len(types) {
		panic("isolate: package keys and types have different lengths")
	}
	table := &isolateE4PackageBases{
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
		table.bases[i] = newobject((*_type)(typ))
	}
	return unsafe.Pointer(table)
}

//go:linkname isolateE4RunInitTask
func isolateE4RunInitTask(task unsafe.Pointer) {
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

func (table *isolateE4PackageBases) base(key unsafe.Pointer) unsafe.Pointer {
	for i, candidate := range table.keys {
		if candidate == key {
			return table.bases[i]
		}
	}
	panic("isolate: package has no state in selected isolate")
}

//go:linkname isolateE4SetPackageBases
func isolateE4SetPackageBases(table unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	old := gp.isolateE4Bases
	gp.isolateE4Bases = table
	return old
}

//go:linkname isolateE4PackageBase
func isolateE4PackageBase(table, key unsafe.Pointer) unsafe.Pointer {
	return (*isolateE4PackageBases)(table).base(key)
}

//go:linkname isolateE4GetPackageBase
func isolateE4GetPackageBase(key unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	if gp.isolateMetadataDepth != 0 {
		return nil // Only audited metadata operations may select process state.
	}
	if gp.isolateE4Bases != nil {
		return (*isolateE4PackageBases)(gp.isolateE4Bases).base(key)
	}
	return gp.isolateE4Base
}

//go:linkname isolateE4GetImportedPackageBase
func isolateE4GetImportedPackageBase(key unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	if gp.isolateMetadataDepth != 0 {
		return nil // Only audited metadata operations may select process state.
	}
	if gp.isolateE4Bases != nil {
		return (*isolateE4PackageBases)(gp.isolateE4Bases).base(key)
	}
	if gp.isolateE4Base != nil {
		panic("isolate: imported package has no state in selected isolate")
	}
	return nil // ordinary process initialization
}
