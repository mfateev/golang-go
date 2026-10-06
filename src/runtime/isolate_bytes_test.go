// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func TestIsolateHeapBoundarySnapshots(t *testing.T) {
	process := bytes.Repeat([]byte{7, 8}, 4096)
	text := strings.Repeat("host text", 100)
	group, peer := runtime.IsolateMetadataGroupForTest(), runtime.IsolateMetadataGroupForTest()
	owner, peerOwner := nextAllocTestOwner(), nextAllocTestOwner()
	var foreign []byte
	runtime.IsolateMetadataRunForTest(peer, peerOwner, func() { foreign = runtime.IsolateMetadataBytesForTest(8) })
	var err string
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		clone := runtime.IsolateBoundaryBytesForTest(process[7:])
		if !bytes.Equal(clone, process[7:]) || &clone[0] == &process[7] {
			err = "not a byte snapshot"
		}
		if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(&clone[0])); !ok || got != owner {
			err = "byte snapshot has wrong owner"
		}
		copied := runtime.IsolateBoundaryStringForTest(text)
		if copied != text || unsafe.StringData(copied) == unsafe.StringData(text) {
			err = "not a string snapshot"
		}
		if got, ok := runtime.IsolateAllocOriginForTest(unsafe.Pointer(unsafe.StringData(copied))); !ok || got != owner {
			err = "string snapshot has wrong owner"
		}
		if runtime.IsolateBoundaryBytesForTest(nil) != nil || runtime.IsolateBoundaryBytesForTest([]byte{}) == nil {
			err = "nil/empty byte semantics changed"
		}
		defer func() {
			if recover() != "isolate: boundary bytes belong to another instance" {
				err = "peer bytes were accepted"
			}
		}()
		runtime.IsolateBoundaryBytesForTest(foreign)
	})
	if err != "" {
		t.Fatal(err)
	}
	runtime.KeepAlive(peer)
	runtime.KeepAlive(group)
}

func TestIsolateHeapInterfaceCacheOwners(t *testing.T) {
	group, owner := runtime.IsolateMetadataGroupForTest(), nextAllocTestOwner()
	var assertion, switching uintptr
	runtime.IsolateMetadataRunForTest(group, owner, func() {
		assertion, switching = runtime.IsolateInterfaceCacheOwnersForTest()
	})
	if assertion != 0 || switching != 0 {
		t.Fatalf("shared interface cache owners = %d, %d", assertion, switching)
	}
	runtime.KeepAlive(group)
}
