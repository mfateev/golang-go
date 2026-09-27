// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolateproto

import (
	"encoding/binary"
	"runtime"
	"time"
)

// Task is the explicit Phase 1 replacement for runtime-managed execution.
// It must only be used by the registered goroutine that received it.
type Task struct {
	id     uint64
	iso    *Isolate
	permit chan reply
}

func (t *Task) exchange(req request) reply {
	if t.iso.revoked.Load() {
		runtime.Goexit()
	}
	req.from = t.id
	select {
	case t.iso.requests <- req:
	case <-t.iso.halt:
		runtime.Goexit()
	}
	select {
	case r := <-t.permit:
		if t.iso.revoked.Load() {
			runtime.Goexit()
		}
		return r
	case <-t.iso.halt:
		runtime.Goexit()
	}
	panic("unreachable")
}

// Yield lets the next registered task run. The runnable queue is FIFO.
func (t *Task) Yield() { t.exchange(request{kind: reqYield}) }

// Go starts a child task. The child enters the runnable queue before the
// caller, so it runs at the next scheduling point.
func (t *Task) Go(fn func(*Task)) {
	if fn == nil {
		panic("isolateproto: nil task function")
	}
	t.exchange(request{kind: reqGo, fn: fn})
}

// Call emits one copied command and parks this task until an Event answers it.
func (t *Task) Call(op uint32, payload []byte) ([]byte, error) {
	r := t.exchange(request{kind: reqCall, op: op, payload: clone(payload)})
	return clone(r.payload), r.err
}

// Inbox receives one copied unsolicited host event, blocking through the
// scheduler when no event is queued.
func (t *Task) Inbox() []byte {
	r := t.exchange(request{kind: reqInbox})
	return clone(r.payload)
}

// Now reads the host-injected logical clock while this task holds the baton.
func (t *Task) Now() time.Time { return t.iso.clock }

// Sleep emits a timer command. The host must answer it when logical time has
// reached the requested deadline, then update the clock before Resume.
func (t *Task) Sleep(d time.Duration) error {
	if d <= 0 {
		t.Yield()
		return nil
	}
	if t.iso.timerOp == 0 {
		panic("isolateproto: Sleep requires Config.TimerOp")
	}
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], uint64(d))
	_, err := t.Call(t.iso.timerOp, payload[:])
	return err
}

// RandUint64 uses an isolate-scoped PRNG. It is deterministic for the same
// seed and Task operation order; it makes no cryptographic claim.
func (t *Task) RandUint64() uint64 {
	x := t.iso.random
	if x == 0 {
		x = 0x9e3779b97f4a7c15
	}
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	t.iso.random = x
	return x * 0x2545f4914f6cdd1d
}
