// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

// The implicit standard streams are process resources. Explicit readers and
// writers remain the caller's responsibility until an effect policy exists.
func rejectIsolateStandardIO() {
	if isolateActive() {
		panic("fmt: standard input and output are unavailable inside an isolate")
	}
}
