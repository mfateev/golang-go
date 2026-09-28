// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_stdlib

package e4base64toy_test

import (
	"encoding/base64"
	"internal/isolateproto/testdata/e4base64toy"
	"testing"
	"unsafe"
)

//go:linkname layoutType encoding/base64.isolateLayoutType
var layoutType unsafe.Pointer

//go:linkname layoutKey encoding/base64.isolateLayoutKey
var layoutKey byte

//go:linkname newPackageBases runtime.isolateE4NewPackageBases
func newPackageBases([]unsafe.Pointer, []unsafe.Pointer) unsafe.Pointer

//go:linkname setPackageBases runtime.isolateE4SetPackageBases
func setPackageBases(unsafe.Pointer) unsafe.Pointer

//go:linkname rerunBase64Init encoding/base64.init
func rerunBase64Init()

func withPackageBases(table unsafe.Pointer, fn func()) {
	old := setPackageBases(table)
	defer setPackageBases(old)
	fn()
}

func newInstance(t *testing.T) unsafe.Pointer {
	t.Helper()
	if layoutType == nil {
		t.Fatal("compiler did not emit the base64 package layout type")
	}
	table := newPackageBases([]unsafe.Pointer{unsafe.Pointer(&layoutKey)}, []unsafe.Pointer{layoutType})
	withPackageBases(table, rerunBase64Init)
	return table
}

func TestStandardLibraryInitializedState(t *testing.T) {
	processStd, _, processRaw, _ := e4base64toy.Snapshot()
	a, b := newInstance(t), newInstance(t)
	var aStd, aURL, aRaw, aRawURL, bStd, bURL, bRaw, bRawURL *base64.Encoding
	withPackageBases(a, func() { aStd, aURL, aRaw, aRawURL = e4base64toy.Snapshot() })
	withPackageBases(b, func() { bStd, bURL, bRaw, bRawURL = e4base64toy.Snapshot() })
	if aStd == bStd || aStd == processStd || bStd == processStd ||
		aURL == bURL || aRaw == bRaw || aRaw == processRaw || bRaw == processRaw ||
		aRawURL == bRawURL {
		t.Fatal("base64 initializer graph is shared across instances or with the process")
	}

	input := []byte{0xfb, 0xef}
	withPackageBases(a, func() {
		e4base64toy.SetStd(aURL)
		if got := e4base64toy.EncodeStd(input); got != "--8=" {
			t.Errorf("instance a standard encoding after change = %q", got)
		}
		if got := e4base64toy.EncodeRawStd(input); got != "++8" {
			t.Errorf("instance a raw standard encoding = %q", got)
		}
	})
	withPackageBases(b, func() {
		if got := e4base64toy.EncodeStd(input); got != "++8=" {
			t.Errorf("instance b standard encoding changed = %q", got)
		}
	})
	if got := e4base64toy.EncodeStd(input); got != "++8=" {
		t.Errorf("process standard encoding changed = %q", got)
	}
}
