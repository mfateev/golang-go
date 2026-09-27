// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e5a && linux && arm64

package runtime

import "unsafe"

var isolatePhase0Target *g

// IsolatePhase0RegisterTarget must be called by a user-code loop before the
// test attempts to suspend it. A loop inside package runtime is deliberately
// excluded from asynchronous preemption.
func IsolatePhase0RegisterTarget() { isolatePhase0Target = getg() }

func IsolatePhase0NonHeadDetachCount() uint64 { return isolatePhase0NonHeadDetachCount.Load() }

func IsolatePhase0RaceEnabled() bool { return raceenabled }

// IsolatePhase0CaptureTargetStack asks the registered G's OS thread for one
// signal-context stack sample. A missed deadline returns an empty sample.
// This test-only path never suspends the target or waits for a Go safe point.
func IsolatePhase0CaptureTargetStack(maxNanos int64) (pcs []uintptr, threadID int64) {
	gp := isolatePhase0Target
	if gp == nil || maxNanos <= 0 {
		return nil, 0
	}
	isolatePhase0StackTarget.StoreNoWB(gp)
	isolatePhase0StackState.Store(1)
	start, nextSignal := nanotime(), int64(0)
	for isolatePhase0StackState.Load() != 3 {
		now := nanotime()
		if now-start >= maxNanos {
			isolatePhase0StackTarget.StoreNoWB(nil)
			return nil, 0
		}
		if now >= nextSignal {
			if mp := gp.m; mp != nil {
				signalM(mp, sigPreempt)
			}
			nextSignal = now + 1000*1000
		}
		Gosched()
	}
	isolatePhase0StackTarget.StoreNoWB(nil)
	return append([]uintptr(nil), isolatePhase0StackPCs[:isolatePhase0StackN]...), isolatePhase0StackThreadID
}

// IsolatePhase0CondWaiterCount observes the registered waiter's notify list.
// It is only for arranging the test's ticket order before a kill request.
func IsolatePhase0CondWaiterCount() int {
	gp := isolatePhase0Target
	if gp == nil || gp.waitreason != waitReasonSyncCondWait || gp.waiting == nil || gp.waiting.elem.get() == nil {
		return 0
	}
	l := (*notifyList)(gp.waiting.elem.get())
	lockWithRank(&l.lock, lockRankNotifyList)
	count := 0
	for s := l.head; s != nil; s = s.next {
		count++
	}
	unlock(&l.lock)
	return count
}

// IsolateSuspendLatencyPhase0 measures safe-point suspension of a busy user G.
// It is test-only and intentionally makes no termination claim.
func IsolateSuspendLatencyPhase0() (elapsed int64, wasRunning, wasWaiting bool) {
	gp := isolatePhase0Target
	systemstack(func() {
		me := getg().m.curg
		casGToWaitingForSuspendG(me, _Grunning, waitReasonTraceGoroutineStatus)
		status := readgstatus(gp) &^ _Gscan
		wasRunning = status == _Grunning
		wasWaiting = status == _Gwaiting
		start := nanotime()
		state := suspendG(gp)
		elapsed = nanotime() - start
		resumeG(state)
		casgstatus(me, _Gwaiting, _Grunning)
	})
	return
}

// IsolateKillRegisteredPhase0 asks the experimental scheduler hook to discard
// the registered user G. It covers a running loop only.
func IsolateKillRegisteredPhase0() (elapsed int64, dead bool) {
	return isolateKillRegisteredPhase0(false)
}

// IsolateKillChannelWaitPhase0 wakes a single channel receiver by closing its
// channel, then terminates it before its waiting continuation can run.
func IsolateKillChannelWaitPhase0() (elapsed int64, dead bool) {
	return isolateKillRegisteredPhase0(true)
}

