// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/stringslite"
	"unsafe"
)

// Audited metadata builders publish immutable types into process registries.
// This is not a general escape hatch for application state or converters.
// Enter before taking a service lock and defer Leave before deferring Unlock:
// the locks must be released before the outermost Leave discards a revoked G.
// The group and boundary remain attached, so process API restrictions and live
// accounting still apply. A service may not start goroutines or call the host.
//
//go:linkname isolateEnterMetadata
func isolateEnterMetadata() uintptr {
	gp := getg()
	if gp.isolateGroup == nil && gp.isolateOwner == 0 && gp.isolateMetadataDepth == 0 {
		return 0
	}
	isolateDiscardIfRevoked()
	if gp.isolateMetadataDepth == ^uint32(0) {
		throw("isolate: metadata nesting overflow")
	}
	owner := gp.isolateOwner
	gp.isolateMetadataDepth++
	gp.isolateOwner = 0
	return owner
}

//go:linkname isolateLeaveMetadata
func isolateLeaveMetadata(owner uintptr) {
	gp := getg()
	if gp.isolateMetadataDepth == 0 {
		if owner != 0 {
			throw("isolate: metadata scope underflow")
		}
		return // Ordinary host execution did not enter a scope.
	}
	if gp.isolateOwner != 0 || gp.isolateMetadataDepth > 1 && owner != 0 {
		throw("isolate: metadata owner changed inside service")
	}
	gp.isolateOwner = owner
	gp.isolateMetadataDepth--
	if gp.isolateMetadataDepth == 0 {
		isolateDiscardIfRevoked()
	}
}

// Called by the compiler for unsupported registry mutation, callbacks and legacy
// descriptor builders. Host implementations retain their original behavior.
func isolateRejectMetadataAPI(name string) {
	if isolateActive() {
		panic("isolate: unaudited metadata operation " + name)
	}
}

// A shared operation must never promote a caller's mutable cache or descriptor
// to the process owner. Nil receivers retain their ordinary method semantics.
func isolateCheckMetadataReceiver(p unsafe.Pointer) {
	if !isolateActive() || p == nil {
		return
	}
	if s := spanOfHeap(uintptr(p)); s != nil {
		if s.isolateAllocOwner == 0 {
			return
		}
	} else if isGoPointerWithoutSpan(p) {
		return
	}
	panic("isolate: metadata service requires a process-owned receiver")
}

func isolateCheckMetadataDescriptor(value any) {
	if !isolateActive() {
		return
	}
	e := efaceOf(&value)
	if e._type == nil {
		return
	}
	typ := e._type
	if typ.Kind() == abi.Pointer {
		typ = (*ptrtype)(unsafe.Pointer(typ)).Elem
	}
	if toRType(typ).pkgpath() != "google.golang.org/protobuf/internal/filedesc" {
		panic("isolate: unaudited metadata descriptor implementation")
	}
	isolateCheckMetadataReceiver(e.data)
}

// Service scopes must not propagate to user callbacks hidden behind interfaces
// or function values. The compiler inserts this at dynamic call sites in an
// isolate build; the ordinary host and application paths return immediately.
func isolateCheckMetadataCall(pc uintptr) {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateMetadataDepth == 0 {
		return
	}
	name := funcname(findfunc(pc))
	for _, prefix := range [...]string{
		"runtime.", "internal/", "reflect.", "sync.", "sync/atomic.",
		"bytes.", "strings.", "strconv.", "fmt.", "errors.", "sort.",
		"cmp.", "slices.", "maps.", "iter.", "encoding/", "unicode/", "unicode.",
		"type:.eq.", "type:.hash.",
		"google.golang.org/protobuf/", "go.temporal.io/api/",
	} {
		if stringslite.HasPrefix(name, prefix) {
			return
		}
	}
	panic("isolate: unaudited metadata callback " + name)
}
