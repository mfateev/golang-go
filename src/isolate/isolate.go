// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolate provides the host communication operations available to
// code running inside an isolate. An isolate program is an ordinary package
// main with an ordinary func main.
//
// The native runtime boundary is still under construction. The current hooks
// panic because the runtime cannot yet associate a goroutine with an active
// isolate and its owned host command queue.
package isolate

import _ "unsafe" // for go:linkname

// Call sends one operation to the host and blocks until the host replies.
// The runtime copies payload before handing it to the host and copies the
// response before returning it to the isolate. The operation number belongs
// to the isolate's SDK; the runtime does not interpret it.
func Call(op uint32, payload []byte) ([]byte, error) {
	return runtimeCall(op, payload)
}

// Inbox receives messages pushed by the host, including the initial input.
// Messages are copied into isolate-owned memory before delivery.
func Inbox() <-chan []byte {
	return runtimeInbox()
}

//go:linkname runtimeCall runtime.isolateCall
func runtimeCall(op uint32, payload []byte) ([]byte, error)

//go:linkname runtimeInbox runtime.isolateInbox
func runtimeInbox() <-chan []byte