func isolateKillRegisteredPhase0(closeWait bool) (elapsed int64, dead bool) {
	gp := isolatePhase0Target
	isolatePhase0KillAck.Store(0)
	isolatePhase0KillTarget.StoreNoWB(gp)
	start := nanotime()
	var ch *hchan
	var detachedWait *sudog
	systemstack(func() {
		me := getg().m.curg
		casGToWaitingForSuspendG(me, _Grunning, waitReasonTraceGoroutineStatus)
		state := suspendG(gp)
		if state.dead {
			dead = true
		} else {
			if closeWait && gp.waiting != nil {
				ch = gp.waiting.c.get()
			} else if !closeWait && gp.waitreason == waitReasonSyncCondWait && gp.waiting != nil && gp.waiting.elem.get() != nil {
				// A sole waiter or the newest ticket can be removed without
				// leaving a hole that a later Signal would consume.
				l := (*notifyList)(gp.waiting.elem.get())
				lockWithRank(&l.lock, lockRankNotifyList)
				var previous *sudog
				for s := l.head; s != nil && s != gp.waiting; s = s.next {
					previous = s
				}
				if l.tail == gp.waiting && (l.head == gp.waiting || previous != nil && previous.next == gp.waiting) &&
					!less(gp.waiting.ticket, l.notify) &&
					l.wait.CompareAndSwap(gp.waiting.ticket+1, gp.waiting.ticket) {
					detachedWait = gp.waiting
					if l.head == detachedWait {
						l.head, l.tail = nil, nil
					} else {
						previous.next = nil
						l.tail = previous
					}
					detachedWait.next = nil
					gp.waiting = nil
					detachedWait.elem.set(nil)
				}
				unlock(&l.lock)
			} else if !closeWait && isolatePhase0SemaWait(gp.waitreason) && gp.waiting != nil && gp.waiting.c.get() == nil && gp.waiting.elem.get() != nil {
				// A semaphore waiter records its address in sudog.elem.
				// The root lock also protects the per-address waitlink list.
				addr := (*uint32)(gp.waiting.elem.get())
				root := semtable.rootFor(addr)
				lockWithRank(&root.lock, lockRankRoot)
				key := uintptr(unsafe.Pointer(addr))
				for head := root.treap; head != nil; {
					value := head.elem.uintptr()
					if key == value {
						if head == gp.waiting {
							detachedWait, _, _ = root.dequeue(addr)
							root.nwait.Add(-1)
						} else {
							for prev, current := head, head.waitlink; current != nil; prev, current = current, current.waitlink {
								if current != gp.waiting {
									continue
								}
								prev.waitlink = current.waitlink
								if head.waittail == current {
									if prev == head {
										head.waittail = nil
									} else {
										head.waittail = prev
									}
								}
								if head.waiters > 0 {
									head.waiters--
								}
								current.waitlink = nil
								current.elem.set(nil)
								gp.waiting = nil
								root.nwait.Add(-1)
								isolatePhase0NonHeadDetachCount.Add(1)
								detachedWait = current
								break
							}
						}
						break
					}
					if key < value {
						head = head.prev
					} else {
						head = head.next
					}
				}
				unlock(&root.lock)
			}
			resumeG(state)
			if detachedWait != nil {
				goready(gp, 0)
			}
		}
		casgstatus(me, _Gwaiting, _Grunning)
	})
	if closeWait && ch == nil {
		isolatePhase0KillTarget.StoreNoWB(nil)
		return nanotime() - start, false
	}
	if ch != nil {
		closechan(ch)
	}
	for !dead && isolatePhase0KillAck.Load() == 0 && nanotime()-start < 100*1000*1000 {
		Gosched()
	}
	dead = dead || isolatePhase0KillAck.Load() != 0
	elapsed = nanotime() - start
	isolatePhase0KillTarget.StoreNoWB(nil)
	if dead && detachedWait != nil {
		releaseSudog(detachedWait)
	}
	return
}

func isolatePhase0SemaWait(reason waitReason) bool {
	switch reason {
	case waitReasonSemacquire, waitReasonSyncMutexLock, waitReasonSyncRWMutexRLock,
		waitReasonSyncRWMutexLock, waitReasonSyncWaitGroupWait:
		return true
	}
	return false
}
