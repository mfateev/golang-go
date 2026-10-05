// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestIsolateHeapAccess(t *testing.T) {
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	for _, size := range []int{1, 17, 4096, 65536} {
		values := [][]byte{runtime.IsolateMetadataBytesForTest(size), nil, nil}
		for i, group := range groups {
			runtime.IsolateMetadataRunForTest(group, owners[i], func() {
				values[i+1] = runtime.IsolateMetadataBytesForTest(size)
			})
		}
		check := func(current int, service bool) {
			for target, value := range values {
				for _, write := range []bool{false, true} {
					allowed := !service && target == current || service && (target == 0 || target == current && !write)
					for _, offset := range []int{0, size / 2, size - 1} {
						func() {
							defer func() {
								got := recover()
								if allowed && got != nil {
									t.Errorf("size=%d current=%d target=%d service=%v write=%v offset=%d: %v", size, current, target, service, write, offset, got)
								} else if !allowed {
									want := "isolate: read from foreign heap"
									if write {
										want = "isolate: write to foreign heap"
									}
									if got != want {
										t.Errorf("foreign access: got %v, want %q", got, want)
									}
								}
							}()
							runtime.IsolateHeapAccessForTest(unsafe.Pointer(&value[offset]), uintptr(size-offset), write)
						}()
					}
				}
			}
		}
		check(0, false)
		for i, group := range groups {
			runtime.IsolateMetadataRunForTest(group, owners[i], func() {
				check(i+1, false)
				runtime.IsolateMetadataScopeForTest(func() { check(i+1, true) })
			})
		}
		runtime.GC()
		runtime.KeepAlive(values)
	}
	runtime.KeepAlive(groups)
}

var isolatePublicationSink unsafe.Pointer

func TestIsolateHeapPublication(t *testing.T) {
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	values := [][]byte{runtime.IsolateMetadataBytesForTest(17), nil, nil}
	for i, group := range groups {
		runtime.IsolateMetadataRunForTest(group, owners[i], func() { values[i+1] = runtime.IsolateMetadataBytesForTest(17) })
	}
	check := func(allowed bool, fn func()) {
		t.Helper()
		defer func() {
			got := recover()
			if allowed && got != nil {
				t.Errorf("allowed publication: %v", got)
			}
			if !allowed && got != "isolate: foreign heap reference publication" {
				t.Errorf("foreign publication: %v", got)
			}
		}()
		fn()
	}
	for target, slot := range values {
		for source, value := range values {
			check(target == source, func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&slot[8]), unsafe.Pointer(&value[1])) })
		}
	}
	runtime.IsolateMetadataRunForTest(groups[0], owners[0], func() {
		check(true, func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&values[1][8]), nil) })
		check(false, func() {
			runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&isolatePublicationSink), unsafe.Pointer(&values[1][0]))
		})
		runtime.IsolateMetadataScopeForTest(func() {
			runtime.IsolateHeapAccessForTest(unsafe.Pointer(&values[1][0]), 17, false)
			check(false, func() {
				runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&values[0][8]), unsafe.Pointer(&values[1][0]))
			})
		})
		// A late pointer beyond several GC bitmap bytes must be checked, even
		// when the outer source and destination both belong to this instance.
		source, target := new([10000]*byte), new([10000]*byte)
		for _, p := range []unsafe.Pointer{unsafe.Pointer(source), unsafe.Pointer(target)} {
			if owner, ok := runtime.IsolateAllocOriginForTest(p); !ok || owner != owners[0] {
				t.Fatalf("bulk object owner=(%d,%v), want %d", owner, ok, owners[0])
			}
		}
		source[9999] = &values[0][0] // Test-only construction of an invalid graph.
		check(false, func() { runtime.IsolateHeapMoveForTest(target, source) })
		source[9999] = &values[1][0]
		check(true, func() { runtime.IsolateHeapMoveForTest(target, source) })
		source[9999] = nil
		check(true, func() { runtime.IsolateHeapMoveForTest(target, source) })
		runtime.KeepAlive(source)
		runtime.KeepAlive(target)
	})
	runtime.GC()
	runtime.KeepAlive(values)
	runtime.KeepAlive(groups)
}
