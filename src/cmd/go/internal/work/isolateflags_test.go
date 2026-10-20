// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"slices"
	"testing"

	"cmd/go/internal/load"
)

func TestIsolateCompilerFlags(t *testing.T) {
	previous := forcedGcflags
	t.Cleanup(func() { forcedGcflags = previous })
	leaf := &load.Package{PackagePublic: load.PackagePublic{ImportPath: "example.com/leaf"}}
	shared := &load.Package{PackagePublic: load.PackagePublic{ImportPath: "example.com/shared"}}
	shared.Internal.Imports = []*load.Package{leaf}
	root := &load.Package{PackagePublic: load.PackagePublic{ImportPath: "example.com/root"}}
	root.Internal.Imports = []*load.Package{shared}

	forcedGcflags = []string{"-race", "-d=isolateeffects=1", "-d=isolatepackages=example.com/leaf:example.com/root:example.com/shared", "-d=isolateeffectentries=example.com/root.Entry"}
	baseline := compilerForcedFlags(shared)
	if !slices.Contains(baseline, "-d=isolatebuild=1") || !slices.Contains(baseline, "-d=isolatepackages=example.com/leaf:example.com/shared") {
		t.Fatalf("shared flags: %q", baseline)
	}
	forcedGcflags = []string{"-race", "-d=isolateeffects=1", "-d=isolateeffects=1", "-d=isolatepackages=example.com/old", "-d=isolatepackages=example.com/leaf:example.com/other:example.com/shared", "-d=isolateeffectentries=example.com/old.Entry", "-d=isolateeffectentries=example.com/other.Entry"}
	if got := compilerForcedFlags(shared); !slices.Equal(got, baseline) {
		t.Fatalf("unrelated program invalidated shared flags:\n%q\n%q", baseline, got)
	}

	forcedGcflags = []string{"-d=isolatepackages=example.com/root", "-d=isolateeffectentries=example.com/root.Entry"}
	if got := compilerForcedFlags(leaf); !slices.Equal(got, []string{"-d=isolatebuild=1"}) {
		t.Fatalf("process-owned leaf lost mandatory checks: %q", got)
	}
	if got := compilerForcedFlags(root); !slices.Contains(got, "-d=isolateeffectentries=example.com/root.Entry") {
		t.Fatalf("root lost entry diagnostics: %q", got)
	}

	forcedGcflags = []string{"-d=isolatepackages=example.com/shared", "-d=isolateeffectentries=example.com/leaf.Entry"}
	processLeaf := compilerForcedFlags(shared)
	if !slices.Contains(processLeaf, "-d=isolateeffectentries=example.com/leaf.Entry") {
		t.Fatalf("transitive inlined entry lost diagnostics: %q", processLeaf)
	}
	forcedGcflags = []string{"-d=isolatepackages=example.com/leaf:example.com/shared", "-d=isolateeffectentries=example.com/leaf.Entry"}
	if slices.Equal(compilerForcedFlags(shared), processLeaf) {
		t.Fatal("different reachable ownership layouts have the same flags")
	}

	forcedGcflags = []string{"-d=isolateauditedpackages=example.com/shared:example.com/unrelated", "-d=isolateauditedfiles=example.com/leaf/model.pb.go:example.com/unrelated/model.pb.go"}
	got := compilerForcedFlags(shared)
	want := []string{"-d=isolateauditedpackages=example.com/shared", "-d=isolateauditedfiles=example.com/leaf/model.pb.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("audit flags not scoped/canonical: %q", got)
	}

	forcedGcflags = []string{"-race", "-d=checkptr=1"}
	if got := compilerForcedFlags(root); !slices.Equal(got, forcedGcflags) {
		t.Fatalf("ordinary build flags changed: %q", got)
	}
}
