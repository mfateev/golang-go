// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_stdlib

package e4base64toy_test

import (
	"encoding/base64"
	"internal/isolateproto"
	"internal/isolateproto/testdata/e4base64toy"
	"testing"
	"unsafe"
)

//go:linkname layoutType encoding/base64.isolateLayoutType
var layoutType unsafe.Pointer

//go:linkname layoutKey encoding/base64.isolateLayoutKey
var layoutKey byte

//go:linkname base64InitTask encoding/base64.isolateInitTask
var base64InitTask byte

func newInstance(t *testing.T) *isolateproto.PackageInstance {
	t.Helper()
	if layoutType == nil {
		t.Fatal("compiler did not emit the base64 package layout type")
	}
	instance, err := isolateproto.NewPackageInstance([]isolateproto.PackageInitSpec{{
		Path:     "encoding/base64",
		Key:      unsafe.Pointer(&layoutKey),
		Type:     layoutType,
		InitTask: unsafe.Pointer(&base64InitTask),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestStandardLibraryInitializedState(t *testing.T) {
	processStd, _, processRaw, _ := e4base64toy.Snapshot()
	a, b := newInstance(t), newInstance(t)
	var aStd, aURL, aRaw, aRawURL, bStd, bURL, bRaw, bRawURL *base64.Encoding
	a.Run(func() { aStd, aURL, aRaw, aRawURL = e4base64toy.Snapshot() })
	b.Run(func() { bStd, bURL, bRaw, bRawURL = e4base64toy.Snapshot() })
	if aStd == bStd || aStd == processStd || bStd == processStd ||
		aURL == bURL || aRaw == bRaw || aRaw == processRaw || bRaw == processRaw ||
		aRawURL == bRawURL {
		t.Fatal("base64 initializer graph is shared across instances or with the process")
	}

	input := []byte{0xfb, 0xef}
	a.Run(func() {
		e4base64toy.SetStd(aURL)
		if got := e4base64toy.EncodeStd(input); got != "--8=" {
			t.Errorf("instance a standard encoding after change = %q", got)
		}
		if got := e4base64toy.EncodeRawStd(input); got != "++8" {
			t.Errorf("instance a raw standard encoding = %q", got)
		}
	})
	b.Run(func() {
		if got := e4base64toy.EncodeStd(input); got != "++8=" {
			t.Errorf("instance b standard encoding changed = %q", got)
		}
	})
	if got := e4base64toy.EncodeStd(input); got != "++8=" {
		t.Errorf("process standard encoding changed = %q", got)
	}
}
