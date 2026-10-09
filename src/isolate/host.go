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
	Program        Program
	Deterministic  bool       // FIFO native goroutines and deterministic select/map iteration.
	InitialTime    *time.Time // Enables the host clock before package initialization.
	TimerOp        uint32     // Call operation used for durable timer waits.
	ResourceLimits ResourceLimits
	// LogHandler enables LogOp and services printing during initialization.
	// After Start, logging messages arrive on Writes without acknowledgments.
	LogHandler func(LogRecord)
}

// Command is one host request made by Call. Reply must be called once.
type Command = isolatebridge.Command

// Message is observational output from Write. Payload is a host-owned copy;
// no acknowledgment or reply is required or possible.
type Message = isolatebridge.Message

// Isolate is a trusted instance of one statically linked program. This POC
// uses owner-specific allocations under the shared Go collector. Deterministic
// mode gates native goroutines through a FIFO token. It is not a process sandbox.
type Isolate struct {
	entry         func()
	runState      func(func())
	boundary      *isolatebridge.Boundary
	started       atomic.Bool
	lifecycleMu   sync.Mutex
	killed        bool
	done          chan struct{}
	terminal      chan struct{}
	completeOnce  sync.Once
	exitRequested atomic.Bool
	exitErr       *ExitError
	cause         error // immutable after terminal closes
	err           error // published by closing done
}

var errMainExited = errors.New("isolate: main goroutine exited without returning")
var errMainRevoked = errors.New("isolate: main goroutine revoked")
var errInitializerExited = errors.New("isolate: package initializer goroutine exited without returning")
var errInitializerFailed = errors.New("isolate: package initialization failed")

// ErrRevoked is the outcome of host-requested termination before a program
// publishes another terminal outcome. Revocation remains effective on timeout.
var ErrRevoked = errMainRevoked

// PanicError contains copied diagnostics for an unrecovered entry, child or
// initializer panic. It never retains the original arbitrary panic value.
type PanicError struct{ Phase, Message, Stack string }

func (e *PanicError) Error() string {
	return "isolate: unrecovered panic in " + e.Phase + ": " + e.Message + "\n" + e.Stack
}

// GoexitError reports a root goroutine that exited without completing its entry.
// Child Goexit retains ordinary Go behavior and is not an isolate failure.
type GoexitError struct{ Phase, Stack string }

func (e *GoexitError) Error() string {
	if e.Phase == "initialization" {
		return errInitializerExited.Error()
	}
	return errMainExited.Error()
}

// InitializationError reports failed startup whose revoked goroutines remain
// pending after bounded cleanup. Done closes when cleanup finishes. Retaining
// the error or notification does not keep a completed instance's heap alive.
type InitializationError struct {
	Cause   error
	Pending *KillPendingError
	Done    <-chan struct{}
}

func (e *InitializationError) Error() string {
	return "isolate: initialization failed with pending cleanup: " + e.Cause.Error() + ": " + e.Pending.Error()
}

func (e *InitializationError) Unwrap() error { return e.Cause }

// ExitError reports a nonzero status from os.Exit or syscall.Exit inside a
// program. Exit with status zero completes Wait successfully.
type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return "isolate: exited with status " + strconv.Itoa(e.Code)
}

// OwnershipError reports a permanently revoked instance after a memory owner
// violation. Application recover and defers cannot resume the instance. Kill
// waits for the remaining runtime/service cleanup fence.
type OwnershipError struct{ Reason, Stack string }

func (e *OwnershipError) Error() string { return e.Reason + "\n" + e.Stack }

// EffectError reports a forbidden operation attempted before its effect occurred.
// The isolate is permanently revoked; workflow recover and defers cannot run.
type EffectError struct{ Operation, Stack string }

func (e *EffectError) Error() string {
	return "isolate: forbidden operation " + e.Operation + "\n" + e.Stack
}

// KillPendingError reports goroutines still attached to a revoked instance.
// Running includes goroutines in syscalls. Diagnostics are best effort: a busy
// running stack is never suspended merely to produce a sample.
type KillPendingError struct {
	GoroutineID       uint64
	ThreadID          int64
	Stack             string
	LiveGoroutines    int32
	RunningGoroutines int32
}

