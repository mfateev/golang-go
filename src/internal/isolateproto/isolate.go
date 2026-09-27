package isolateproto

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Entry is a process-local handle. Snapshot-stable identity uses its name.
type Entry struct{ id uint32 }

// EntryFunc receives copied input and returns output copied to the host.
type EntryFunc func(*Task, []byte) ([]byte, error)

var registry = struct {
	sync.Mutex
	byName map[string]Entry
	funcs  []EntryFunc
}{byName: make(map[string]Entry)}

// Register binds a unique name to an entry. Call it during package init.
func Register(name string, fn EntryFunc) Entry {
	if name == "" || fn == nil {
		panic("isolateproto: empty entry name or nil function")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.byName[name]; exists {
		panic("isolateproto: duplicate entry " + name)
	}
	if uint64(len(registry.funcs)) >= math.MaxUint32 {
		panic("isolateproto: entry registry exhausted")
	}
	id := Entry{id: uint32(len(registry.funcs) + 1)}
	registry.byName[name] = id
	registry.funcs = append(registry.funcs, fn)
	return id
}

// Lookup resolves the stable entry name in the current binary.
func Lookup(name string) (Entry, bool) {
	registry.Lock()
	defer registry.Unlock()
	id, ok := registry.byName[name]
	return id, ok
}

// State describes the result of a scheduler run.
type State uint8

const (
	Quiescent State = iota
	Completed
	Failed
	Deadlocked
	Killed
)

// KillPendingError reports an isolate task that has not stopped after a kill
// request. The prototype cannot sample its OS thread or stack, so ThreadID and
// Stack are empty. GoroutineID is the task holding the scheduling baton, if
// known. Revocation remains in force after this error.
type KillPendingError struct {
	GoroutineID uint64
	ThreadID    int64
	Stack       string
}

func (e *KillPendingError) Error() string {
	if e.GoroutineID == 0 {
		return "isolateproto: kill pending: isolate tasks have not stopped"
	}
	return fmt.Sprintf("isolateproto: kill pending: task %d has not stopped", e.GoroutineID)
}

// Command is one outstanding host call. Payload belongs to the caller.
type Command struct {
	ID      uint64
	Op      uint32
	Payload []byte
}

// Event answers a call, or sends an unsolicited Inbox message when ID is 0.
// Err is copied as text before delivery to a task.
type Event struct {
	ID      uint64
	Payload []byte
	Err     error
}

// Config is the input to one prototype instance. Limits and capabilities
// belong to later runtime phases and are deliberately absent here.
type Config struct {
	Entry   Entry
	Input   []byte
	Clock   time.Time // initial host-injected logical time
	Seed    uint64    // deterministic source for Task.RandUint64
	TimerOp uint32    // SDK-owned operation code used by Task.Sleep; zero disables Sleep
}

type requestKind uint8

const (
	reqYield requestKind = iota
	reqGo
	reqCall
	reqInbox
	reqSend
	reqRecv
	reqClose
	reqSelectRecv
	reqFinish
)

type request struct {
	from     uint64
	kind     requestKind
	fn       func(*Task)
	op       uint32
	payload  []byte
	err      error
	panicV   any
	channel  *channelState
	channels []*channelState
	value    any
}

type reply struct {
	payload []byte
	err     error
	value   any
	ok      bool
	index   int
}

type taskRecord struct {
	task           *Task
	next           reply
	waiting        uint64 // nonzero if parked in Call
	inbox          bool
	channel        bool
	selectChannels []*channelState
}

// Isolate is a cooperative execution instance. Resume calls are serialized.
type Isolate struct {
	mu        sync.Mutex
	killMu    sync.Mutex // protects liveTasks and admission of new tasks
	revoked   atomic.Bool
	current   atomic.Uint64 // task holding the baton, for pending diagnostics
	haltOnce  sync.Once
	stopOnce  sync.Once
	stopped   chan struct{} // closed after revocation and all tasks have exited
	liveTasks int
	entry     EntryFunc
	input     []byte
	requests  chan request
	halt      chan struct{}
	tasks     map[uint64]*taskRecord
	runnable  []uint64
	waiting   map[uint64]uint64 // call ID to task ID
	commands  map[uint64]Command
	inbox     [][]byte
	inboxQ    []uint64
	nextTask  uint64
	nextCall  uint64
	started   bool
	active    uint64 // task holding the baton across a canceled Resume
	state     State
	result    []byte
	err       error
	clock     time.Time
	random    uint64
	timerOp   uint32
}

// New validates an entry and copies its input.
func New(cfg Config) (*Isolate, error) {
	registry.Lock()
	var fn EntryFunc
	if cfg.Entry.id > 0 && cfg.Entry.id <= uint32(len(registry.funcs)) {
		fn = registry.funcs[cfg.Entry.id-1]
	}
	registry.Unlock()
	if fn == nil {
		return nil, errors.New("isolateproto: unregistered entry")
	}
	return &Isolate{
		entry: fn, input: clone(cfg.Input), requests: make(chan request), halt: make(chan struct{}), stopped: make(chan struct{}),
		tasks: make(map[uint64]*taskRecord), waiting: make(map[uint64]uint64),
		commands: make(map[uint64]Command), nextCall: 1,
		clock: cfg.Clock.Round(0), random: cfg.Seed, timerOp: cfg.TimerOp,
	}, nil
}

// SetClock injects logical time between Resume calls. Time cannot move back.
func (i *Isolate) SetClock(now time.Time) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.revoked.Load() {
		return errors.New("isolateproto: cannot set clock after kill")
	}
	if i.active != 0 {
		return errors.New("isolateproto: cannot set clock while a task is running")
	}
	now = now.Round(0)
	if !i.clock.IsZero() && now.Before(i.clock) {
		return errors.New("isolateproto: logical clock cannot move backwards")
	}
	i.clock = now
	return nil
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}

