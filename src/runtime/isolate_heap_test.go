// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/abi"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
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

func TestIsolateHeapCanonicalTypes(t *testing.T) {
	makeTypes := func() []reflect.Type {
		array := reflect.ArrayOf(1001, reflect.TypeFor[int]())
		record := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: array}})
		return []reflect.Type{array, record, reflect.PointerTo(record), reflect.SliceOf(record),
			reflect.ChanOf(reflect.BothDir, record), reflect.MapOf(reflect.TypeFor[string](), record),
			reflect.FuncOf([]reflect.Type{record}, []reflect.Type{array}, false),
			reflect.StructOf([]reflect.StructField{{Name: "Time", Type: reflect.TypeFor[time.Time](), Anonymous: true}})}
	}
	// Concurrent cold and cached construction must publish only canonical roots.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 8 {
				makeTypes()
			}
		})
	}
	wg.Wait()
	types := makeTypes()
	sizes := []uintptr{unsafe.Sizeof(abi.ArrayType{}), unsafe.Sizeof(abi.StructType{}),
		unsafe.Sizeof(abi.PtrType{}), unsafe.Sizeof(abi.SliceType{}), unsafe.Sizeof(abi.ChanType{}),
		unsafe.Sizeof(abi.MapType{}), unsafe.Sizeof(abi.FuncType{}), unsafe.Sizeof(abi.StructType{})}
	group := runtime.IsolateMetadataGroupForTest()
	owner := nextAllocTestOwner()
	check := func(want string, fn func()) {
		t.Helper()
		defer func() {
			got := recover()
			if want == "" && got != nil || want != "" && got != want {
				t.Errorf("got %v, want %q", got, want)
			}
		}()
		fn()
	}
	// A process object with exactly the metadata layout is still untrusted.
	fake := isolateUnregisteredType()
	if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(fake)); !ok || got != 0 {
		t.Fatalf("fake owner=(%d,%v), want process heap", got, ok)
	}
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		slot := runtime.IsolateMetadataBytesForTest(32)
		for i, typ := range types {
			root := reflect.ValueOf(typ).UnsafePointer()
			if got, ok := runtime.IsolateAllocOriginForTest(root); !ok || got != 0 {
				t.Fatalf("type %v owner=(%d,%v), want process heap", typ, got, ok)
			}
			if makeTypes()[i] != typ {
				t.Error("canonical identity changed")
			}
			check("", func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&slot[0]), root) })
			check("", func() { runtime.IsolateHeapAccessForTest(root, sizes[i], false) })
			check("", func() { runtime.IsolateHeapAccessForTest(unsafe.Add(root, sizes[i]-1), 1, false) })
			check("isolate: write to foreign heap", func() { runtime.IsolateHeapAccessForTest(root, 1, true) })
			check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(root, sizes[i]+1, false) })
			check("isolate: foreign heap reference publication", func() {
				runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&slot[0]), unsafe.Add(root, 1))
			})
		}
		check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(unsafe.Pointer(fake), 1, false) })
		check("isolate: foreign heap reference publication", func() {
			runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&slot[0]), unsafe.Pointer(fake))
		})
	})
	runtime.GC()
	runtime.KeepAlive(fake)
	runtime.KeepAlive(types)
	runtime.KeepAlive(group)
}

//go:noinline
func isolateUnregisteredType() *abi.ArrayType { return new(abi.ArrayType) }

func TestIsolateHeapSliceCopies(t *testing.T) {
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	values := []([]*int){runtime.IsolatePointerSliceForTest(10000), nil, nil}
	for i, group := range groups {
		runtime.IsolateMetadataRunForTest(group, owners[i], func() { values[i+1] = runtime.IsolatePointerSliceForTest(10000) })
	}
	check := func(want string, fn func()) {
		t.Helper()
		defer func() {
			got := recover()
			if want == "" && got != nil || want != "" && got != want {
				t.Errorf("got %v, want %q", got, want)
			}
		}()
		fn()
	}
	for current := 0; current < len(values); current++ {
		run := func() {
			for target, dst := range values {
				for source, src := range values {
					want := ""
					if source != current {
						want = "isolate: read from foreign heap"
					} else if target != current {
						want = "isolate: write to foreign heap"
					}
					check(want, func() { runtime.IsolateHeapSliceCopyForTest(dst, src, true) })
					check("", func() { runtime.IsolateHeapSliceCopyForTest(dst[:0], src, true) })
					check("", func() { runtime.IsolateHeapSliceCopyForTest(dst, src[:0], true) })
				}
			}
		}
		if current == 0 {
			run()
		} else {
			runtime.IsolateMetadataRunForTest(groups[current-1], owners[current-1], run)
		}
	}
	hostValue := new(int)
	// Force the pointee to escape independently of its slice header.
	values[0][9999] = hostValue
	runtime.IsolateMetadataRunForTest(groups[0], owners[0], func() {
		src, dst := values[1], values[1][:9999]
		src[9999] = hostValue // Test-only corruption of an owned source graph.
		check("", func() { runtime.IsolateHeapSliceCopyForTest(dst, src, true) })
		check("isolate: foreign heap reference publication", func() { runtime.IsolateHeapSliceCopyForTest(src, src, true) })
		check("", func() { runtime.IsolateHeapNewSliceCopyForTest(src, 9999, true) })
		check("isolate: foreign heap reference publication", func() { runtime.IsolateHeapNewSliceCopyForTest(src, 10000, true) })
		check("", func() { runtime.IsolateHeapNewSliceCopyForTest(src, 10000, false) })
		check("", func() { runtime.IsolateHeapNewSliceCopyForTest(values[0], 0, true) })
		src[9999] = nil
	})
	runtime.KeepAlive(hostValue)
	runtime.KeepAlive(values)
	runtime.KeepAlive(groups)
}
