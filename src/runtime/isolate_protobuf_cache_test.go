// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

type testProtoField struct{ bytes []byte }
type testProtoCache struct {
	initDone           uint32
	reflectMessageInfo struct {
		fields     map[int]*testProtoField
		nilMessage *int
	}
	coderMessageInfo struct {
		fields []*testProtoField
		fn     func() byte
	}
}

func buildTestProtoCache() *testProtoCache {
	cache := new(testProtoCache)
	runtime.IsolateProtoCacheForTest(cache, func() {
		field := &testProtoField{bytes: make([]byte, 7)}
		field.bytes[0] = 42
		cache.reflectMessageInfo.fields = map[int]*testProtoField{1: field}
		cache.reflectMessageInfo.nilMessage = new(int)
		cache.coderMessageInfo.fields = []*testProtoField{field}
		cache.coderMessageInfo.fn = func() byte { return field.bytes[0] }
	})
	return cache
}

func TestIsolateSharedProtoCacheRanges(t *testing.T) {
	cache := buildTestProtoCache()
	group := runtime.IsolateMetadataGroupForTest()
	owner := nextAllocTestOwner()
	check := func(want string, fn func()) {
		defer func() {
			if got := recover(); got != nil && want == "" || got != nil && got != want || got == nil && want != "" {
				runtime.IsolateMetadataRunForTest(nil, 0, func() { t.Errorf("got %v, want %q", got, want) })
			}
		}()
		fn()
	}
	runtime.GC() // The immutable snapshot must keep its published allocations live.
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		field := cache.coderMessageInfo.fields[0]
		bytes := unsafe.Pointer(unsafe.SliceData(field.bytes))
		check("", func() { runtime.IsolateHeapAccessForTest(bytes, 7, false) })
		check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(bytes, 8, false) })
		check("isolate: write to foreign heap", func() { runtime.IsolateHeapAccessForTest(bytes, 1, true) })
		check("", func() { runtime.IsolateHeapMapForTest(cache.reflectMessageInfo.fields, false) })
		check("isolate: map write crosses owner boundary", func() { runtime.IsolateHeapMapForTest(cache.reflectMessageInfo.fields, true) })
		check("isolate: read from foreign heap", func() { runtime.IsolateHeapAccessForTest(unsafe.Pointer(&cache.initDone), 4, false) })
		check("isolate: read from foreign heap", func() {
			runtime.IsolateHeapAccessForTest(unsafe.Pointer(&cache.reflectMessageInfo.nilMessage), unsafe.Sizeof(uintptr(0)), false)
		})
		check("isolate: read from foreign heap", func() {
			runtime.IsolateHeapAccessForTest(unsafe.Pointer(cache.reflectMessageInfo.nilMessage), 1, false)
		})
		dst := runtime.IsolateMetadataBytesForTest(8)
		check("", func() { runtime.IsolateHeapReferenceForTest(unsafe.Pointer(&dst[0]), unsafe.Add(bytes, 6)) })
	})
}

func TestIsolateSharedProtoCacheConcurrentPublication(t *testing.T) {
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			cache := buildTestProtoCache()
			group := runtime.IsolateMetadataGroupForTest()
			owner := nextAllocTestOwner()
			runtime.IsolateMetadataRunForTest(group, owner, func() {
				for range 100 {
					bytes := cache.coderMessageInfo.fields[0].bytes
					runtime.IsolateHeapAccessForTest(unsafe.Pointer(unsafe.SliceData(bytes)), uintptr(len(bytes)), false)
					runtime.IsolateHeapMapForTest(cache.reflectMessageInfo.fields, false)
				}
			})
		})
	}
	wg.Wait()
}

func TestIsolateSharedProtoCacheRejectsPrivateCapture(t *testing.T) {
	var private []byte
	runtime.IsolateMetadataRunForTest(runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner(), func() {
		private = runtime.IsolateMetadataBytesForTest(8)
	})
	defer func() {
		if got := recover(); got != "isolate: shared protobuf cache retains private memory" {
			t.Fatalf("publication got %v", got)
		}
	}()
	cache := new(testProtoCache)
	captured := private
	runtime.IsolateProtoCacheForTest(cache, func() {
		cache.coderMessageInfo.fn = func() byte { return captured[0] }
	})
}

func TestIsolateSharedProtoCacheRejectsUnregisteredHostCapture(t *testing.T) {
	box := &testProtoField{bytes: make([]byte, 7)}
	defer func() {
		got, ok := recover().(string)
		if !ok || !strings.HasPrefix(got, "isolate: shared protobuf cache references unaudited process memory") {
			t.Fatalf("publication got %v", got)
		}
	}()
	cache := new(testProtoCache)
	runtime.IsolateProtoCacheForTest(cache, func() {
		cache.coderMessageInfo.fn = func() byte { return box.bytes[0] }
	})
}

func untrustedProtoCacheCallback() {}

func TestIsolateSharedProtoCacheRejectsHostCallback(t *testing.T) {
	pc := reflect.ValueOf(untrustedProtoCacheCallback).Pointer()
	runtime.IsolateMetadataCallbackForTest(pc) // Ordinary host callbacks stay valid.
	defer func() {
		got, ok := recover().(string)
		if !ok || !strings.HasPrefix(got, "isolate: unaudited protobuf cache callback ") {
			t.Fatalf("callback got %v", got)
		}
	}()
	runtime.IsolateProtoCacheForTest(new(testProtoCache), func() {
		runtime.IsolateMetadataCallbackForTest(pc)
	})
}