func (i *Isolate) start(fn func(*Task) ([]byte, error)) uint64 {
	i.killMu.Lock()
	defer i.killMu.Unlock()
	if i.revoked.Load() {
		return 0
	}
	i.nextTask++
	id := i.nextTask
	t := &Task{id: id, iso: i, permit: make(chan reply)}
	i.tasks[id] = &taskRecord{task: t}
	i.runnable = append(i.runnable, id)
	i.liveTasks++
	go func() {
		defer i.taskExited()
		select {
		case <-t.permit:
		case <-i.halt:
			return
		}
		if i.revoked.Load() {
			return
		}
		var out []byte
		var err error
		defer func() {
			req := request{from: id, kind: reqFinish, payload: clone(out), err: err, panicV: recover()}
			select {
			case i.requests <- req:
			case <-i.halt:
			}
		}()
		out, err = fn(t)
	}()
	return id
}

func (i *Isolate) taskExited() {
	i.killMu.Lock()
	defer i.killMu.Unlock()
	i.liveTasks--
	if i.revoked.Load() && i.liveTasks == 0 {
		i.stopOnce.Do(func() { close(i.stopped) })
	}
}

// Kill revokes the entire isolate and waits for every registered task to
// exit. Revocation is permanent even if ctx expires. A task that is running
// native code must reach a Task operation or return before it can stop.
// Concurrent Kill calls wait on the same revocation request.
func (i *Isolate) Kill(ctx context.Context) error {
	if ctx == nil {
		return errors.New("isolateproto: nil context")
	}
	i.killMu.Lock()
	i.revoked.Store(true)
	i.haltOnce.Do(func() { close(i.halt) })
	if i.liveTasks == 0 {
		i.stopOnce.Do(func() { close(i.stopped) })
	}
	i.killMu.Unlock()
	select {
	case <-i.stopped:
		return nil
	case <-ctx.Done():
		select {
		case <-i.stopped:
			return nil
		default:
		}
		return &KillPendingError{GoroutineID: i.current.Load()}
	}
}

// killed is called with i.mu held.
func (i *Isolate) killed() (State, []Command, error) {
	i.state = Killed
	i.err = nil
	return Killed, nil, nil
}

