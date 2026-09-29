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

// Boundary is one host command and inbox transport. Its channels are
// infrastructure for the Phase 2B API probe, not a contained isolate heap.
type Boundary struct {
	inbox chan []byte
	calls chan *Command
	next  atomic.Uint64
}

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

// New creates a boundary with an initial, copied Inbox message.
func New(initial []byte) *Boundary {
	b := &Boundary{
		inbox: make(chan []byte, 1),
		calls: make(chan *Command),
	}
	b.inbox <- bytes.Clone(initial)
	return b
}

// Run binds b to the current goroutine for fn. Ordinary child goroutines
// inherit that binding. A native isolate scheduler will own this binding
// instead of a Go host call.
func (b *Boundary) Run(fn func()) {
	if b == nil || fn == nil {
		panic("isolate: nil boundary or entry")
	}
	old := setBoundary(unsafe.Pointer(b))
	defer func() {
		setBoundary(old)
		runtime.KeepAlive(b)
	}()
	fn()
}

// Send copies one host message into the Inbox. It blocks when the Inbox
// buffer is full until isolate code receives a message.
func (b *Boundary) Send(payload []byte) {
	b.inbox <- bytes.Clone(payload)
}

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
		panic("isolate: Call or Inbox outside an active isolate")
	}
	return (*Boundary)(p)
}

// Call copies one request to the host and waits for its response.
func (b *Boundary) Call(op uint32, payload []byte) ([]byte, error) {
	id := b.next.Add(1)
	if id == 0 {
		panic("isolate: command ID exhausted")
	}
	c := &Command{
		ID:      id,
		Op:      op,
		Payload: bytes.Clone(payload),
		reply:   make(chan response, 1),
	}
	b.calls <- c
	r := <-c.reply
	if r.hasErr {
		return bytes.Clone(r.payload), errors.New(r.errText)
	}
	return bytes.Clone(r.payload), nil
}

// Inbox returns this boundary's copied host-message stream.
func (b *Boundary) Inbox() <-chan []byte {
	return b.inbox
}

//go:linkname getBoundary runtime.isolateGetBoundary
func getBoundary() unsafe.Pointer

//go:linkname setBoundary runtime.isolateSetBoundary
func setBoundary(unsafe.Pointer) unsafe.Pointer
