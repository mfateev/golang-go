// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

type IsolateItabMarker struct{}

func (IsolateItabMarker) Marker() {}

func TestIsolateHeapItabTables(t *testing.T) {
	// Distinct dynamic types force cold itab creation and table growth. A table
	// records types/code only; it must not retain a receiver or a private cache.
	types := make([]reflect.Type, 700)
	for i := range types {
		types[i] = reflect.StructOf([]reflect.StructField{
			{Name: "IsolateItabMarker", Type: reflect.TypeFor[IsolateItabMarker](), Anonymous: true},
			{Name: "Padding", Type: reflect.ArrayOf(i+1, reflect.TypeFor[byte]())},
		})
	}
	group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
	var failure string
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		for _, typ := range types {
			receiver := reflect.New(typ).Elem().Interface().(interface{ Marker() })
			receiver.Marker()
			pointer, size := runtime.IsolateItabBoundsForTest(receiver)
			runtime.IsolateHeapAccessForTest(pointer, size, false)
			runtime.IsolateHeapReferenceForTest(nil, pointer)
			func() {
				defer func() {
					if recover() != "isolate: write to read-only memory" {
						failure = "dynamic itab write was allowed"
					}
				}()
				runtime.IsolateHeapAccessForTest(pointer, 1, true)
			}()
		}
	})
	if failure != "" {
		t.Fatal(failure)
	}
	_, tableOwner := runtime.IsolateItabTableForTest()
	if tableOwner != 0 {
		t.Fatalf("shared itab table owner = %d", tableOwner)
	}
	// A heap allocation with an itab-sized layout gains no provenance.
	fake := runtime.IsolateMetadataBytesForTest(int(unsafe.Sizeof(uintptr(0)) * 8))
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		defer func() {
			if recover() != "isolate: read from foreign heap" {
				failure = "unregistered itab lookalike was accepted"
			}
		}()
		runtime.IsolateHeapAccessForTest(unsafe.Pointer(&fake[0]), uintptr(len(fake)), false)
	})
	if failure != "" {
		t.Fatal(failure)
	}
	runtime.KeepAlive(group)
}
