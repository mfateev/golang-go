// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency && phase2b_autoselection

package e4importtoy_test

import (
	"internal/isolateproto/testdata/e4deptoy"
	"testing"
)

// This external test package is not selected for its own layout. Its direct
// accesses to an opted-in dependency must still follow the selected base.
func TestBuildWidePackageSelection(t *testing.T) {
	a, b := newInstance(t), newInstance(t)
	a.Run(func() {
		if got := e4deptoy.Exported; got != 41 {
			t.Fatalf("first instance Exported=%d, want 41", got)
		}
		e4deptoy.Exported = 42
	})
	b.Run(func() {
		if got := e4deptoy.Exported; got != 41 {
			t.Fatalf("second instance Exported=%d, want 41", got)
		}
	})
	a.Run(func() {
		if got := e4deptoy.Exported; got != 42 {
			t.Fatalf("first instance Exported=%d, want 42", got)
		}
	})
	if got := e4deptoy.Exported; got != 41 {
		t.Fatalf("process Exported=%d, want 41", got)
	}
}
