// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase2b_layout_guard

package isolateproto_test

import (
	"internal/testenv"
	"strings"
	"testing"
)

func TestIsolateGlobalsRejectExportedGenerics(t *testing.T) {
	const pkg = "internal/isolateproto/testdata/e4genericguard"
	goTool := testenv.GoToolPath(t)
	cmd := testenv.Command(t, goTool, "build", pkg)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ordinary build failed: %v\n%s", err, out)
	}

	cmd = testenv.Command(t, goTool, "build", "-gcflags="+pkg+"=-d=isolateglobals=1", pkg)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("opt-in build accepted exported generics:\n%s", out)
	}
	if n := strings.Count(string(out), "exported generic function or method is unsupported with isolate globals"); n != 2 {
		t.Fatalf("got %d guard diagnostics, want 2:\n%s", n, out)
	}
}
