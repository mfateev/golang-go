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

// Name returns the stable configured program name or marked function name.
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
	Program       Program
	Deterministic bool       // FIFO native goroutines and deterministic select/map iteration.
	InitialTime   *time.Time // Enables the host clock before package initialization.
	TimerOp       uint32     // Call operation used for durable timer waits.
}

// Command is one host request made by Call. Reply must be called once.
type Command = isolatebridge.Command

// Isolate is a trusted instance of one statically linked program. This POC
// uses the ordinary Go heap. Deterministic mode gates its native goroutines
// through a FIFO execution token; it does not provide containment.
type Isolate struct {
	entry         func()
	runState      func(func())
	boundary      *isolatebridge.Boundary
	started       atomic.Bool
	lifecycleMu   sync.Mutex
	killed        bool
	done          chan struct{}
	completeOnce  sync.Once
	watchOnce     sync.Once
	scanOnce      sync.Once
	exitRequested atomic.Bool
	exitErr       ExitError
	err           error // published by closing done
}

var errMainPanicked = errors.New("isolate: main panicked")
var errMainExited = errors.New("isolate: main goroutine exited without returning")
var errMainRevoked = errors.New("isolate: main goroutine revoked")
var errInitializerPanicked = errors.New("isolate: package initializer panicked")
var errInitializerExited = errors.New("isolate: package initializer goroutine exited without returning")
var errInitializerFailed = errors.New("isolate: package initialization failed")

// ExitError reports a nonzero status from os.Exit or syscall.Exit inside a
// program. Exit with status zero completes Wait successfully.
type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return "isolate: exited with status " + strconv.Itoa(e.Code)
}

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
	if cfg.Deterministic {
		if err := boundary.EnableDeterminism(); err != nil {
			return nil, err
		}
	}
	if cfg.InitialTime != nil {
		clock, err := isolateTimeNanos(*cfg.InitialTime)
		if err != nil {
			return nil, err
		}
		if err := boundary.ConfigureTime(clock, cfg.TimerOp); err != nil {
			return nil, err
		}
	}
	i := &Isolate{
		entry:    cfg.Program.entry.Main,
		boundary: boundary,
		done:     make(chan struct{}),
	}
	var runState func(func())
	var err error
	// Initializers can call runtime.Goexit. Run them on a dedicated goroutine
	// so that doing so does not terminate the host goroutine calling New.
	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	boundary.SetExitHandler(func(code int) {
		i.completeExit(code)
		closeDone()
	})
	go func() {
		defer closeDone()
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
	if i.exitRequested.Load() {
		boundary.Stop()
		return nil, &i.exitErr
	}
	if err != nil {
		boundary.Stop()
		return nil, err
	}
	if runState == nil {
		boundary.Stop()
		return nil, errors.New("isolate: program has no state runner")
	}
	i.runState = runState
	return i, nil
}

func isolateTimeNanos(t time.Time) (int64, error) {
	ns := t.UnixNano()
	if !time.Unix(0, ns).Equal(t) {
		return 0, errors.New("isolate: host time is outside Unix nanosecond range")
	}
	return ns, nil
}

// AdvanceTime publishes a history timestamp for the next workflow task. The
// host must call it while the instance is quiescent and before replying to any
// commands from that task. Time cannot move backwards within an instance.
func (i *Isolate) AdvanceTime(t time.Time) error {
	if i == nil {
		return errors.New("isolate: nil instance")
	}
	ns, err := isolateTimeNanos(t)
	if err != nil {
		return err
	}
	return i.boundary.AdvanceTime(ns)
}

// Start runs the program's ordinary main on a new goroutine. It may be called
// once. Deterministic mode also applies to its children and initialization.
func (i *Isolate) Start() error {
	if i == nil {
		return errors.New("isolate: instance already started or nil")
	}
	i.lifecycleMu.Lock()
	defer i.lifecycleMu.Unlock()
	if i.killed || i.exitRequested.Load() || i.boundary.Stopped() || !i.started.CompareAndSwap(false, true) {
		return errors.New("isolate: instance already started or revoked")
	}
	ready := make(chan struct{})
	go func() {
		defer i.boundary.Stop()
		i.boundary.RunOwner(func() {
			close(ready) // group membership is visible before Start returns
			returned := false
			defer func() {
				if recover() != nil {
					i.complete(errMainPanicked)
				} else if !returned {
					if i.boundary.Stopped() {
						i.complete(errMainRevoked)
					} else {
						i.complete(errMainExited)
					}
				} else {
					i.complete(nil)
				}
			}()
			i.runState(func() { i.boundary.Run(i.entry) })
			returned = true
		})
	}()
	<-ready
	return nil
}

func (i *Isolate) complete(err error) {
	i.completeOnce.Do(func() {
		i.err = err
		close(i.done)
	})
}

func (i *Isolate) completeExit(code int) {
	i.exitRequested.Store(true)
	i.completeOnce.Do(func() {
		i.exitErr.Code = code
		if code != 0 {
			i.err = &i.exitErr
		}
		close(i.done)
	})
}

// A revoked main may be discarded by the runtime without running Go defers.
// Observe group destruction from the host so Wait and Done still complete.
func (i *Isolate) watchRevokedCompletion() {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if i.boundary.LiveGoroutines() == 0 {
			i.complete(errMainRevoked)
			return
		}
		select {
		case <-i.done:
			return
		case <-ticker.C:
		}
	}
}

// Commands returns host requests from the program. The host must reply to
// each request using Command.Reply.
func (i *Isolate) Commands() <-chan *Command { return i.boundary.Commands() }

// Suspend waits for every deterministic instance goroutine to block, then
// fences dispatch. Service Commands concurrently until this returns. Deliver
// host event replies while suspended, then call Resume to run the next task.
func (i *Isolate) Suspend() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	return i.boundary.Suspend()
}

// Resume allows suspended instance work to run in FIFO order.
func (i *Isolate) Resume() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	return i.boundary.Resume()
}

// PendingCalls counts outstanding host operations. Inspect after Suspend;
// a live instance with no pending host calls cannot progress without native
// synchronization and is deadlocked under the trusted deterministic contract.
func (i *Isolate) PendingCalls() int64 { return i.boundary.PendingCalls() }

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
// real time.Sleep, channel, select, Cond, and sync semaphore waiters, then
// waits for every attached goroutine to exit.
// Other runtime waits are not yet interrupted. If ctx expires while one
// remains, Kill returns a pending error. This is a provisional lifecycle,
// not safe heap teardown.
func (i *Isolate) Kill(ctx context.Context) error {
	if i == nil || ctx == nil {
		return errors.New("isolate: nil instance or context")
	}
	i.lifecycleMu.Lock()
	i.killed = true
	// Publish the fence and wake Call before waiting on any runtime queue
	// lock. The scan runs on a process goroutine, so a blocked scan cannot
	// prevent this call from observing ctx's deadline.
	i.boundary.BeginStop()
	i.scanOnce.Do(func() { go i.boundary.WakeStoppedWaiters() })
	if i.started.Load() {
		i.watchOnce.Do(func() { go i.watchRevokedCompletion() })
	}
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
