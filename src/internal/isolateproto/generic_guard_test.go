// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase2b_layout_guard

package isolateproto_test

import (
	"internal/testenv"
	"testing"
)

func TestIsolateGlobalsAcceptExportedGenerics(t *testing.T) {
	const pkg = "internal/isolateproto/testdata/e4genericguard"
	goTool := testenv.GoToolPath(t)
	cmd := testenv.Command(t, goTool, "build", pkg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ordinary build failed: %v\n%s", err, out)
	}

	cmd = testenv.Command(t, goTool, "build", "-gcflags="+pkg+"=-d=isolateglobals=1", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("opt-in build rejected exported generics: %v\n%s", err, out)
	}
}
