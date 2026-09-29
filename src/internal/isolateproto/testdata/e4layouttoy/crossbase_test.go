// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_crossbase

package e4layouttoy_test

import (
	"encoding/base64"
	"testing"
)

func TestSingleBaseCannotAddressImportedPackage(t *testing.T) {
	state := newInstance(t)
	withBase(state, func() {
		defer func() {
			if got := recover(); got == nil {
				t.Error("imported global used the single-package base")
			}
		}()
		if base64.StdEncoding == nil {
			t.Error("unexpected nil base64 encoding")
		}
	})
}
