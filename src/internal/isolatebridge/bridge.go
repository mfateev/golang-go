// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatebridge is the trusted, provisional host transport for the
// source-level isolate API. It does not provide isolation or deterministic
// scheduling; those remain native runtime work.
package isolatebridge

import (
	"bytes"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Boundary is one host command transport. Its channel is
// infrastructure for the Phase 2B API probe, not a contained isolate heap.
type Boundary struct {
	owner         uintptr
	group         unsafe.Pointer
	calls         chan *Command
	next          atomic.Uint64
	halt          chan struct{}
	stop          sync.Once
	wake          sync.Once
	wakeNeeded    bool
	stopped       atomic.Bool
	onExit        func(int)
	timerOp       uint32
	deterministic bool
	pendingCalls  atomic.Int64
}

var nextOwner atomic.Uintptr

// Command is one Call waiting for a host response. Payload is a copy owned by
// the host side of the transport.
type Command struct {
	ID      uint64
	Op      uint32
	Payload []byte

	reply   chan response
	replied atomic.Bool
}

type response struct {
	payload []byte
	errText string
	hasErr  bool
}

// New creates a boundary for one instance.
func New() *Boundary {
	owner := nextOwner.Add(1)
	if owner == 0 {
		panic("isolate: owner ID exhausted")
	}
	b := &Boundary{
		owner: owner,
		group: newGroup(),
		calls: make(chan *Command),
		halt:  make(chan struct{}),
	}
	setGroupExit(b.group, b.exit)
	return b
}

// EnableDeterminism opts into the native isolate FIFO/token scheduler. It
// must be called before initializers or any other group members run.
func (b *Boundary) EnableDeterminism() error {
	if !enableDeterminism(b.group) {
		return errors.New("isolate: cannot enable determinism after attaching goroutines")
	}
	b.deterministic = true
	return nil
}

// Suspend fences native dispatch when all members are blocked or finished.
// The host must service outstanding Commands concurrently while waiting.
func (b *Boundary) Suspend() error {
	if !b.deterministic {
		return errors.New("isolate: suspension requires deterministic dispatch")
	}
	suspend(b.group)
	if b.Stopped() {
		return errors.New("isolate: suspension interrupted by revocation")
	}
	return nil
}

// PendingCalls counts host calls that have not returned to instance code.
// After suspension it distinguishes host-event waits from native deadlock.
// Revocation may discard defers, so this count is not a teardown diagnostic.
func (b *Boundary) PendingCalls() int64 { return b.pendingCalls.Load() }

// Resume releases the suspension fence after host events have been delivered.
func (b *Boundary) Resume() error {
	if !b.deterministic {
		return errors.New("isolate: resume requires deterministic dispatch")
	}
	resume(b.group)
	return nil
}

//go:linkname suspend runtime.isolateSuspend
func suspend(unsafe.Pointer)

//go:linkname resume runtime.isolateResume
func resume(unsafe.Pointer)

//go:linkname enableDeterminism runtime.isolateEnableDeterminism
func enableDeterminism(unsafe.Pointer) bool

// ConfigureTime gives this boundary a host-controlled clock and a Call
// operation for durable timers. It must run before program initialization.
func (b *Boundary) ConfigureTime(unixNano int64, timerOp uint32) error {
	if timerOp == 0 {
		return errors.New("isolate: timer operation is required for a host clock")
	}
	if !setTimerSleep(b.group, b.TimerSleep) {
		return errors.New("isolate: host clock must be configured once before program initialization")
	}
	b.timerOp = timerOp
	if !setClock(b.group, unixNano) {
		return errors.New("isolate: clock moved backwards")
	}
	return nil
}

//go:linkname setTimerSleep runtime.isolateSetTimerSleep
func setTimerSleep(unsafe.Pointer, func(int64) error) bool

// AdvanceTime publishes a history timestamp before the host resumes a task.
func (b *Boundary) AdvanceTime(unixNano int64) error {
	if b.timerOp == 0 {
		return errors.New("isolate: host clock is not configured")
	}
	if !setClock(b.group, unixNano) {
		return errors.New("isolate: clock moved backwards")
	}
	return nil
}

// ClockEnabled reports whether the current goroutine has a host clock.
func ClockEnabled() bool { return clockEnabled() }

// TimerSleep waits for the host's durable timer event. The request payload is
// the JSON representation of a time.Duration, shared with SDK timer calls.
func (b *Boundary) TimerSleep(ns int64) error {
	if ns <= 0 {
		return nil
	}
	if b.timerOp == 0 {
		return errors.New("isolate: durable timer operation is not configured")
	}
	_, err := b.Call(b.timerOp, []byte(strconv.FormatInt(ns, 10)))
	return err
}

// SetExitHandler installs the host lifecycle callback before an instance
// starts. Exit from a program or initializer reports its status here.
func (b *Boundary) SetExitHandler(fn func(int)) {
	if b.onExit != nil || fn == nil {
		panic("isolate: invalid exit handler")
	}
	b.onExit = fn
}

func (b *Boundary) exit(code int) {
	b.Stop()
	if b.onExit != nil {
		b.onExit(code)
	}
}

// Run binds b to the current goroutine for fn. Ordinary child goroutines
// inherit that binding. Deterministic mode also claims the group execution
// token before running fn.
func (b *Boundary) Run(fn func()) {
	if b == nil || fn == nil {
		panic("isolate: nil boundary or entry")
	}
	oldGroup := setGroup(b.group)
	defer setGroup(oldGroup)
	oldOwner := setOwner(b.owner)
	defer setOwner(oldOwner)
	old := setBoundary(unsafe.Pointer(b))
	defer func() {
		setBoundary(old)
		runtime.KeepAlive(b)
	}()
	if b.Stopped() {
		return
	}
	fn()
}

// RunOwner binds b's stable instance identity without enabling Call.
// The generated state factory uses it while replaying package initializers.
func (b *Boundary) RunOwner(fn func()) {
	if b == nil || fn == nil {
		panic("isolate: nil boundary or initializer")
	}
	oldGroup := setGroup(b.group)
	defer setGroup(oldGroup)
	old := setOwner(b.owner)
	defer func() {
		setOwner(old)
		runtime.KeepAlive(b)
	}()
	fn()
}

// LiveGoroutines counts goroutines currently attached to this boundary's
// group. It includes a host goroutine while Run or RunOwner is executing.
// A zero count does not yet establish isolate quiescence.
func (b *Boundary) LiveGoroutines() int32 { return groupLive(b.group) }

// RunningGoroutines counts goroutines associated with an execution thread,
// including blocked syscalls. A zero count is not yet safe teardown: waiters
// and scheduler records still need ownership and revocation handling.
func (b *Boundary) RunningGoroutines() int32 { return groupRunning(b.group) }

// RunnableGoroutines conservatively counts group goroutines ready to run.
// This is a scheduler diagnostic, not a quiescence decision.
func (b *Boundary) RunnableGoroutines() int32 { return groupRunnable(b.group) }

// RevokeUnstarted prevents group children that have not begun executing from
// entering user code and wakes registered network poll, real time.Sleep,
// channel, select, Cond, and sync semaphore waiters. Other running and parked
// goroutines are not stopped by this method alone.
func (b *Boundary) RevokeUnstarted() { revokeUnstarted(b.group) }

// BeginStop publishes the revocation fence and wakes Call without waiting for
// the runtime's scan of other wait queues. It is safe to call more than once.
func (b *Boundary) BeginStop() {
	b.stop.Do(func() {
		b.stopped.Store(true)
		// Fence new isolate execution before Call resumes. Call's halt
		// channel is independent of the scan of runtime wait queues.
		b.wakeNeeded = markRevoked(b.group)
		close(b.halt)
	})
}

// WakeStoppedWaiters performs the runtime wait-queue scan once. A host may run
// it on a separate process goroutine so that Kill can observe its deadline
// even if the scan waits for a runtime lock.
func (b *Boundary) WakeStoppedWaiters() {
	b.BeginStop()
	b.wake.Do(func() {
		if b.wakeNeeded {
			wakeRevoked(b.group)
		}
	})
}

// Stop fences new execution, wakes Call, and scans runtime wait queues.
// A reply that wins just before Stop may leave its caller active. Other
// runtime waiters and active code still require native revocation.
func (b *Boundary) Stop() {
	b.WakeStoppedWaiters()
}

// Stopped reports whether the host has requested this boundary to stop.
func (b *Boundary) Stopped() bool { return b.stopped.Load() }

// Commands returns the stream of host commands from this boundary.
func (b *Boundary) Commands() <-chan *Command {
	return b.calls
}

// Reply answers a command once. The response bytes and error text are copied
// before the waiting isolate goroutine receives them.
func (c *Command) Reply(payload []byte, err error) {
	if !c.replied.CompareAndSwap(false, true) {
		panic("isolate: command already replied")
	}
	r := response{payload: bytes.Clone(payload)}
	if err != nil {
		r.hasErr = true
		r.errText = strings.Clone(err.Error())
	}
	c.reply <- r
}

// Current returns the boundary bound to this goroutine.
func Current() *Boundary {
	p := getBoundary()
	if p == nil {
		panic("isolate: Call outside an active isolate")
	}
	return (*Boundary)(p)
}

// Call copies one request to the host and waits for its response.
func (b *Boundary) Call(op uint32, payload []byte) ([]byte, error) {
	b.stopIfRevoked()
	b.pendingCalls.Add(1)
	defer b.pendingCalls.Add(-1)
	id := b.next.Add(1)
	if id == 0 {
		panic("isolate: command ID exhausted")
	}
	c := newHostCommand(id, op, payload)
	select {
	case <-b.halt:
		stopCall()
	case b.calls <- c:
	}
	var r response
	select {
	case <-b.halt:
		stopCall()
	case r = <-c.reply:
	}
	b.stopIfRevoked()
	if r.hasErr {
		return bytes.Clone(r.payload), errors.New(strings.Clone(r.errText))
	}
	return bytes.Clone(r.payload), nil
}

func (b *Boundary) stopIfRevoked() {
	select {
	case <-b.halt:
		stopCall()
	default:
	}
}

// stopCall runs after Stop has marked the group revoked and closed halt.
// Discarding the caller skips user defers, which may touch state abandoned by
// other goroutines in the same isolate. Goexit is a fallback if this boundary
// is used without an attached group.
func stopCall() {
	discardIfRevoked()
	runtime.Goexit()
}

//go:linkname discardIfRevoked runtime.isolateDiscardIfRevoked
func discardIfRevoked()

//go:linkname setClock runtime.isolateSetClock
func setClock(unsafe.Pointer, int64) bool

//go:linkname clockEnabled runtime.isolateClockEnabled
func clockEnabled() bool

//go:linkname markRevoked runtime.isolateMarkRevoked
func markRevoked(unsafe.Pointer) bool

//go:linkname wakeRevoked runtime.isolateWakeRevoked
func wakeRevoked(unsafe.Pointer)

//go:noinline
func newHostCommand(id uint64, op uint32, payload []byte) *Command {
	old := setOwner(0)
	defer setOwner(old)
	c := &Command{
		ID:      id,
		Op:      op,
		Payload: bytes.Clone(payload),
		reply:   make(chan response, 1),
	}
	return c
}

//go:linkname getBoundary runtime.isolateGetBoundary
func getBoundary() unsafe.Pointer

//go:linkname setBoundary runtime.isolateSetBoundary
func setBoundary(unsafe.Pointer) unsafe.Pointer

//go:linkname setOwner runtime.isolateSetOwner
func setOwner(uintptr) uintptr

//go:linkname newGroup runtime.isolateNewGroup
func newGroup() unsafe.Pointer

//go:linkname setGroupExit runtime.isolateSetGroupExit
func setGroupExit(unsafe.Pointer, func(int))

//go:linkname setGroup runtime.isolateSetGroup
func setGroup(unsafe.Pointer) unsafe.Pointer

//go:linkname groupLive runtime.isolateGroupLive
func groupLive(unsafe.Pointer) int32

//go:linkname groupRunning runtime.isolateGroupRunning
func groupRunning(unsafe.Pointer) int32

//go:linkname groupRunnable runtime.isolateGroupRunnable
func groupRunnable(unsafe.Pointer) int32

//go:linkname revokeUnstarted runtime.isolateRevokeUnstarted
func revokeUnstarted(unsafe.Pointer)
