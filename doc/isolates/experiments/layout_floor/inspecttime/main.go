// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"unsafe"
)

// descriptor mirrors the experimental compiler record.
type descriptor struct {
	path           string
	key            unsafe.Pointer
	typeSlot       unsafe.Pointer
	dependencyTask unsafe.Pointer
	initTask       unsafe.Pointer
}

//go:linkname timeDescriptor time.isolatePackageDescriptor
var timeDescriptor byte

func main() {
	d := (*descriptor)(unsafe.Pointer(&timeDescriptor))
	typ := *(*unsafe.Pointer)(d.typeSlot)
	fmt.Printf("time %d\n", *(*uintptr)(typ))
}
