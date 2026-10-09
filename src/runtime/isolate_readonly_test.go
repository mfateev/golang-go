// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestIsolateReadOnlyBorrow(t *testing.T) {
	group := runtime.IsolateMetadataGroupForTest()
	owner, scratch := nextAllocTestOwner(), nextAllocTestOwner()
	var state []byte
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		state = runtime.IsolateMetadataBytesForTest(1024)
		state[17] = 99
	})
	runtime.IsolateReadOnlyRunForTest(group, owner, scratch, func() {
		runtime.IsolateHeapAccessForTest(unsafe.Pointer(&state[17]), 1, false)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("read-only write did not panic")
				}
			}()
			runtime.IsolateHeapAccessForTest(unsafe.Pointer(&state[17]), 1, true)
		}()
		local := runtime.IsolateMetadataBytesForTest(1024)
		runtime.IsolateHeapAccessForTest(unsafe.Pointer(&local[17]), 1, true)
		if got, _ := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&local[17])); got != scratch {
			t.Error("read-only allocation used workflow owner")
		}
	})
	// Rejecting a query write must leave the ordinary workflow owner usable.
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		runtime.IsolateHeapAccessForTest(unsafe.Pointer(&state[17]), 1, true)
		state[17]++
	})
	if state[17] != 100 {
		t.Fatal("workflow did not continue after the rejected write")
	}
	runtime.KeepAlive(group)
}

func TestIsolateReadOnlyCacheRetirement(t *testing.T) {
	const count = 64
	owners := make([][2]uintptr, count)
	values := make([][]byte, count)
	for i := range owners {
		owners[i] = [2]uintptr{nextAllocTestOwner(), nextAllocTestOwner()}
		values[i] = readOnlyCacheCycle(owners[i][0], owners[i][1])
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		present := false
		for _, pair := range owners {
			present = runtime.IsolateCachePresentForTest(pair[0]) || runtime.IsolateCachePresentForTest(pair[1]) || present
		}
		if !present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("query/workflow cache survived cyclic instance collection")
		}
		runtime.Gosched()
	}
	for i, value := range values {
		if got, _ := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&value[17])); got != owners[i][1] || value[17] != 99 {
			t.Fatal("retiring the query cache damaged a live object")
		}
	}
	runtime.KeepAlive(values)
}

//go:noinline
func readOnlyCacheCycle(owner, scratch uintptr) []byte {
	group := runtime.IsolateMetadataGroupForTest()
	runtime.IsolateMetadataSetExitForTest(group, func(int) { runtime.KeepAlive(group) })
	runtime.IsolateMetadataRunForTest(group, owner, func() { _ = runtime.IsolateMetadataBytesForTest(1024) })
	var value []byte
	runtime.IsolateReadOnlyRunForTest(group, owner, scratch, func() {
		value = runtime.IsolateMetadataBytesForTest(1024)
		value[17] = 99
	})
	return value
}
