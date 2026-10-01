// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolate provides the host communication operations available to
// code running inside an isolate. An isolate program is an ordinary package
// main with an ordinary func main.
//
// The native runtime boundary is still under construction. The current
// transport is a trusted Phase 2B probe; it does not provide heap isolation or
// deterministic scheduling.
package isolate

import "internal/isolatebridge"

// Call sends one operation to the host and blocks until the host replies.
// The boundary copies payload before handing it to the host and copies the
// response before returning it. The operation number belongs to the
// isolate's SDK; the runtime does not interpret it. An SDK can use an
// operation such as NextRequest to wait for incoming host work.
func Call(op uint32, payload []byte) ([]byte, error) {
	return isolatebridge.Current().Call(op, payload)
}
