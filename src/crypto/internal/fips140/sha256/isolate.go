// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sha256

import "unsafe"

//go:linkname isolateCheckHeapAccess runtime.isolateCheckHeapAccess
//go:noescape
func isolateCheckHeapAccess(unsafe.Pointer, uintptr, bool)

func isolateCheckInput(d *Digest, p []byte) {
	isolateCheckHeapAccess(unsafe.Pointer(d), unsafe.Sizeof(*d), true)
	isolateCheckHeapAccess(unsafe.Pointer(unsafe.SliceData(p)), uintptr(len(p)), false)
}
