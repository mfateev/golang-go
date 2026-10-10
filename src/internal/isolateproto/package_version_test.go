// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolateproto

import (
	"strings"
	"testing"
	"unsafe"
)

func TestPackageMetadataVersionBeforeLayout(t *testing.T) {
	// Supply just the fixed prefix. A mismatched descriptor must be rejected
	// before reading string headers, GC types, dependency records or init tasks.
	for _, version := range []uint64{0, 2, ^uint64(0)} {
		_, err := NewPackageInstance([]unsafe.Pointer{unsafe.Pointer(&version)})
		if err == nil || !strings.Contains(err.Error(), "incompatible package metadata version") {
			t.Fatalf("version %d: %v", version, err)
		}
	}
}
