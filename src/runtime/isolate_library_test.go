// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

type frozenLibraryFixture struct {
	bytes []byte
	index map[string]*frozenLibraryFixture
}

func frozenLibraryForTest() *frozenLibraryFixture {
	return runtime.IsolateFrozenLibraryForTest(func() any {
		value := &frozenLibraryFixture{bytes: make([]byte, 7)}
		value.bytes[0] = 42
		value.index = map[string]*frozenLibraryFixture{"self": value}
		return value
	}).(*frozenLibraryFixture)
}

func TestIsolateFrozenLibraryRanges(t *testing.T) {
	value := frozenLibraryForTest()
	bytes := unsafe.Pointer(unsafe.SliceData(value.bytes))
	group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
	check := func(want string, fn func()) {
		defer func() {
			if got := recover(); got != nil && got != want || got == nil && want != "" {
				runtime.IsolateMetadataRunForTest(nil, 0, func() { t.Errorf("got %v, want %q", got, want) })
			}
		}()
		fn()
	}
	runtime.GC()
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		check("", func() { runtime.IsolateHeapAccessForTest(bytes, 7, false) })
		check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(bytes, 8, false) })
		check("isolate: write to foreign heap", func() { runtime.IsolateHeapAccessForTest(bytes, 1, true) })
		check("", func() { runtime.IsolateHeapMapForTest(value.index, false) })
		check("isolate: map write crosses owner boundary", func() { runtime.IsolateHeapMapForTest(value.index, true) })
		destination := runtime.IsolateMetadataBytesForTest(8)
		check("", func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&destination[0]), unsafe.Add(bytes, 6)) })
	})
	runtime.IsolateReadOnlyRunForTest(group, owner, nextAllocTestOwner(), func() {
		check("isolate: read-only handler cannot mutate shared library data", func() { runtime.IsolateHeapAccessForTest(bytes, 1, true) })
		check("isolate: read-only handler cannot mutate shared library map", func() { runtime.IsolateHeapMapForTest(value.index, true) })
		check("", func() { runtime.IsolateHeapAccessForTest(bytes, 7, false) })
	})
}

func TestIsolateFrozenLibraryPublication(t *testing.T) {
	value := frozenLibraryForTest()
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
			runtime.IsolateMetadataRunForTest(group, owner, func() {
				for range 100 {
					runtime.IsolateHeapAccessForTest(unsafe.Pointer(unsafe.SliceData(value.bytes)), 7, false)
					runtime.IsolateHeapMapForTest(value.index, false)
				}
			})
		})
	}
	// Readers of the earlier immutable snapshot must remain valid while a new
	// graph is published. Publication is serialized by process initialization.
	for range 16 {
		_ = frozenLibraryForTest()
	}
	wg.Wait()
}

func TestIsolateFrozenLibraryRejectsUnrecordedCapture(t *testing.T) {
	foreign := &frozenLibraryFixture{bytes: make([]byte, 7)}
	defer func() {
		if got := recover(); got != "isolate: frozen library retains unrecorded process memory" {
			t.Fatalf("publication: %v", got)
		}
	}()
	_ = runtime.IsolateFrozenLibraryForTest(func() any {
		return &frozenLibraryFixture{index: map[string]*frozenLibraryFixture{"foreign": foreign}}
	})
}