func (e *KillPendingError) Error() string {
	return "isolate: kill pending: " + strconv.FormatInt(int64(e.LiveGoroutines), 10) +
		" goroutines remain (running=" + strconv.FormatInt(int64(e.RunningGoroutines), 10) +
		", goroutine=" + strconv.FormatUint(e.GoroutineID, 10) + ", thread=" + strconv.FormatInt(e.ThreadID, 10) + ")"
}

// Named lifecycle handlers keep their audited identity independent of the
// anonymous-function numbering in New. They execute no application callbacks.
type initializerCompletion struct {
	instance *Isolate
	done     chan struct{}
	once     sync.Once
}

func (c *initializerCompletion) close() {
	c.once.Do(func() { close(c.done) })
}

func (c *initializerCompletion) exit(code int) {
	c.instance.completeExit(code)
	c.close()
}

func (c *initializerCompletion) fault(reason string) {
	c.instance.completeOwnershipFault(reason)
	c.close()
}

// New prepares an instance using an unbounded initialization context. Use
// NewContext to bound an initializer. Initializers may use configured logging;
// application host calls require Start.
func New(cfg Config) (*Isolate, error) {
	return NewContext(context.Background(), cfg)
}

// NewContext prepares an instance while observing ctx's cancellation. Failed
// startup revokes its children and attempts cleanup for at most 100 ms. If any
// remain, InitializationError carries a cleanup notification and diagnostics.
func NewContext(ctx context.Context, cfg Config) (*Isolate, error) {
	if ctx == nil {
		return nil, errors.New("isolate: nil initialization context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Program.entry.Main == nil || cfg.Program.entry.NewState == nil {
		return nil, errors.New("isolate: unknown program")
	}
	boundary := isolatebridge.New()
	if err := boundary.ConfigureResources(cfg.ResourceLimits.MaxMemoryBytes, cfg.ResourceLimits.MaxGoroutines); err != nil {
		return nil, err
	}
	if cfg.LogHandler != nil && !boundary.ConfigureLogging() {
		return nil, errors.New("isolate: cannot configure logging")
	}
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
		terminal: make(chan struct{}),
		// A retained exit error must not retain its instance or allocator cache.
		exitErr: new(ExitError),
	}
	prepared := false
	defer func() {
		if !prepared {
			select {
			case <-i.terminal:
				return // Failure already queued cleanup, possibly still pending.
			default:
			}
			// Also release startup state if a host logging handler panics.
			cleanup, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_ = i.Kill(cleanup)
		}
	}()
	var runState func(func())
	var err error
	// Initializers can call runtime.Goexit. Run them on a dedicated goroutine
	// so that doing so does not terminate the host goroutine calling New.
	completion := &initializerCompletion{instance: i, done: make(chan struct{})}
	boundary.SetExitHandler(completion.exit)
	boundary.SetOwnershipFaultHandler(completion.fault)
	ready := make(chan struct{})
	go func() {
		defer completion.close()
		boundary.RunOwnerReady(func() {
			returned := false
			defer func() {
				if value := recover(); value != nil {
					boundary.ReportPanic(value, "initialization")
				} else if !returned {
					boundary.ReportGoexit("initialization")
				}
			}()
			runState, err = cfg.Program.entry.NewState()
			if err != nil {
				// A state factory can return an error whose object or fields
				// belong to the isolate. Keep it behind the boundary.
				err = errInitializerFailed
			}
			returned = true
		}, ready)
	}()
	<-ready
	// New has not returned an instance yet. Service only the reserved logging
	// Write so initializers can print without an active Call boundary.
	handleLog := func(message *Message) error {
		if message.Op != LogOp || cfg.LogHandler == nil {
			return errors.New("isolate: initializer attempted an unsupported host write")
		}
		record, cause := DecodeLog(message.Payload)
		if cause != nil {
			return cause
		}
		cfg.LogHandler(record)
		return nil
	}
initialize:
	for {
		select {
		case <-completion.done:
			break initialize
		case <-ctx.Done():
			return nil, i.initializationFailure(ctx.Err())
		case message := <-boundary.Writes():
			if cause := handleLog(message); cause != nil {
				return nil, i.initializationFailure(cause)
			}
		case <-boundary.Commands():
			return nil, i.initializationFailure(errors.New("isolate: initializer attempted an unsupported host call"))
		}
	}
	// Completion and the final writes can be ready simultaneously. Initializer
	// records must not depend on which ready select case the host chose first.
drainInitializerLogs:
	for {
		select {
		case message := <-boundary.Writes():
			if cause := handleLog(message); cause != nil {
				return nil, i.initializationFailure(cause)
			}
		default:
			break drainInitializerLogs
		}
	}
	if reason := boundary.OwnershipFaultReason(); reason != "" {
		return nil, i.initializationFailure(i.faultError(reason))
	}
	if i.exitRequested.Load() {
		return nil, i.initializationFailure(i.exitErr)
	}
	if err != nil {
		return nil, i.initializationFailure(err)
	}
	if runState == nil {
		return nil, i.initializationFailure(errors.New("isolate: program has no state runner"))
	}
	i.lifecycleMu.Lock()
	if boundary.Stopped() {
		i.lifecycleMu.Unlock()
		return nil, i.initializationFailure(ErrRevoked)
	}
	i.runState = runState
	prepared = true
	i.lifecycleMu.Unlock()
	return i, nil
}

