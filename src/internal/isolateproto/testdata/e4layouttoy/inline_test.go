// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_layout_inline

package e4layouttoy_test

import (
	"internal/isolateproto/testdata/e4layouttoy"
	"testing"
)

func TestGeneratedLayoutCrossPackageInline(t *testing.T) {
	state := newInstance(t)
	withBase(state, func() {
		e4layouttoy.SetEpoch(7)
		if got := e4layouttoy.RunCurrent(); got != "1/1/1/7" {
			t.Fatalf("cross-package global write: RunCurrent=%q", got)
		}
	})
}
