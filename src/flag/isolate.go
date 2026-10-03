// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flag

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

// originalCommandLine also guards an alias retained after CommandLine changes.
var originalCommandLine *FlagSet

func rejectIsolateCommandLine() {
	if isolateActive() {
		panic("flag.CommandLine is unavailable in an isolate")
	}
}

func defaultFlagSet() *FlagSet {
	rejectIsolateCommandLine()
	return CommandLine
}

func (f *FlagSet) rejectIsolateCommandLine() {
	if isolateActive() && (f == CommandLine || f == originalCommandLine) {
		panic("flag.CommandLine is unavailable in an isolate")
	}
}
