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
		unsafe.Sizeof(abi.MapType{}), unsafe.Sizeof(abi.FuncType{}) + 2*unsafe.Sizeof(uintptr(0)), unsafe.Sizeof(abi.StructType{})}
	methodRoot := reflect.ValueOf(types[7]).UnsafePointer()
	ut := (*abi.Type)(methodRoot).Uncommon()
	sizes[7] = uintptr(unsafe.Pointer(ut)) - uintptr(methodRoot) + uintptr(ut.Moff) + uintptr(ut.Mcount)*unsafe.Sizeof(abi.Method{})
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

func TestIsolateHeapMessageInfoPrefixes(t *testing.T) {
	// The final 32 bytes remain unapproved, like size-class padding. The first
	// three records each have eight immutable bytes followed by mutable cells.
	data := runtime.IsolateMetadataBytesForTest(128)
	base := unsafe.Pointer(unsafe.SliceData(data))
	runtime.IsolateMessageInfoLayoutForTest(base, 32, 8, 3, []bool{true, true, false})
	group := runtime.IsolateMetadataGroupForTest()
	owner := nextAllocTestOwner()
	check := func(want string, fn func()) {
		defer func() {
			got := recover()
			if want == "" && got != nil || want != "" && got != want {
				// testing.T owns process state. In particular, Helper must
				// never allocate its helper-PC map under a synthetic owner.
				runtime.IsolateMetadataRunForTest(nil, 0, func() {
					t.Errorf("got %v, want %q", got, want)
				})
			}
		}()
		fn()
	}
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		dst := runtime.IsolateMetadataBytesForTest(8)
		for i := 0; i < 3; i++ {
			p := unsafe.Add(base, i*32)
			check("", func() { runtime.IsolateHeapAccessForTest(p, 8, false) })
			check("", func() { runtime.IsolateHeapAccessForTest(unsafe.Add(p, 7), 1, false) })
			check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(unsafe.Add(p, 7), 2, false) })
			check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(unsafe.Add(p, 8), 1, false) })
			check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(p, 32, false) })
			check("isolate: write to foreign heap", func() { runtime.IsolateHeapAccessForTest(p, 1, true) })
			want := ""
			if i == 2 { // An empty map-entry slot is not a canonical message root.
				want = "isolate: foreign heap reference publication"
			}
			check(want, func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&dst[0]), p) })
			check("isolate: foreign heap reference publication", func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&dst[0]), unsafe.Add(p, 1)) })
		}
		check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(unsafe.Add(base, 96), 1, false) })
		runtime.IsolateMetadataScopeForTest(func() {
			check("", func() { runtime.IsolateHeapAccessForTest(base, 128, false) })
		})
	})
	// Ordinary process access to its own metadata is unaffected.
	check("", func() { runtime.IsolateHeapAccessForTest(base, 128, true) })
	runtime.GC()
	runtime.KeepAlive(data)
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

//go:noinline
func isolateHeapMapFixture[K comparable, V any]() map[K]V { return make(map[K]V) }

func TestIsolateMetadataMapBorrowing(t *testing.T) {
	t.Run("uint32", func(t *testing.T) { testIsolateMetadataMapBorrowing(t, func(n int) uint32 { return uint32(n) }) })
	t.Run("uint64", func(t *testing.T) { testIsolateMetadataMapBorrowing(t, func(n int) uint64 { return uint64(n) }) })
	t.Run("string", func(t *testing.T) {
		testIsolateMetadataMapBorrowing(t, func(n int) string { return string(rune('a' + n)) })
	})
	t.Run("struct", func(t *testing.T) {
		testIsolateMetadataMapBorrowing(t, func(n int) [3]int { return [3]int{n, n + 1, n + 2} })
	})
}

