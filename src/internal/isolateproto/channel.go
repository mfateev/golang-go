package isolateproto

import "errors"

// ErrClosed reports a send or close on a closed scheduler channel.
var ErrClosed = errors.New("isolateproto: channel closed")

type sender struct {
	task  uint64
	value any
}

type receiver struct {
	task  uint64
	index int
}

type channelState struct {
	owner     *Isolate
	capacity  int
	buffer    []any
	senders   []sender
	receivers []receiver
	closed    bool
}

// Channel is a scheduler-aware channel. The prototype supports buffered and
// unbuffered channels, but native channel operations remain untracked.
type Channel[T any] struct{ state *channelState }

// NewChannel creates an isolate-owned channel. The calling Task must hold the
// execution baton, and capacity must be nonnegative.
func NewChannel[T any](t *Task, capacity int) *Channel[T] {
	if t == nil || capacity < 0 {
		panic("isolateproto: invalid channel owner or capacity")
	}
	return &Channel[T]{state: &channelState{owner: t.iso, capacity: capacity}}
}

func (ch *Channel[T]) check(t *Task) *channelState {
	if ch == nil || ch.state == nil || t == nil || ch.state.owner != t.iso {
		panic("isolateproto: channel used outside its isolate")
	}
	return ch.state
}

// Send blocks through the scheduler until the value is accepted.
func (ch *Channel[T]) Send(t *Task, value T) error {
	return t.exchange(request{kind: reqSend, channel: ch.check(t), value: value}).err
}

// Receive blocks through the scheduler, returning false after Close and drain.
func (ch *Channel[T]) Receive(t *Task) (T, bool) {
	r := t.exchange(request{kind: reqRecv, channel: ch.check(t)})
	if !r.ok {
		var zero T
		return zero, false
	}
	if r.value == nil {
		var zero T
		return zero, true
	}
	return r.value.(T), true
}

// Close wakes all blocked receivers and senders.
func (ch *Channel[T]) Close(t *Task) error {
	return t.exchange(request{kind: reqClose, channel: ch.check(t)}).err
}

// SelectReceive receives from one of several channels. If several cases are
// ready, the lowest argument index wins. This is a deliberate, deterministic
// Phase 1 rule; it does not emulate native select's random ready-case choice.
// An empty case list blocks forever and is classified as deadlock when no
// host-visible work remains.
func SelectReceive[T any](t *Task, channels ...*Channel[T]) (int, T, bool) {
	states := make([]*channelState, len(channels))
	for j, ch := range channels {
		states[j] = ch.check(t)
	}
	r := t.exchange(request{kind: reqSelectRecv, channels: states})
	if !r.ok {
		var zero T
		return r.index, zero, false
	}
	if r.value == nil {
		var zero T
		return r.index, zero, true
	}
	return r.index, r.value.(T), true
}

func (i *Isolate) wake(id uint64, r reply) {
	rec := i.tasks[id]
	rec.next = r
	rec.channel = false
	i.runnable = append(i.runnable, id)
}

func (i *Isolate) removeReceivers(id uint64) {
	// The receiver can be registered on multiple select cases, including the
	// same channel more than once. Remove all copies before it runs again.
	rec := i.tasks[id]
	for _, ch := range rec.selectChannels {
		filtered := ch.receivers[:0]
		for _, w := range ch.receivers {
			if w.task != id {
				filtered = append(filtered, w)
			}
		}
		ch.receivers = filtered
	}
	rec.selectChannels = nil
}

func (i *Isolate) wakeReceiver(ch *channelState, value any, ok bool) {
	w := ch.receivers[0]
	i.removeReceivers(w.task)
	i.wake(w.task, reply{value: value, ok: ok, index: w.index})
}

func (i *Isolate) send(id uint64, ch *channelState, value any) {
	if ch.closed {
		i.wake(id, reply{err: ErrClosed})
		return
	}
	if len(ch.receivers) > 0 {
		i.wakeReceiver(ch, value, true)
		i.wake(id, reply{})
		return
	}
	if len(ch.buffer) < ch.capacity {
		ch.buffer = append(ch.buffer, value)
		i.wake(id, reply{})
		return
	}
	i.tasks[id].channel = true
	ch.senders = append(ch.senders, sender{task: id, value: value})
}

func (i *Isolate) receive(id uint64, ch *channelState) {
	if len(ch.buffer) > 0 {
		value := ch.buffer[0]
		ch.buffer = ch.buffer[1:]
		if len(ch.senders) > 0 {
			s := ch.senders[0]
			ch.senders = ch.senders[1:]
			ch.buffer = append(ch.buffer, s.value)
			i.wake(s.task, reply{})
		}
		i.wake(id, reply{value: value, ok: true})
		return
	}
	if len(ch.senders) > 0 {
		s := ch.senders[0]
		ch.senders = ch.senders[1:]
		i.wake(s.task, reply{})
		i.wake(id, reply{value: s.value, ok: true})
		return
	}
	if ch.closed {
		i.wake(id, reply{})
		return
	}
	rec := i.tasks[id]
	rec.channel = true
	rec.selectChannels = []*channelState{ch}
	ch.receivers = append(ch.receivers, receiver{task: id})
}

func (i *Isolate) selectRecv(id uint64, channels []*channelState) {
	for index, ch := range channels {
		if len(ch.buffer) > 0 || len(ch.senders) > 0 || ch.closed {
			i.receive(id, ch)
			i.tasks[id].next.index = index
			return
		}
	}
	rec := i.tasks[id]
	rec.channel = true
	rec.selectChannels = channels
	for index, ch := range channels {
		ch.receivers = append(ch.receivers, receiver{task: id, index: index})
	}
}

func (i *Isolate) closeChannel(id uint64, ch *channelState) {
	if ch.closed {
		i.wake(id, reply{err: ErrClosed})
		return
	}
	ch.closed = true
	for len(ch.receivers) > 0 {
		i.wakeReceiver(ch, nil, false)
	}
	for _, s := range ch.senders {
		i.wake(s.task, reply{err: ErrClosed})
	}
	ch.senders = nil
	i.wake(id, reply{})
}
