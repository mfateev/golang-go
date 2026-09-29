// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import _ "unsafe" // for go:linkname

// These hooks reserve the package isolate to runtime boundary seam. The
// native isolate scheduler will replace the fail-closed bodies when it can
// associate a running goroutine with an isolate and its host command queue.

//go:linkname isolateCall
func isolateCall(op uint32, payload []byte) ([]byte, error) {
	panic("isolate: Call outside an active isolate")
}

//go:linkname isolateInbox
func isolateInbox() <-chan []byte {
	panic("isolate: Inbox outside an active isolate")
}
