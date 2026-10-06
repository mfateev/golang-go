// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// The copied-byte bridge is the only application transport that snapshots host
// storage under a private allocator. Validate complete input ranges before the
// trusted copy, reject peers, and return newly allocated backing storage. This
// grants no reference sharing and no process allocation scope to caller code.
func isolateCheckBoundaryBytes(p unsafe.Pointer, size uintptr) {
	if p == nil || size == 0 {
		return
	}
	gp := getg()
	end := uintptr(p) + size - 1
	if end < uintptr(p) {
		isolateOwnershipViolation("isolate: boundary byte range overflow")
	}
	if uintptr(p) >= gp.stack.lo && end < gp.stack.hi {
		return
	}
	owner := gp.isolateOwner
	var original uintptr
	if gp.isolateGroup != nil {
		original = atomic.Loaduintptr(&gp.isolateGroup.alloc.cache.isolateOwner)
	}
	for addr := uintptr(p); ; {
		s := spanOfHeap(addr)
		if s == nil {
			if known, _ := isolateStaticRange(addr, end); known {
				return
			}
			isolateCheckNonHeapAccess(addr, end, false)
			return
		}
		if s.isolateAllocOwner != 0 && s.isolateAllocOwner != owner && (original == 0 || s.isolateAllocOwner != original) {
			isolateOwnershipViolation("isolate: boundary bytes belong to another instance")
		}
		if end < s.limit {
			return
		}
		addr = s.base() + s.npages*pageSize
		if addr > end {
			return
		}
	}
}

//go:linkname isolateCopyBoundaryBytes
func isolateCopyBoundaryBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	isolateCheckBoundaryBytes(unsafe.Pointer(unsafe.SliceData(src)), uintptr(len(src)))
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

//go:linkname isolateCopyBoundaryString
func isolateCopyBoundaryString(src string) string {
	if len(src) == 0 {
		return ""
	}
	isolateCheckBoundaryBytes(unsafe.Pointer(unsafe.StringData(src)), uintptr(len(src)))
	dst, data := rawstring(len(src))
	copy(data, src)
	return dst
}
