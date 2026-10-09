// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import "unsafe"

// LogOp is reserved for one-way observational output.
const LogOp uint32 = 0xffff0001

func (b *Boundary) ConfigureLogging() bool { return setLogTransport(b.group, b.writeLog) }

//go:linkname setLogTransport runtime.isolateSetLogTransport
func setLogTransport(unsafe.Pointer, func(byte, []byte)) bool

func (b *Boundary) writeLog(source byte, message []byte) {
	payload := make([]byte, 1+len(message))
	payload[0] = source
	copy(payload[1:], message)
	b.Write(LogOp, payload)
}
