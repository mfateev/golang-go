// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

//go:linkname isolateRejectEffect runtime.isolateRejectEffect
func isolateRejectEffect(string)

// The implicit standard input is a process resource. Explicit reader and
// writer calls also obey the compiler/runtime effect policy.
func rejectIsolateStandardIO() {
	if isolateActive() {
		isolateRejectEffect("fmt standard input")
	}
}

//go:linkname isolateWriteLog runtime.isolateWriteLog
func isolateWriteLog(byte, []byte)

func writeIsolateLog(source byte, p []byte) (int, error) {
	isolateWriteLog(source, p)
	return len(p), nil
}
