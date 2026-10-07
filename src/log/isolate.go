// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package log

import (
	"os"
	"unsafe"
)

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

//go:linkname isolateCheckHeapAccess runtime.isolateCheckHeapAccess
//go:noescape
func isolateCheckHeapAccess(unsafe.Pointer, uintptr, bool)

type isolateLogWriter struct{}

//go:linkname isolateWriteLog runtime.isolateWriteLog
func isolateWriteLog(byte, []byte)

func (isolateLogWriter) Write(p []byte) (int, error) {
	isolateWriteLog(1, p)
	return len(p), nil
}

func newStandardLogger() *Logger {
	if isolateActive() {
		return New(isolateLogWriter{}, "", LstdFlags|LUTC)
	}
	return New(os.Stderr, "", LstdFlags)
}

func (l *Logger) rejectIsolateStandard(name string) {
	if isolateActive() {
		isolateCheckHeapAccess(unsafe.Pointer(l), unsafe.Sizeof(*l), false)
	}
}