func (i *Isolate) initializationFailure(cause error) error {
	i.complete(cause)
	cleanup, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	select {
	case <-i.done:
		if i.err != nil {
			return i.err
		}
		return cause // Includes successful os.Exit(0), which still aborts startup.
	case <-cleanup.Done():
		return &InitializationError{Cause: cause, Pending: i.pendingError(), Done: i.done}
	}
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
		if reason := i.boundary.OwnershipFaultReason(); reason != "" {
			return i.faultError(reason)
		}
		return errors.New("isolate: instance already started or revoked")
	}
	ready := make(chan struct{})
	go func() {
		i.boundary.RunOwnerReady(func() {
			returned := false
			defer func() {
				if value := recover(); value != nil {
					i.boundary.ReportPanic(value, "main")
				} else if !returned {
					if i.boundary.Stopped() {
						i.complete(errMainRevoked)
					} else {
						i.boundary.ReportGoexit("main")
					}
				} else {
					i.complete(nil)
				}
			}()
			i.runState(func() { i.boundary.Run(i.entry) })
			returned = true
		}, ready)
	}()
	<-ready
	return nil
}

func (i *Isolate) complete(err error) {
	owner := isolatebridge.EnterProcess()
	defer isolatebridge.LeaveProcess(owner)
	i.completeOnce.Do(func() {
		i.beginCompletion(err)
	})
}

func (i *Isolate) completeExit(code int) {
	owner := isolatebridge.EnterProcess()
	defer isolatebridge.LeaveProcess(owner)
	i.completeOnce.Do(func() {
		i.exitErr.Code = code
		i.exitRequested.Store(true)
		var err error
		if code != 0 {
			err = i.exitErr
		}
		i.beginCompletion(err)
	})
}

func (i *Isolate) completeOwnershipFault(reason string) {
	i.complete(i.faultError(reason))
}

// Called only by the winner of completeOnce, inside a trusted process scope.
func (i *Isolate) beginCompletion(cause error) {
	if reason := i.boundary.OwnershipFaultReason(); reason != "" {
		cause = i.faultError(reason)
	}
	i.cause = cause
	close(i.terminal)
	i.boundary.BeginStop()
	i.boundary.QueueCleanup(i.finishCleanup)
}

// A runtime system G owns cleanup independently of application defers. Waiter
// scanning finishes before the group-drain notification establishes zero.
func (i *Isolate) finishCleanup() {
	i.boundary.WakeStoppedWaiters()
	i.boundary.WaitDrained()
	i.boundary.ReleaseAllocation()
	i.lifecycleMu.Lock()
	i.entry, i.runState = nil, nil
	i.err = i.cause
	// A fatal fault may have been published after main return won completeOnce.
	// All members are gone now, so the first immutable fault is final.
	if reason := i.boundary.OwnershipFaultReason(); reason != "" {
		i.err = i.faultError(reason)
	}
	close(i.done)
	i.lifecycleMu.Unlock()
}

// Commands returns host requests from the program. The host must reply to
// each request using Command.Reply.
func (i *Isolate) Commands() <-chan *Command { return i.boundary.Commands() }

// Writes returns best-effort observational messages, independently of Commands.
// A host can consume this stream on its own goroutine without resuming instance
// dispatch. Buffered messages remain readable after Done; the channel is not
// closed. Hosts should drain it on completion if final output is needed.
func (i *Isolate) Writes() <-chan *Message { return i.boundary.Writes() }

// DroppedWrites reports writes lost to queue or payload limits. It is a host
// diagnostic; instance code cannot observe delivery or backend failures.
func (i *Isolate) DroppedWrites() uint64 { return i.boundary.DroppedWrites() }