func testIsolateMetadataMapBorrowing[K comparable](t *testing.T, key func(int) K) {
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	keys := make([]K, 41)
	for i := range keys {
		keys[i] = key(i)
	}
	run := func(current int, fn func()) {
		if current == 0 {
			runtime.IsolateMetadataRunForTest(nil, 0, fn)
		} else {
			runtime.IsolateMetadataRunForTest(groups[current-1], owners[current-1], fn)
		}
	}
	for _, count := range []int{0, 1, 40} {
		values := make([]map[K]int, 3)
		for current := range values {
			run(current, func() {
				values[current] = isolateHeapMapFixture[K, int]()
				for i := range count {
					values[current][keys[i]] = i + 1
				}
			})
		}
		for current := range values {
			for depth := range 3 {
				run(current, func() {
					check := func() {
						for target, value := range values {
							service := current != 0 && depth != 0
							readAllowed := target == 0 || target == current
							writeAllowed := !service && target == current || service && target == 0
							checkOperation := func(name string, allowed bool, write bool, fn func()) {
								defer func() {
									got := recover()
									want := "isolate: map read crosses owner boundary"
									if write {
										want = "isolate: map write crosses owner boundary"
									}
									if allowed && got == nil || !allowed && got == want {
										return
									}
									// Test bookkeeping belongs to the process, even when a
									// synthetic owner is active or a borrowed lookup fails.
									runtime.IsolateMetadataRunForTest(nil, 0, func() {
										t.Errorf("count=%d current=%d depth=%d target=%d operation=%s allowed=%v: %v", count, current, depth, target, name, allowed, got)
									})
								}()
								fn()
							}
							// Ordinary runtime reads preserve process-map compatibility;
							// the opt-in compiler diagnostic enforces the stricter policy.
							diagnosticAllowed := !service && target == current || service && (target == 0 || target == current)
							checkOperation("diagnostic read", diagnosticAllowed, false, func() { runtime.IsolateHeapMapForTest(value, false) })
							checkOperation("lookup", readAllowed, false, func() {
								got, ok := value[keys[0]]
								if count == 0 && (got != 0 || ok) || count != 0 && (got != 1 || !ok) {
									panic("incorrect map lookup")
								}
							})
							checkOperation("missing", readAllowed, false, func() {
								if _, ok := value[keys[40]]; ok {
									panic("incorrect missing-key lookup")
								}
							})
							checkOperation("range", readAllowed, false, func() {
								n, sum := 0, 0
								for _, v := range value {
									n++
									sum += v
								}
								if n != count || sum != count*(count+1)/2 {
									panic("incorrect map iteration")
								}
							})
							checkOperation("diagnostic write", writeAllowed, true, func() { runtime.IsolateHeapMapForTest(value, true) })
							checkOperation("assign", writeAllowed, true, func() {
								value[keys[40]] = 41
								delete(value, keys[40])
							})
							checkOperation("delete", writeAllowed, true, func() { delete(value, keys[40]) })
							if !writeAllowed {
								checkOperation("clear", false, true, func() { clear(value) })
							}
						}
					}
					if depth == 0 {
						check()
					} else {
						runtime.IsolateMetadataScopeForTest(func() {
							if depth == 1 {
								check()
							} else {
								runtime.IsolateMetadataScopeForTest(check)
							}
						})
					}
				})
			}
		}
		runtime.KeepAlive(values)
	}
	runtime.KeepAlive(groups)
}

