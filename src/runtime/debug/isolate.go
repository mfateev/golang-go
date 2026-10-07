// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debug

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

func rejectIsolate(name string) {
	if isolateActive() {
		isolateRejectEffect("runtime/debug." + name)
	}
}

//go:linkname isolateRejectEffect runtime.isolateRejectEffect
func isolateRejectEffect(string)
