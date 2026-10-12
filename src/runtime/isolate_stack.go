// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateMarkServiceGoroutine
func isolateMarkServiceGoroutine() {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateReadOnlyOwner != 0 {
		panic("isolate: marking a service requires a writable isolate goroutine")
	}
	gp.isolateStackService = true
}

// isolateGroupStack is a host diagnostic, separate from the deadline-bounded
// termination snapshot. Like Stack with all=true, it stops the world so every
// workflow goroutine's stack can be read safely, including runnable goroutines.
//
//go:linkname isolateGroupStack
func isolateGroupStack(p unsafe.Pointer, buf []byte) int {
	if isolateActive() {
		isolateRejectEffect("isolate stack trace from isolate code")
	}
	if len(buf) == 0 {
		return 0
	}
	group := (*isolateRevocationGroup)(p)
	stw := stopTheWorld(stwAllGoroutinesStack)
	n := 0
	systemstack(func() {
		g0 := getg()
		g0.m.traceback = 1
		g0.writebuf = buf[:0:len(buf)]
		forEachGRace(func(gp *g) {
			status := readgstatus(gp) &^ _Gscan
			if gp.isolateGroup != group || status == _Gdead || status == _Gdeadextra {
				return
			}
			if gp.isolateReadOnlyService || gp.isolateInboundService || gp.isolateStackService {
				return
			}
			if len(g0.writebuf) != 0 {
				print("\n")
			}
			goroutineheader(gp)
			// Match tracebackothers' treatment of a goroutine leaving a
			// syscall while the world stops; its stack may not be available.
			if gp.m != g0.m && status == _Grunning && gp.syscallsp == 0 {
				print("\tgoroutine running on other thread; stack unavailable\n")
				printcreatedby(gp)
			} else {
				traceback(^uintptr(0), ^uintptr(0), 0, gp)
			}
		})
		n = len(g0.writebuf)
		g0.writebuf = nil
		g0.m.traceback = 0
	})
	startTheWorld(stw)
	return n
}
