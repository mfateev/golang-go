// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

func rejectIsolateProcess(name string) {
	if isolateActive() {
		panic("log/slog." + name + " is unavailable in an isolate")
	}
}

func rejectIsolateDefaultObject(name string) {
	if isolateActive() {
		panic("log/slog default " + name + " is unavailable in an isolate")
	}
}

func (l *Logger) rejectIsolateDefault() {
	if l.processRoot().processDefault.Load() && isolateActive() {
		panic("log/slog default Logger is unavailable in an isolate")
	}
}

func (l *Logger) processRoot() *Logger {
	if l.origin != nil {
		return l.origin
	}
	return l
}
