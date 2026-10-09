// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolate provides the host communication operations available to
// code running inside an isolate. Functions marked //go:isolate are discovered
// by go build and exposed as handles; legacy package main programs remain
// available for runtime probes.
//
// This trusted POC offers opt-in deterministic native dispatch and host
// suspension through Config.Deterministic. Marked builds enforce memory and
// effect restrictions; arbitrary native execution is not contained.
package isolate

import "internal/isolatebridge"

// Call sends one operation to the host and blocks until the host replies.
// The boundary copies payload before handing it to the host and copies the
// response before returning it. The operation number belongs to the
// isolate's SDK; the runtime does not interpret it. An SDK can use an
// operation such as NextRequest to wait for incoming host work.
func Call(op uint32, payload []byte) ([]byte, error) {
	if IsReadOnly() {
		panic("isolate: read-only handlers cannot issue workflow calls")
	}
	return isolatebridge.Current().Call(op, payload)
}

// ReadOnlyCall waits for host work on a dedicated read-only service goroutine.
// After its first reply, allocations use a scratch heap. Existing workflow
// memory can be read but never written, even by callbacks. The goroutine cannot
// start children or perform blocking native channel operations. The host must
// use ResumeReadOnly to admit these requests while workflow dispatch is fenced.
func ReadOnlyCall(op uint32, payload []byte) ([]byte, error) {
	return isolatebridge.Current().ReadOnlyCall(op, payload)
}

// IsReadOnly reports execution in a query or update validator.
func IsReadOnly() bool { return isolatebridge.InReadOnly() }
