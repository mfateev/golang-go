// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"unsafe"
)

// releaseLive runs only after the departing G has relinquished its dispatch
// token and wait records and no longer points to this group. Once revoked,
// zero is stable: a new child requires an already attached parent.
func (group *isolateRevocationGroup) releaseLive() {
	live := group.live.Add(-1)
	if live < 0 {
		throw("isolate: negative live goroutine count")
	}
	if live != 0 || group.admission.Load()&isolateRevokedBit == 0 {
		return
	}
	systemstack(func() {
		lock(&group.dispatchLock)
		waiters := group.drainWaiters
		group.drainWaiters = gList{}
		unlock(&group.dispatchLock)
		for gp := waiters.pop(); gp != nil; gp = waiters.pop() {
			ready(gp, 0, false)
		}
	})
}

//go:linkname isolateWaitGroupDrained
func isolateWaitGroupDrained(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || group.admission.Load()&isolateRevokedBit == 0 {
		panic("isolate: drain requires a revoked group and a host caller")
	}
	gopark(isolateDrainCommit, p, waitReasonZero, traceBlockGeneric, 1)
	if raceenabled {
		raceacquire(p)
	}
}

func isolateDrainCommit(gp *g, p unsafe.Pointer) bool {
	group := (*isolateRevocationGroup)(p)
	lock(&group.dispatchLock)
	if group.live.Load() == 0 {
		unlock(&group.dispatchLock)
		return false
	}
	group.drainWaiters.push(gp)
	unlock(&group.dispatchLock)
	return true
}

// The managed lifecycle can start cleanup while still inside an isolate's
// trusted reporting scope. A runtime system G inherits neither the private
// allocator nor the group, and must finish the cleanup callback exactly once.
//
//go:linkname isolateQueueCleanup
func isolateQueueCleanup(fn func()) {
	if fn == nil {
		throw("isolate: nil cleanup callback")
	}
	go isolateCleanupWorker(fn)
}

func isolateCleanupWorker(fn func()) { fn() }

//go:linkname isolateReleaseGroupAlloc
func isolateReleaseGroupAlloc(p unsafe.Pointer) {
	group := (*isolateRevocationGroup)(p)
	if getg().isolateGroup != nil || group.live.Load() != 0 || group.admission.Load()&isolateRevokedBit == 0 {
		throw("isolate: releasing allocator before group cleanup")
	}
	// Private objects remain under ordinary GC. Detaching the acyclic lifetime
	// handle lets GC retire its non-GC cache even if the host retains Isolate.
	group.alloc = nil
	group.randLegacy = nil // The lazily created application generator is private.
}

// Panic diagnostics never invoke application Error or String methods. Such a
// method may panic again, block forever, or perform an effect while reporting.
// Primitive values are formatted; other values report only their dynamic type.
func isolatePanicMessage(value any) string {
	buf := make([]byte, 8<<10)
	systemstack(func() {
		gp := getg()
		gp.writebuf = buf[:0:len(buf)]
		e := efaceOf(&value)
		if e._type == nil {
			print("nil")
		} else {
			kind := e._type.Kind()
			if kind >= abi.Bool && kind <= abi.Complex128 || kind == abi.String {
				printpanicval(value)
			} else {
				print("value of type ", toRType(e._type).string())
			}
		}
		buf = gp.writebuf
		gp.writebuf = nil
	})
	return string(buf)
}

//go:linkname isolateReportPanic
func isolateReportPanic(value any, phase string) {
	isolateReportLifecycleFault("isolate: unrecovered panic", "panic", phase, value)
}

//go:linkname isolateReportGoexit
func isolateReportGoexit(phase string) {
	reason := "isolate: main goroutine exited without returning"
	if phase == "initialization" {
		reason = "isolate: package initializer goroutine exited without returning"
	}
	isolateReportLifecycleFault(reason, "Goexit", phase, nil)
}

//go:linkname isolateLifecycleFaultDetails
func isolateLifecycleFaultDetails(p unsafe.Pointer) (kind, phase, message string) {
	if fault := (*isolateRevocationGroup)(p).ownershipFault.Load(); fault != nil {
		return fault.kind, fault.phase, fault.message
	}
	return "", "", ""
}

// A bounded snapshot does not suspend a running G or stop the world: either
// operation could wait beyond Kill's deadline. Parked/runnable/syscall stacks
// are sampled only after one successful scan-status CAS; busy stacks are skipped.
//
//go:linkname isolateGroupSnapshot
func isolateGroupSnapshot(p unsafe.Pointer) (id uint64, thread int64, stack string) {
	group := (*isolateRevocationGroup)(p)
	buf := make([]byte, 64<<10)
	n := 0
	systemstack(func() {
		ptr, count := atomicAllG()
		for index := uintptr(0); index < count; index++ {
			gp := atomicAllGIndex(ptr, index)
			status := readgstatus(gp)
			if gp.isolateGroup != group {
				continue
			}
			if id == 0 {
				id = gp.goid
				if mp := gp.m; mp != nil {
					thread = int64(mp.procid)
				}
			}
			if status != _Gwaiting && status != _Grunnable && status != _Gsyscall {
				continue
			}
			if !castogscanstatus(gp, status, status|_Gscan) {
				continue
			}
			// Native symbolizers can call application C code. Do not invoke them
			// while producing a deadline-bounded pending-termination diagnostic.
			if mp := gp.m; mp != nil && mp.ncgo > 0 {
				casfrom_Gscanstatus(gp, status|_Gscan, status)
				continue
			}
			if gp.isolateGroup == group {
				id, thread = gp.goid, 0
				if mp := gp.m; mp != nil {
					thread = int64(mp.procid)
				}
				g0 := getg()
				g0.m.traceback = 1
				g0.writebuf = buf[:0:len(buf)]
				goroutineheader(gp)
				traceback(^uintptr(0), ^uintptr(0), 0, gp)
				n = len(g0.writebuf)
				g0.writebuf = nil
				g0.m.traceback = 0
			}
			casfrom_Gscanstatus(gp, status|_Gscan, status)
			if n != 0 {
				break
			}
		}
	})
	return id, thread, string(buf[:n])
}

// Merge all departing members before their race contexts end. The host drain
// acquires the merged clock before releasing private runners and callbacks.
func isolateRaceReleaseCleanup(gp *g) {
	if gp.isolateGroup != nil {
		racereleasemergeg(gp, unsafe.Pointer(gp.isolateGroup))
	}
}
