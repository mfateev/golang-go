// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/sys"
	"unsafe"
)

func newIsolateCoro(f func(*coro)) *coro {
	c := &coro{
		f:              f,
		isolateGroup:   getg().isolateGroup,
		isolateRequest: make(chan struct{}),
		isolateReply:   make(chan struct{}),
	}
	caller := getg()
	pc := sys.GetCallerPC()
	// Initialize every field before making the child runnable. In particular,
	// the caller may invoke next before the child has received its first token.
	systemstack(func() {
		start := corostart
		startfv := *(**funcval)(unsafe.Pointer(&start))
		gp := newproc1(startfv, caller, pc, true, waitReasonCoroutine)
		gp.coroarg = c
		c.isolateRunner = gp
		ready(gp, 0, false)
	})
	return c
}

func isolateCorostart(c *coro) {
	<-c.isolateRequest
	// The iterator package recovers panics and marks Goexit before returning
	// control to next/stop. Revocation discards this defer together with the
	// caller; it must not execute a handshake after the group has been killed.
	defer func() { c.isolateReply <- struct{}{} }()
	c.f(c)
}

func isolateCoroSwitch(c *coro) {
	gp := getg()
	if c.isolateGroup != gp.isolateGroup {
		isolateOwnershipViolation("isolate: coroutine crosses owner boundary")
	}
	if gp == c.isolateRunner {
		c.isolateReply <- struct{}{}
		<-c.isolateRequest
	} else {
		c.isolateRequest <- struct{}{}
		<-c.isolateReply
	}
}
