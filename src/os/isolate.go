// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import _ "unsafe" // for go:linkname

//go:linkname runtime_isolateActive runtime.isolateActive
func runtime_isolateActive() bool

//go:linkname runtime_isolateExit runtime.isolateExit
func runtime_isolateExit(int)

func rejectIsolateProcessStart() {
	if runtime_isolateActive() {
		isolateRejectEffect("os.StartProcess")
	}
}

func rejectIsolateEnvironment(name string) {
	if runtime_isolateActive() {
		isolateRejectEffect("os." + name)
	}
}

//go:linkname isolateRejectEffect runtime.isolateRejectEffect
func isolateRejectEffect(string)
