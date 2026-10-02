// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syscall

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

func rejectIsolateProcessControl(name string) {
	if isolateActive() {
		panic("syscall." + name + " is unavailable in an isolate")
	}
}

func rejectIsolateEnvironment(name string) {
	if isolateActive() {
		panic("syscall." + name + " is unavailable in an isolate")
	}
}