// Suspend waits for every deterministic instance goroutine to block, then
// fences dispatch. Service Commands concurrently until this returns. Deliver
// host event replies while suspended, then call Resume to run the next task.
func (i *Isolate) Suspend() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	err := i.boundary.Suspend()
	return i.executionError(err)
}

// Resume allows suspended instance work to run in FIFO order.
func (i *Isolate) Resume() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	err := i.boundary.Resume()
	return i.executionError(err)
}

// Dispatch can observe revocation before Done publishes complete cleanup.
// Preserve its typed cause so an SDK never mistakes exit for a returned error.
func (i *Isolate) executionError(err error) error {
	if reason := i.boundary.OwnershipFaultReason(); reason != "" {
		return i.faultError(reason)
	}
	if i.boundary.Stopped() {
		if i.exitRequested.Load() && i.exitErr.Code != 0 {
			return i.exitErr
		}
		return ErrRevoked
	}
	return err
}

func (i *Isolate) faultError(reason string) error {
	_, stack := i.boundary.EffectFaultDetails()
	if kind, phase, message := i.boundary.LifecycleFaultDetails(); kind == "panic" {
		return &PanicError{Phase: phase, Message: message, Stack: stack}
	} else if kind == "Goexit" {
		return &GoexitError{Phase: phase, Stack: stack}
	} else if kind == "resource" {
		return i.resourceFault(stack)
	}
	if operation, stack := i.boundary.EffectFaultDetails(); operation != "" {
		return &EffectError{Operation: operation, Stack: stack}
	}
	return &OwnershipError{Reason: reason, Stack: stack}
}

// PendingCalls counts outstanding host operations. Inspect after Suspend;
// a live instance with no pending host calls cannot progress without native
// synchronization and is deadlocked under the trusted deterministic contract.
func (i *Isolate) PendingCalls() int64 { return i.boundary.PendingCalls() }

// Done closes after a terminal outcome and every attached goroutine's runtime
// cleanup. No later wakeup can execute instance code after it closes.
func (i *Isolate) Done() <-chan struct{} { return i.done }

// Wait waits for the whole instance to finish cleanup and reports its terminal
// outcome, including unrecovered child panics. It can remain blocked on pending
// termination; use Kill with a context to observe a deadline and diagnostics.
func (i *Isolate) Wait() error {
	if i == nil {
		return errors.New("isolate: instance not started")
	}
	if !i.started.Load() {
		select {
		case <-i.terminal:
		default:
			return errors.New("isolate: instance not started")
		}
	}
	<-i.done
	return i.err
}

// Kill permanently revokes execution and waits for the complete cleanup fence.
// It wakes Calls and supported native waits. A nil result is stable: no later
// event can resume instance code. If ctx expires, KillPendingError describes
// remaining execution; a later Kill can wait for its cleanup without reviving it.
func (i *Isolate) Kill(ctx context.Context) error {
	if i == nil || ctx == nil {
		return errors.New("isolate: nil instance or context")
	}
	i.lifecycleMu.Lock()
	i.killed = true
	i.complete(ErrRevoked)
	i.lifecycleMu.Unlock()

	select {
	case <-i.done:
		return nil
	case <-ctx.Done():
		select {
		case <-i.done:
			return nil
		default:
			return i.pendingError()
		}
	}
}

func (i *Isolate) pendingError() *KillPendingError {
	id, thread, stack := i.boundary.Snapshot()
	return &KillPendingError{GoroutineID: id, ThreadID: thread, Stack: stack,
		LiveGoroutines: i.boundary.LiveGoroutines(), RunningGoroutines: i.boundary.RunningGoroutines()}
}

// FreezeWorkflow fences workflow continuations at their next park, while an
// in-progress Suspend waits only for read-only service work. Completed workflows
// may retain their private state this way until host cache eviction calls Kill.
func (i *Isolate) FreezeWorkflow() { i.boundary.FreezeWorkflow() }

// ResumeReadOnly admits only read-only service goroutines. Other continuations
// keep their FIFO positions. Service Commands and call Suspend as with Resume.
func (i *Isolate) ResumeReadOnly() error {
	if i == nil || !i.started.Load() {
		return errors.New("isolate: instance not started")
	}
	return i.executionError(i.boundary.ResumeReadOnly())
}
