// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Only the pinned SDK's process initializer publishes ErrNoData. This exact
// immutable errors.errorString is shared to preserve SDK sentinel identity.
// Neither arbitrary error implementations nor their reachable graphs qualify.
func isolatePublishErrorSentinel(value any) {
	gp := getg()
	if gp.isolateOwner != 0 || gp.isolateGroup != nil || gp.isolateMetadataDepth != 0 {
		throw("isolate: error sentinels require process initialization")
	}
	e := efaceOf(&value)
	if e._type == nil || e._type.Kind() != abi.Pointer || e.data == nil {
		throw("isolate: invalid immutable error sentinel")
	}
	typ := (*ptrtype)(unsafe.Pointer(e._type)).Elem
	if typ.Kind() != abi.Struct || toRType(typ).pkgpath() != "errors" || toRType(typ).string() != "errors.errorString" || typ.Size_ != unsafe.Sizeof(string("")) {
		throw("isolate: unexpected immutable error sentinel type")
	}
	s := spanOfHeap(uintptr(e.data))
	if s == nil || s.isolateAllocOwner != 0 || isolateTypeObjectBase(e.data) != uintptr(e.data) {
		throw("isolate: error sentinel must be a process-owned object")
	}
	text := *(*string)(e.data)
	if owner, heap := isolateAllocOrigin(unsafe.Pointer(unsafe.StringData(text))); heap && owner != 0 {
		throw("isolate: error sentinel contains private text")
	}
	reflectOffsLock()
	if reflectOffs.isolateTypes == nil {
		reflectOffs.isolateTypes = make(map[unsafe.Pointer]uintptr)
	}
	reflectOffs.isolateTypes[e.data] = typ.Size_
	reflectOffsUnlock()
	isolatePublishTypePart(unsafe.Pointer(unsafe.StringData(text)), uintptr(len(text)))
}