// Resume applies a batch of copied events, then runs registered tasks until
// none is runnable. Commands contains every still outstanding Call, ordered
// by call ID. A host must deduplicate commands by ID across repeated Resumes.
// Context cancellation returns without terminating the instance; a later
// Resume continues its active task. A context canceled before entry has no
// effect. Events accepted at entry are consumed even if cancellation occurs
// during the run, so the host must not submit that batch again. Cancellation
// cannot stop a native blocked or looping task.
func (i *Isolate) Resume(ctx context.Context, events []Event) (State, []Command, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if ctx == nil {
		return i.state, nil, errors.New("isolateproto: nil context")
	}
	if i.revoked.Load() {
		return i.killed()
	}
	if err := ctx.Err(); err != nil {
		return i.state, nil, err
	}
	if i.state == Completed || i.state == Failed || i.state == Deadlocked {
		if len(events) != 0 {
			return i.state, nil, errors.New("isolateproto: event after terminal state")
		}
		return i.state, nil, i.err
	}
	if err := i.validate(events); err != nil {
		return i.state, nil, err
	}
	if !i.started {
		i.started = true
		i.start(func(t *Task) ([]byte, error) { return i.entry(t, clone(i.input)) })
	}
	if i.revoked.Load() {
		return i.killed()
	}
	for _, ev := range events {
		if ev.ID == 0 {
			i.inbox = append(i.inbox, clone(ev.Payload))
			if len(i.inboxQ) > 0 {
				id := i.inboxQ[0]
				i.inboxQ = i.inboxQ[1:]
				i.deliverInbox(id)
			}
			continue
		}
		id := i.waiting[ev.ID]
		delete(i.waiting, ev.ID)
		delete(i.commands, ev.ID)
		rec := i.tasks[id]
		rec.waiting = 0
		rec.next = reply{payload: clone(ev.Payload), err: copyError(ev.Err)}
		i.runnable = append(i.runnable, id)
	}
	for i.active != 0 || len(i.runnable) > 0 {
		if i.revoked.Load() {
			return i.killed()
		}
		if err := ctx.Err(); err != nil {
			return i.state, nil, err
		}
		if i.active == 0 {
			id := i.runnable[0]
			i.runnable = i.runnable[1:]
			rec := i.tasks[id]
			if rec == nil {
				continue
			}
			i.current.Store(id)
			select {
			case rec.task.permit <- rec.next:
				rec.next = reply{}
				i.active = id
			case <-ctx.Done():
				i.current.Store(0)
				i.runnable = append([]uint64{id}, i.runnable...)
				return i.state, nil, ctx.Err()
			case <-i.halt:
				i.current.Store(0)
				return i.killed()
			}
		}
		var req request
		select {
		case req = <-i.requests:
		case <-ctx.Done():
			return i.state, nil, ctx.Err()
		case <-i.halt:
			return i.killed()
		}
		if i.revoked.Load() {
			return i.killed()
		}
		if req.from != i.active {
			return Failed, nil, fmt.Errorf("isolateproto: unregistered scheduling request from task %d", req.from)
		}
		i.active = 0
		i.current.Store(0)
		i.handle(req)
		if i.revoked.Load() {
			return i.killed()
		}
		if i.state == Failed {
			i.haltOnce.Do(func() { close(i.halt) })
			return Failed, nil, i.err
		}
	}
	if i.revoked.Load() {
		return i.killed()
	}
	if len(i.tasks) == 0 {
		i.state = Completed
		i.haltOnce.Do(func() { close(i.halt) })
		return Completed, nil, i.err
	}
	if len(i.waiting) == 0 && len(i.inboxQ) == 0 {
		i.state = Deadlocked
		i.err = errors.New("isolateproto: registered tasks deadlocked")
		i.haltOnce.Do(func() { close(i.halt) })
		return Deadlocked, nil, i.err
	}
	i.state = Quiescent
	return Quiescent, i.outstanding(), nil
}

func copyError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(err.Error())
}

func (i *Isolate) validate(events []Event) error {
	seen := make(map[uint64]bool, len(events))
	for _, ev := range events {
		if ev.ID == 0 {
			if ev.Err != nil {
				return errors.New("isolateproto: Inbox event cannot carry an error")
			}
			continue
		}
		if _, ok := i.waiting[ev.ID]; !ok || seen[ev.ID] {
			return fmt.Errorf("isolateproto: unknown or duplicate call ID %d", ev.ID)
		}
		seen[ev.ID] = true
	}
	return nil
}

func (i *Isolate) handle(req request) {
	rec := i.tasks[req.from]
	switch req.kind {
	case reqYield:
		i.runnable = append(i.runnable, req.from)
	case reqGo:
		i.start(func(t *Task) ([]byte, error) { req.fn(t); return nil, nil })
		i.runnable = append(i.runnable, req.from)
	case reqCall:
		if i.nextCall == math.MaxUint64 {
			i.state, i.err = Failed, errors.New("isolateproto: call ID exhausted")
			return
		}
		id := i.nextCall
		i.nextCall++
		rec.waiting = id
		i.waiting[id] = req.from
		i.commands[id] = Command{ID: id, Op: req.op, Payload: clone(req.payload)}
	case reqInbox:
		if len(i.inbox) == 0 {
			rec.inbox = true
			i.inboxQ = append(i.inboxQ, req.from)
		} else {
			i.deliverInbox(req.from)
		}
	case reqSend:
		i.send(req.from, req.channel, req.value)
	case reqRecv:
		i.receive(req.from, req.channel)
	case reqClose:
		i.closeChannel(req.from, req.channel)
	case reqSelectRecv:
		i.selectRecv(req.from, req.channels)
	case reqFinish:
		delete(i.tasks, req.from)
		if req.panicV != nil {
			i.state, i.err = Failed, fmt.Errorf("isolateproto: task panic: %v", req.panicV)
		} else if req.err != nil {
			i.state, i.err = Failed, copyError(req.err)
		} else if req.from == 1 {
			i.result = clone(req.payload)
		}
	}
}

func (i *Isolate) deliverInbox(id uint64) {
	rec := i.tasks[id]
	rec.inbox = false
	rec.next = reply{payload: i.inbox[0]}
	i.inbox = i.inbox[1:]
	i.runnable = append(i.runnable, id)
}

func (i *Isolate) outstanding() []Command {
	result := make([]Command, 0, len(i.commands))
	for _, cmd := range i.commands {
		cmd.Payload = clone(cmd.Payload)
		result = append(result, cmd)
	}
	sort.Slice(result, func(a, b int) bool { return result[a].ID < result[b].ID })
	return result
}

// Result returns a copy of the entry output once Completed.
func (i *Isolate) Result() ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.revoked.Load() || i.state != Completed {
		return nil, errors.New("isolateproto: result not available")
	}
	return clone(i.result), nil
}
