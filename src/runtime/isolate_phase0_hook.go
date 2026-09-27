//go:build phase0_e5a && linux && arm64

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

var isolatePhase0KillTarget atomic.Pointer[g]
var isolatePhase0KillAck atomic.Uint32
var isolatePhase0NonHeadDetachCount atomic.Uint64

// The signal handler writes this fixed buffer without allocating. State 1
// requests one sample, 2 owns the buffer, and 3 publishes it to the caller.
var isolatePhase0StackTarget atomic.Pointer[g]
var isolatePhase0StackState atomic.Uint32
var isolatePhase0StackPCs [64]uintptr
var isolatePhase0StackN int
var isolatePhase0StackThreadID int64

//go:nowritebarrierrec
func isolatePhase0CaptureSignalStack(gp *g, pc, sp, lr uintptr) {
	if gp == nil || isolatePhase0StackTarget.Load() != gp || !isolatePhase0StackState.CompareAndSwap(1, 2) {
		return
	}
	var u unwinder
	u.initAt(pc, sp, lr, gp, unwindSilentErrors|unwindTrap|unwindJumpStack)
	n := tracebackPCs(&u, 0, isolatePhase0StackPCs[:])
	if n == 0 {
		isolatePhase0StackPCs[0] = pc
		n = 1
	}
	isolatePhase0StackN = n
	isolatePhase0StackThreadID = int64(gp.m.procid)
	isolatePhase0StackState.Store(3)
}

//go:nosplit
func isolatePhase0Kill(gp *g) bool { return isolatePhase0KillTarget.Load() == gp }

func isolatePhase0Terminate(gp *g) {
	if raceenabled {
		if gp.bubble != nil {
			racereleasemergeg(gp, gp.bubble.raceaddr())
		}
		racectxend(gp.racectx)
	}
	trace := traceAcquire()
	if trace.ok() {
		trace.GoEnd()
		traceRelease(trace)
	}
	gdestroy(gp)
	isolatePhase0KillAck.Store(1)
	schedule()
}

// isolatePhase0CleanupDead runs after gdestroy marks the target dead and
// before it puts the G on a reuse list. GC no longer scans its stack here.
func isolatePhase0CleanupDead(gp *g) {
	if isolatePhase0KillTarget.Load() != gp {
		return
	}
	// A preempted arm64 G owns an off-heap register-save block. Ordinary
	// Goexit never runs with that block attached, so gdestroy does not free it.
	if gp.xRegs.state != nil {
		lock(&xRegAlloc.lock)
		xRegAlloc.alloc.free(unsafe.Pointer(gp.xRegs.state))
		gp.xRegs.state = nil
		unlock(&xRegAlloc.lock)
	}
	// The channel-wait probe closes the channel first, which dequeues its
	// one receiver and makes it runnable. The receiver normally releases its
	// sudog after resuming; this path skips that continuation.
	if sg := gp.waiting; sg != nil {
		if sg.isSelect || sg.waitlink != nil || sg.next != nil || sg.prev != nil || sg.elem.get() != nil {
			throw("unsupported channel waiter in isolate Phase 0 kill probe")
		}
		gp.waiting = nil
		gp.activeStackChans = false
		gp.param = nil
		sg.c.set(nil)
		releaseSudog(sg)
	}
	gp.asyncSafePoint = false
}
