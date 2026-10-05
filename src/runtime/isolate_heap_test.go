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
