// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"internal/buildcfg"
	"strings"
)

var isolateSelectedPackages map[string]bool
var isolateSelectedImports map[string]bool
var isolateEntrySkip map[string]bool

// IsolateBuildEnabled reports whether compulsory isolate checks are enabled.
func IsolateBuildEnabled() bool {
	return Debug.IsolateBuild != 0 || Debug.IsolatePackages != ""
}

// InitIsolatePackageSelection establishes the relevant package selection for
// each compiler invocation. It runs before loading export data, so the local
// package's layout and initializer modes also apply to exported declarations.
func InitIsolatePackageSelection() {
	if (IsolateBuildEnabled() || Debug.IsolateGlobals != 0 || Debug.IsolateInit != 0 || Debug.IsolateHeap != 0 || Debug.IsolateEffects != 0 || Debug.IsolateMetadata != 0) && !buildcfg.Experiment.Isolates {
		Fatalf("isolate instrumentation requires GOEXPERIMENT=isolates")
	}
	if IsolateBuildEnabled() {
		// Checks remain compulsory when this package and all its dependencies
		// are process-owned. An empty state selection grants no memory privilege.
		Debug.IsolateHeap = 2
		Debug.IsolateMetadata = 1
		Debug.IsolateEffects = 1
		isolateSelectedPackages = make(map[string]bool)
		if Debug.IsolatePackages != "" {
			for _, path := range strings.Split(Debug.IsolatePackages, ":") {
				if path == "" {
					Fatalf("isolate: empty package path in isolatepackages")
				}
				isolateSelectedPackages[path] = true
			}
		}
		if isolateSelectedPackages[Ctxt.Pkgpath] {
			Debug.IsolateGlobals = 1
			Debug.IsolateInit = 1
		}
	}
	if Flag.Std && isolateRuntimeImplementation(Ctxt.Pkgpath) {
		Debug.IsolateHeap = 0
	}
	if Debug.IsolateImports != "" {
		isolateSelectedImports = make(map[string]bool)
		for _, path := range strings.Split(Debug.IsolateImports, ",") {
			if path == "" {
				Fatalf("isolate: empty package path in isolateimports")
			}
			isolateSelectedImports[path] = true
		}
	}
	if Debug.IsolateEntrySkip != "" {
		isolateEntrySkip = make(map[string]bool)
		for _, path := range strings.Split(Debug.IsolateEntrySkip, ":") {
			if path == "" {
				Fatalf("isolate: empty package path in isolateentryskip")
			}
			if !isolateSelectedPackages[path] {
				Fatalf("isolate: startup-skipped package %q is not selected", path)
			}
			isolateEntrySkip[path] = true
		}
	}
}

// IsolateImportSelected reports whether a direct import's globals must use
// its selected layout in this compilation. IsolateImports is retained for
// the earlier single-import probe.
func IsolateImportSelected(path string) bool {
	return isolateSelectedPackages[path] || isolateSelectedImports[path]
}

// IsolateEntrySkip reports whether a generated process entry omits this
// import's startup initializer. Selected packages used by the host retain
// process initialization as well as per-instance initialization.
func IsolateEntrySkip(path string) bool {
	return isolateEntrySkip[path]
}

// These GOROOT implementations own the allocator, scheduler, compiler ABI,
// sanitizer hooks and copied-byte transport. They enforce their own contracts
// and cannot call application instrumentation while running without a P or on
// an unmapped system stack. No external module inherits this exemption.
func isolateRuntimeImplementation(path string) bool {
	if path == "runtime" || strings.HasPrefix(path, "internal/runtime/") {
		return true
	}
	switch path {
	case "runtime/cgo", "runtime/race", "runtime/asan", "runtime/msan",
		"internal/abi", "internal/goarch", "internal/goos", "internal/cpu", "internal/bytealg",
		"internal/race", "internal/asan", "internal/msan", "internal/coverage/rtcov",
		"internal/isolatebridge", "internal/isolateproto", "isolate":
		return true
	}
	return false
}
