// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency && phase2b_linkcheck

package e4linktoy_test

import (
	"internal/isolateproto/testdata/e4linktoy"
	"testing"
	_ "unsafe"
)

//go:linkname dependencyTask internal/isolateproto/testdata/e4linktoy.isolateDependencyTask
var dependencyTask byte

func TestSelectedDependencyLinked(t *testing.T) {
	if got := e4linktoy.Observed(); got != 41 {
		t.Errorf("process initialized value = %d, want 41", got)
	}
	// Keep the generated dependency record, including its key relocation,
	// reachable in the linked test binary.
	t.Logf("dependency record at %p", &dependencyTask)
}
