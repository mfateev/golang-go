// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateCheckClosure
func isolateCheckClosure(p unsafe.Pointer) {
	isolateCheckHeapAccess(p, unsafe.Sizeof(uintptr(0)), false)
}

// Compiler function-value materialization preserves domain provenance even for
// noncapturing callbacks and private globals initialized with static functions.
// The code PC is unchanged (including reflect.Value.Pointer). Ordinary host
// values retain their original representation.
func isolateBindFunction(p unsafe.Pointer) unsafe.Pointer {
	if p == nil || !isolateActive() || getg().isolateMetadataDepth != 0 || getg().isolateOwner == 0 || spanOfHeap(uintptr(p)) != nil {
		return p
	}
	// Stack closures also have no heap span. They may contain captures and
	// must keep their complete environment; only immutable static funcvals are
	// eligible for a one-word domain box.
	if known, readonly := isolateStaticRange(uintptr(p), uintptr(p)+unsafe.Sizeof(uintptr(0))-1); !known || !readonly {
		return p
	}
	f := new(funcval)
	f.fn = (*funcval)(p).fn
	return unsafe.Pointer(f)
}
