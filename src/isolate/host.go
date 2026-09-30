// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"errors"
	"internal/isolatebridge"
	"sync/atomic"
)

// Program identifies one statically linked program in the current binary.
type Program struct {
	name  string
	entry func()
}

// Name returns the stable name from the program's isolate.json.
func (p Program) Name() string { return p.name }

// LookupProgram finds a program selected by go build -isolate-dir.
func LookupProgram(name string) (Program, bool) {
	entry, ok := isolatebridge.LookupProgram(name)
	if !ok {
		return Program{}, false
	}
	return Program{name: name, entry: entry}, true
}

// Config selects one program and supplies its first Inbox message.
// The input is copied by New.
type Config struct {
	Program Program
	Input   []byte
}

// Command is one host request made by Call. Reply must be called once.
type Command = isolatebridge.Command

// Isolate is a trusted instance of one statically linked program. This POC
// uses the ordinary Go heap and scheduler; it does not provide containment.
type Isolate struct {
	entry    func()
	boundary *isolatebridge.Boundary
	started  atomic.Bool
	done     chan struct{}
}

// New prepares an instance and copies its initial Inbox message.
func New(cfg Config) (*Isolate, error) {
	if cfg.Program.entry == nil {
		return nil, errors.New("isolate: unknown program")
	}
	return &Isolate{
		entry:    cfg.Program.entry,
		boundary: isolatebridge.New(cfg.Input),
		done:     make(chan struct{}),
	}, nil
}

// Start runs the program's ordinary main on a new goroutine. It may be called
// once. A future runtime scheduler will replace this trusted POC lifecycle.
func (i *Isolate) Start() error {
	if i == nil || !i.started.CompareAndSwap(false, true) {
		return errors.New("isolate: instance already started or nil")
	}
	go func() {
		defer close(i.done)
		i.boundary.Run(i.entry)
	}()
	return nil
}

// Commands returns host requests from the program. The host must reply to
// each request using Command.Reply.
func (i *Isolate) Commands() <-chan *Command { return i.boundary.Commands() }

// Send copies a message into the program's Inbox.
func (i *Isolate) Send(payload []byte) { i.boundary.Send(payload) }

// Done is closed when the program's main returns.
func (i *Isolate) Done() <-chan struct{} { return i.done }
