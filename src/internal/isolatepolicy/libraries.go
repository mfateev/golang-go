// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatepolicy

// FrozenLibraryPackage enumerates GOROOT packages whose initialized backing
// data is immutable. Isolates still receive private copies of their global
// bindings. Never extend this list to a package containing mutable caches,
// pools, or callbacks without auditing its complete initialized graph.
func FrozenLibraryPackage(path string) bool {
	return path == "unicode" || path == "regexp/syntax" || path == "strconv"
}
