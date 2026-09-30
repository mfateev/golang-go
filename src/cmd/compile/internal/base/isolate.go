// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import "strings"

var isolateSelectedPackages map[string]bool
var isolateSelectedImports map[string]bool
var isolateEntrySkip map[string]bool

// InitIsolatePackageSelection establishes the same package selection in each
// compiler invocation. It runs before loading export data, so the local
// package's layout and initializer modes also apply to exported declarations.
func InitIsolatePackageSelection() {
	if Debug.IsolatePackages != "" {
		isolateSelectedPackages = make(map[string]bool)
		for _, path := range strings.Split(Debug.IsolatePackages, ":") {
			if path == "" {
				Fatalf("isolate: empty package path in isolatepackages")
			}
			isolateSelectedPackages[path] = true
		}
		if isolateSelectedPackages[Ctxt.Pkgpath] {
			Debug.IsolateGlobals = 1
			Debug.IsolateInit = 1
		}
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
