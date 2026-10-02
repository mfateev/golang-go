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
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Boundary is one host command transport. Its channel is
// infrastructure for the Phase 2B API probe, not a contained isolate heap.
type Boundary struct {
	owner   uintptr
	group   unsafe.Pointer
	calls   chan *Command
	next    atomic.Uint64
	halt    chan struct{}
	stop    sync.Once
	stopped atomic.Bool
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
	return b
}

// Run binds b to the current goroutine for fn. Ordinary child goroutines
// inherit that binding. A native isolate scheduler will own this binding
// instead of a Go host call.
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
// channel, select, and Cond waiters. Other running and parked goroutines are
// not stopped by this method alone.
func (b *Boundary) RevokeUnstarted() { revokeUnstarted(b.group) }

// Stop fences unstarted children and wakes goroutines parked in Call, a
// registered network poll wait, a real time.Sleep wait, or a channel, select,
// or Cond wait.
// A reply that wins just before Stop may leave its caller active. Other
// runtime waiters and active code still require native revocation.
func (b *Boundary) Stop() {
	b.stop.Do(func() {
		b.stopped.Store(true)
		// Fence new isolate execution before Call resumes. Call's halt
		// channel is independent of the scan of runtime wait queues.
		wake := markRevoked(b.group)
		close(b.halt)
		if wake {
			wakeRevoked(b.group)
		}
	})
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
	id := b.next.Add(1)
	if id == 0 {
		panic("isolate: command ID exhausted")
	}
	c := newHostCommand(id, op, payload)
	select {
	case <-b.halt:
		runtime.Goexit()
	case b.calls <- c:
	}
	var r response
	select {
	case <-b.halt:
		runtime.Goexit()
	case r = <-c.reply:
	}
	b.stopIfRevoked()
	if r.hasErr {
		return bytes.Clone(r.payload), errors.New(r.errText)
	}
	return bytes.Clone(r.payload), nil
}

func (b *Boundary) stopIfRevoked() {
	select {
	case <-b.halt:
		runtime.Goexit()
	default:
	}
}

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
