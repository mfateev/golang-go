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
	return isolateDiagnosticStack(p, buf, false)
}

// Task diagnostics include the whole isolate, the calling host task goroutine
// and its Suspend helper. They are a scope snapshot, not a dependency graph:
// a GC worker or process lock owner outside this scope is not included.
//
//go:linkname isolateTaskStack
func isolateTaskStack(p unsafe.Pointer, buf []byte) int {
	return isolateDiagnosticStack(p, buf, true)
}

func isolateDiagnosticStack(p unsafe.Pointer, buf []byte, task bool) int {
	if isolateActive() {
		isolateRejectEffect("isolate stack trace from isolate code")
	}
	if len(buf) == 0 {
		return 0
	}
	group := (*isolateRevocationGroup)(p)
	caller := getg()
	var queueIDs []uint64
	if task {
		// Allocate before stopping the world or taking the leaf dispatcher lock.
		lock(&group.dispatchLock)
		queued := int(group.dispatchQueue.size) + int(group.dispatchPriorityQueue.size)
		unlock(&group.dispatchLock)
		queueIDs = make([]uint64, max(queued, int(group.live.Load()), 16))
	}
	stw := stopTheWorld(stwAllGoroutinesStack)
	n := 0
	systemstack(func() {
		g0 := getg()
		g0.m.traceback = 1
		g0.writebuf = buf[:0:len(buf)]
		var suspender guintptr
		if task {
			lock(&group.dispatchLock)
			token, waiter := group.dispatchToken, group.dispatchWaiter
			suspender = group.dispatchSuspender
			paused, joining := group.dispatchPaused, group.dispatchJoining
			readOnly, inbound, priority := group.dispatchReadOnly, group.dispatchInbound, group.dispatchPriority
			normalCount, priorityCount := int(group.dispatchQueue.size), int(group.dispatchPriorityQueue.size)
			count := normalCount + priorityCount
			queueConsistent := true
			if count <= len(queueIDs) {
				i := 0
				for _, queue := range [2]gQueue{group.dispatchQueue, group.dispatchPriorityQueue} {
					gp := queue.head
					for remaining := queue.size; remaining > 0; remaining-- {
						if gp != 0 {
							queueIDs[i] = gp.ptr().goid
							gp = gp.ptr().schedlink
						} else {
							queueConsistent = false
						}
						i++
					}
					if gp != 0 {
						queueConsistent = false
					}
				}
			}
			unlock(&group.dispatchLock)
			if count > len(queueIDs) {
				// Membership grew while preparing the buffer. Ask the host to
				// retry rather than omit members of either runnable queue.
				n = len(buf)
				g0.writebuf = nil
				g0.m.traceback = 0
				return
			}
			phase := "normal"
			if readOnly {
				phase = "read-only"
			} else if inbound {
				phase = "inbound"
			} else if priority {
				phase = "priority"
			}
			print("isolate dispatch: phase=", phase, " paused=", paused, " joining=", joining, " token=", isolateDiagnosticGID(token), " suspension_waiter=", isolateDiagnosticGID(waiter), " suspend_caller=", isolateDiagnosticGID(suspender), " host_task=", caller.goid, "\n")
			print("normal_queue=[")
			for i, id := range queueIDs[:normalCount] {
				if i != 0 {
					print(",")
				}
				print(id)
			}
			print("] priority_queue=[")
			for i, id := range queueIDs[normalCount:count] {
				if i != 0 {
					print(",")
				}
				print(id)
			}
			print("] queue_consistent=", queueConsistent, "\n")
		}
		forEachGRace(func(gp *g) {
			status := readgstatus(gp) &^ _Gscan
			if status == _Gdead || status == _Gdeadextra {
				return
			}
			if gp.isolateGroup != group && (!task || gp != caller && gp != suspender.ptr()) {
				return
			}
			if !task && (gp.isolateReadOnlyService || gp.isolateInboundService || gp.isolateStackService) {
				return
			}
			if len(g0.writebuf) != 0 {
				print("\n")
			}
			goroutineheader(gp)
			if task && gp.isolateGroup == group {
				print("\tisolate: inbound=", gp.isolateInboundService, " read_only=", gp.isolateReadOnlyService, " service=", gp.isolateStackService, " priority=", gp.isolatePriority, " queued=", gp.isolateDispatchQueued, " park=", gp.isolateDispatchPark, " metadata_depth=", gp.isolateMetadataDepth, " runtime_wait=", gp.isolateRuntimeWait, "\n")
			}
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

func isolateDiagnosticGID(gp guintptr) uint64 {
	if gp == 0 {
		return 0
	}
	return gp.ptr().goid
}
