// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase2b_revocation

package runtime

import "unsafe"

func IsolateTestNewGroup() unsafe.Pointer {
	return unsafe.Pointer(new(isolateRevocationGroup))
}

func IsolateTestEnterGroup(p unsafe.Pointer) {
	gp := getg()
	if gp.isolateGroup != nil {
		throw("isolate: test goroutine already belongs to a group")
	}
	group := (*isolateRevocationGroup)(p)
	gp.isolateGroup = group
	group.live.Add(1)
}

func IsolateTestLeaveGroup() {
	gp := getg()
	group := gp.isolateGroup
	if group == nil {
		throw("isolate: test goroutine has no group")
	}
	gp.isolateGroup = nil
	group.live.Add(-1)
}

func IsolateTestRevokeGroup(p unsafe.Pointer) {
	(*isolateRevocationGroup)(p).revoked.Store(true)
}

func IsolateTestGroupLive(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).live.Load()
}

func IsolateTestNoPreempt(fn func()) {
	mp := acquirem()
	fn()
	releasem(mp)
}
