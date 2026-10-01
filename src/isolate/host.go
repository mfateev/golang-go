// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Program identifies one statically linked program in the current binary.
type Program struct {
	name  string
	entry isolatebridge.ProgramEntry
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

// Config selects one program.
type Config struct {
	Program Program
}

// Command is one host request made by Call. Reply must be called once.
type Command = isolatebridge.Command

// Isolate is a trusted instance of one statically linked program. This POC
// uses the ordinary Go heap and scheduler; it does not provide containment.
type Isolate struct {
	entry       func()
	runState    func(func())
	boundary    *isolatebridge.Boundary
	started     atomic.Bool
	lifecycleMu sync.Mutex
	killed      bool
	done        chan struct{}
	err         error // published by closing done
}

var errMainPanicked = errors.New("isolate: main panicked")
var errMainExited = errors.New("isolate: main goroutine exited without returning")
var errMainRevoked = errors.New("isolate: main goroutine revoked")
var errInitializerPanicked = errors.New("isolate: package initializer panicked")
var errInitializerExited = errors.New("isolate: package initializer goroutine exited without returning")
var errInitializerFailed = errors.New("isolate: package initialization failed")

// KillPendingError reports goroutines still attached to a revoked instance.
// Running includes goroutines in syscalls; no stack sample is available yet.
type KillPendingError struct {
	GoroutineID       uint64
	ThreadID          int64
	Stack             string
	LiveGoroutines    int32
	RunningGoroutines int32
}

func (e *KillPendingError) Error() string {
	return "isolate: kill pending: " + strconv.FormatInt(int64(e.LiveGoroutines), 10) + " goroutines remain"
}

// New prepares an instance. Its program can request initial input with Call.
func New(cfg Config) (*Isolate, error) {
	if cfg.Program.entry.Main == nil || cfg.Program.entry.NewState == nil {
		return nil, errors.New("isolate: unknown program")
	}
	boundary := isolatebridge.New()
	var runState func(func())
	var err error
	// Initializers can call runtime.Goexit. Run them on a dedicated goroutine
	// so that doing so does not terminate the host goroutine calling New.
	done := make(chan struct{})
	go func() {
		defer close(done)
		boundary.RunOwner(func() {
			returned := false
			defer func() {
				if recover() != nil {
					err = errInitializerPanicked
				} else if !returned {
					err = errInitializerExited
				}
			}()
			runState, err = cfg.Program.entry.NewState()
			if err != nil {
				// A state factory can return an error whose object or fields
				// belong to the isolate. Keep it behind the boundary.
				err = errInitializerFailed
			}
			returned = true
		})
	}()
	<-done
	if err != nil {
		boundary.Stop()
		return nil, err
	}
	if runState == nil {
		boundary.Stop()
		return nil, errors.New("isolate: program has no state runner")
	}
	return &Isolate{
		entry:    cfg.Program.entry.Main,
		runState: runState,
		boundary: boundary,
		done:     make(chan struct{}),
	}, nil
}

// Start runs the program's ordinary main on a new goroutine. It may be called
// once. A future runtime scheduler will replace this trusted POC lifecycle.
func (i *Isolate) Start() error {
	if i == nil {
		return errors.New("isolate: instance already started or nil")
	}
	i.lifecycleMu.Lock()
	defer i.lifecycleMu.Unlock()
	if i.killed || !i.started.CompareAndSwap(false, true) {
		return errors.New("isolate: instance already started or revoked")
	}
	ready := make(chan struct{})
	go func() {
		defer close(i.done)
		defer i.boundary.Stop()
		i.boundary.RunOwner(func() {
			close(ready) // group membership is visible before Start returns
			returned := false
			defer func() {
				if recover() != nil {
					i.err = errMainPanicked
				} else if !returned {
					if i.boundary.Stopped() {
						i.err = errMainRevoked
					} else {
						i.err = errMainExited
					}
				}
			}()
			i.runState(func() { i.boundary.Run(i.entry) })
			returned = true
		})
	}()
	<-ready
	return nil
}

// Commands returns host requests from the program. The host must reply to
// each request using Command.Reply.
func (i *Isolate) Commands() <-chan *Command { return i.boundary.Commands() }

// Done is closed when the program's main goroutine exits.
func (i *Isolate) Done() <-chan struct{} { return i.done }

// Wait waits for main to return or terminate and reports its failure.
// Native child goroutines are not yet covered by this provisional lifecycle.
func (i *Isolate) Wait() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	<-i.done
	return i.err
}

// Kill revokes unstarted children, wakes Call, registered network poll,
// real time.Sleep, and channel waiters, then waits for every attached
// goroutine to exit.
// Other runtime waits are not yet interrupted. If ctx expires while one
// remains, Kill returns a pending error. This is a provisional lifecycle,
// not safe heap teardown.
func (i *Isolate) Kill(ctx context.Context) error {
	if i == nil || ctx == nil {
		return errors.New("isolate: nil instance or context")
	}
	i.lifecycleMu.Lock()
	i.killed = true
	i.boundary.Stop()
	i.lifecycleMu.Unlock()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if i.boundary.LiveGoroutines() == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			live := i.boundary.LiveGoroutines()
			if live == 0 {
				return nil
			}
			return &KillPendingError{LiveGoroutines: live, RunningGoroutines: i.boundary.RunningGoroutines()}
		case <-ticker.C:
		}
	}
}
