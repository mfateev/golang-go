// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"unsafe"
)

// descriptor mirrors the experimental compiler record. This inspection is
// intentionally tied to the current fork's runtime type and descriptor ABI.
type descriptor struct {
	path           string
	key            unsafe.Pointer
	typeSlot       unsafe.Pointer
	dependencyTask unsafe.Pointer
	initTask       unsafe.Pointer
}

//go:linkname reflectDescriptor reflect.isolatePackageDescriptor
var reflectDescriptor byte

//go:linkname jsonDescriptor encoding/json.isolatePackageDescriptor
var jsonDescriptor byte

//go:linkname jsonInternalDescriptor encoding/json/internal.isolatePackageDescriptor
var jsonInternalDescriptor byte

//go:linkname jsonOptsDescriptor encoding/json/internal/jsonopts.isolatePackageDescriptor
var jsonOptsDescriptor byte

//go:linkname jsonTextDescriptor encoding/json/jsontext.isolatePackageDescriptor
var jsonTextDescriptor byte

//go:linkname jsonV2Descriptor encoding/json/v2.isolatePackageDescriptor
var jsonV2Descriptor byte

func size(p *byte) uintptr {
	d := (*descriptor)(unsafe.Pointer(p))
	typ := *(*unsafe.Pointer)(d.typeSlot)
	return *(*uintptr)(typ)
}

func main() {
	for _, item := range []struct {
		name string
		ptr  *byte
	}{
		{"reflect", &reflectDescriptor},
		{"encoding/json", &jsonDescriptor},
		{"encoding/json/internal", &jsonInternalDescriptor},
		{"encoding/json/internal/jsonopts", &jsonOptsDescriptor},
		{"encoding/json/jsontext", &jsonTextDescriptor},
		{"encoding/json/v2", &jsonV2Descriptor},
	} {
		fmt.Printf("%s %d\n", item.name, size(item.ptr))
	}
}
