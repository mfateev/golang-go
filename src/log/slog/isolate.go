// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slog

import "unsafe"

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

//go:linkname isolateCheckHeapAccess runtime.isolateCheckHeapAccess
//go:noescape
func isolateCheckHeapAccess(unsafe.Pointer, uintptr, bool)

func rejectIsolateProcess(name string) {
	if isolateActive() {
		l := defaultLogger.Load()
		isolateCheckHeapAccess(unsafe.Pointer(l), unsafe.Sizeof(*l), false)
	}
}

func (h *defaultHandler) rejectIsolateDefault() {
	if isolateActive() {
		isolateCheckHeapAccess(unsafe.Pointer(h), unsafe.Sizeof(*h), false)
	}
}
func (w *handlerWriter) rejectIsolateDefault() {
	if isolateActive() {
		isolateCheckHeapAccess(unsafe.Pointer(w), unsafe.Sizeof(*w), false)
	}
}

func (l *Logger) rejectIsolateDefault() {
	if isolateActive() {
		isolateCheckHeapAccess(unsafe.Pointer(l), unsafe.Sizeof(*l), false)
	}
}

func (l *Logger) processRoot() *Logger {
	if l.origin != nil {
		return l.origin
	}
	return l
}

//go:linkname isolateWriteLog runtime.isolateWriteLog
func isolateWriteLog(byte, []byte)