func TestIsolateHeapMapKeys(t *testing.T) {
	type key struct{ Pointers [10000]*int }
	groups := []unsafe.Pointer{runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()}
	owners := []uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
	maps := []map[key]bool{isolateHeapMapFixture[key, bool](), nil, nil}
	keys := []*key{new(key), nil, nil}
	pointees := []*int{new(int), nil, nil}
	for i, group := range groups {
		runtime.IsolateMetadataRunForTest(group, owners[i], func() {
			maps[i+1], keys[i+1], pointees[i+1] = isolateHeapMapFixture[key, bool](), new(key), new(int)
		})
	}
	for i := range keys {
		keys[i].Pointers[9999] = pointees[i]
		// A key larger than the map header exposes an incorrect implementation
		// that checks header+field-offset instead of the destination map owner.
		if got, ok := runtime.IsolateAllocOriginForTest(reflect.ValueOf(maps[i]).UnsafePointer()); !ok || i > 0 && got != owners[i-1] || i == 0 && got != 0 {
			t.Fatal("map fixture is not heap-owned")
		}
	}
	check := func(want string, fn func()) {
		t.Helper()
		defer func() {
			if got := recover(); want == "" && got != nil || want != "" && got != want {
				t.Errorf("got %v, want %q", got, want)
			}
		}()
		fn()
	}
	for current := range maps {
		run := func() {
			for target, dst := range maps {
				for source, src := range keys {
					for _, publish := range []bool{false, true} {
						want := ""
						if target != current {
							want = "isolate: map read crosses owner boundary"
							if publish {
								want = "isolate: map write crosses owner boundary"
							}
						} else if source != current {
							want = "isolate: read from foreign heap"
						}
						check(want, func() { runtime.IsolateHeapMapKeyForTest(dst, src, publish) })
					}
				}
			}
		}
		if current == 0 {
			run()
		} else {
			runtime.IsolateMetadataRunForTest(groups[current-1], owners[current-1], run)
		}
	}
	runtime.IsolateMetadataRunForTest(groups[0], owners[0], func() {
		keys[1].Pointers[9999] = pointees[0] // Test-only invalid graph.
		check("isolate: foreign heap reference publication", func() { runtime.IsolateHeapMapKeyForTest(maps[1], keys[1], true) })
		keys[1].Pointers[9999] = pointees[1]
		runtime.IsolateMetadataScopeForTest(func() {
			check("", func() { runtime.IsolateHeapMapKeyForTest(maps[0], keys[1], false) })
			check("isolate: foreign heap reference publication", func() { runtime.IsolateHeapMapKeyForTest(maps[0], keys[1], true) })
		})
	})
	runtime.GC()
	runtime.KeepAlive(keys)
	runtime.KeepAlive(pointees)
	runtime.KeepAlive(maps)
	runtime.KeepAlive(groups)
}

var isolateStaticCounter int

func TestIsolateHeapStaticAndStack(t *testing.T) {
	// Keep a foreign goroutine's stack alive without escaping one of its locals.
	ready, release := make(chan unsafe.Pointer), make(chan struct{})
	go func() { ready <- runtime.IsolateCurrentGForTest(); <-release }()
	foreign := <-ready
	defer close(release)
	group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
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
	global := unsafe.Pointer(&isolateStaticCounter)
	check("", func() { runtime.IsolateHeapAccessForTest(global, unsafe.Sizeof(isolateStaticCounter), true) })
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		for _, write := range []bool{false, true} {
			check("isolate: access to process global", func() { runtime.IsolateHeapAccessForTest(global, unsafe.Sizeof(isolateStaticCounter), write) })
		}
		text := "immutable string"
		p := unsafe.Pointer(unsafe.StringData(text))
		check("", func() { runtime.IsolateHeapAccessForTest(p, uintptr(len(text)), false) })
		check("", func() { runtime.IsolateHeapReferenceForTest(nil, p) })
		check("isolate: write to read-only memory", func() { runtime.IsolateHeapAccessForTest(p, 1, true) })
		check("isolate: process global reference publication", func() { runtime.IsolateHeapReferenceForTest(nil, global) })
		check("", func() { runtime.IsolateStackAccessForTest(nil, false) })
		check("isolate: access to foreign stack or runtime memory", func() { runtime.IsolateStackAccessForTest(foreign, false) })
		check("isolate: access to foreign stack or runtime memory", func() { runtime.IsolateStackAccessForTest(nil, true) })
		check("isolate: stack reference publication", func() { runtime.IsolateStackPublicationForTest(nil) })
		runtime.IsolateMetadataScopeForTest(func() {
			check("", func() { runtime.IsolateHeapAccessForTest(global, unsafe.Sizeof(isolateStaticCounter), true) })
			check("isolate: access to foreign stack or runtime memory", func() { runtime.IsolateStackAccessForTest(foreign, false) })
		})
	})
	runtime.KeepAlive(group)
}
