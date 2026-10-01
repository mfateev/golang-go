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
	"sync/atomic"
	"unsafe"
)

// Boundary is one host command transport. Its channel is
// infrastructure for the Phase 2B API probe, not a contained isolate heap.
type Boundary struct {
	owner uintptr
	group unsafe.Pointer
	calls chan *Command
	next  atomic.Uint64
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
	id := b.next.Add(1)
	if id == 0 {
		panic("isolate: command ID exhausted")
	}
	c := newHostCommand(id, op, payload)
	b.calls <- c
	r := <-c.reply
	if r.hasErr {
		return bytes.Clone(r.payload), errors.New(r.errText)
	}
	return bytes.Clone(r.payload), nil
}

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
