// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

var standardLogger *Logger

func init() {
	standardLogger = std
}

func (l *Logger) rejectIsolateStandard(name string) {
	if l == standardLogger && isolateActive() {
		panic("log." + name + " is unavailable in an isolate")
	}
}
