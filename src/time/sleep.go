// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import (
	"sync"
	"unsafe"
)

// Sleep pauses the current goroutine for at least the duration d.
// A negative or zero duration causes Sleep to return immediately.
//
//go:linkname Sleep
func Sleep(d Duration) {
	if d <= 0 {
		return
	}
	if runtime_isolateClockEnabled() {
		if err := runtime_isolateTimerSleep(int64(d)); err != nil {
			panic("time: durable Sleep failed: " + err.Error())
		}
		return
	}
	if runtime_isolateDeterministic() {
		panic("time: deterministic isolate requires a host clock")
	}
	runtimeSleep(d)
}

//go:linkname runtimeSleep time.runtimeSleep
func runtimeSleep(d Duration)

// syncTimer returns c as an unsafe.Pointer, for passing to newTimer.
func syncTimer(c chan Time) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&c))
}

// when is a helper function for setting the 'when' field of a runtimeTimer.
// It returns what the time will be, in nanoseconds, Duration d in the future.
// If d is negative, it is ignored. If the returned value would be less than
// zero because of an overflow, MaxInt64 is returned.
func when(d Duration) int64 {
	if d <= 0 {
		return runtimeNano()
	}
	t := runtimeNano() + int64(d)
	if t < 0 {
		// N.B. runtimeNano() and d are always positive, so addition
		// (including overflow) will never result in t == 0.
		t = 1<<63 - 1 // math.MaxInt64
	}
	return t
}

// These functions are pushed to package time from package runtime.

// The arg cp is a chan Time, but the declaration in runtime uses a pointer,
// so we use a pointer here too. This keeps some tools that aggressively
// compare linknamed symbol definitions happier.
//
//go:linkname newTimer
func newTimer(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) *Timer

//go:linkname stopTimer
func stopTimer(*Timer) bool

//go:linkname resetTimer
func resetTimer(t *Timer, when, period int64) bool

// Note: The runtime knows the layout of struct Timer, since newTimer allocates it.
// The runtime also knows that Ticker and Timer have the same layout.
// There are extra fields after the channel, reserved for the runtime
// and inaccessible to users.

// The Timer type represents a single event.
// When the Timer expires, the current time will be sent on C,
// unless the Timer was created by [AfterFunc].
// A Timer must be created with [NewTimer] or AfterFunc.
type Timer struct {
	C    <-chan Time
	self *Timer
	iso  *isolateTimer
}

type isolateTimer struct {
	mu         sync.Mutex
	c          chan Time
	f          func()
	generation uint64
	active     bool
}

func (state *isolateTimer) wait(d Duration, generation uint64) {
	if err := runtime_isolateTimerSleep(int64(d)); err != nil {
		panic("time: durable timer failed: " + err.Error())
	}
	state.fire(generation)
}

func (state *isolateTimer) fire(generation uint64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.active || state.generation != generation {
		return
	}
	state.active = false
	if state.c == nil {
		go state.f() // This waiter already runs under the isolate owner.
		return
	}
	select {
	case state.c <- Now():
	default:
	}
}

func (state *isolateTimer) stop() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	wasActive := state.active
	select {
	case <-state.c:
		// A buffered value has not yet been observed by the program. Undo
		// delivery and report it as active, matching Go's synchronous timers.
		wasActive = true
	default:
	}
	state.active = false
	state.generation++
	return wasActive
}

func (state *isolateTimer) reset(d Duration) bool {
	state.mu.Lock()
	wasActive := state.active
	state.generation++
	generation := state.generation
	state.active = true
	select {
	case <-state.c:
		wasActive = true
	default:
	}
	state.mu.Unlock()
	if d <= 0 && state.c != nil {
		state.fire(generation)
	} else {
		go state.wait(d, generation)
	}
	return wasActive
}

// A Timer must be created by NewTimer or AfterFunc and not copied.
func (t *Timer) checkValid(meth string) {
	if t.self == nil {
		panic("time: " + meth + " called on uninitialized Timer")
	} else if t.self != t {
		panic("time: " + meth + " called on copied Timer")
	}
}

// Stop prevents the [Timer] from firing.
// It returns true if the call stops the timer, false if the timer has already
// expired or been stopped.
//
// For a func-based timer created with [AfterFunc](d, f),
// if t.Stop returns false, then the timer has already expired
// and the function f has been started in its own goroutine;
// Stop does not wait for f to complete before returning.
// If the caller needs to know whether f is completed,
// it must coordinate with f explicitly.
//
// For a chan-based timer created with NewTimer(d), as of Go 1.23,
// any receive from t.C after Stop has returned is guaranteed to block
// rather than receive a stale time value from before the Stop;
// if the program has not received from t.C already and the timer is
// running, Stop is guaranteed to return true.
// Before Go 1.23, the only safe way to use Stop was insert an extra
// <-t.C if Stop returned false to drain a potential stale value.
// See the [NewTimer] documentation for more details.
func (t *Timer) Stop() bool {
	t.checkValid("Stop")
	if t.iso != nil {
		return t.iso.stop()
	}
	return stopTimer(t)
}

// NewTimer creates a new Timer that will send
// the current time on its channel after at least duration d.
//
// Before Go 1.23, the garbage collector did not recover
// timers that had not yet expired or been stopped, so code often
// immediately deferred t.Stop after calling NewTimer, to make
// the timer recoverable when it was no longer needed.
// As of Go 1.23, the garbage collector can recover unreferenced
// timers, even if they haven't expired or been stopped.
// The Stop method is no longer necessary to help the garbage collector.
// (Code may of course still want to call Stop to stop the timer for other reasons.)
//
// Before Go 1.23, the channel associated with a Timer was
// asynchronous (buffered, capacity 1), which meant that
// stale time values could be received even after [Timer.Stop]
// or [Timer.Reset] returned.
// As of Go 1.23, the channel is synchronous (unbuffered, capacity 0),
// eliminating the possibility of those stale values.
func NewTimer(d Duration) *Timer {
	if runtime_isolateClockEnabled() {
		c := make(chan Time, 1)
		runtime_isolateTimerChannel(syncTimer(c))
		state := &isolateTimer{c: c, generation: 1, active: true}
		t := &Timer{C: c, iso: state}
		t.self = t
		if d <= 0 {
			state.fire(1)
		} else {
			go state.wait(d, 1)
		}
		return t
	}
	c := make(chan Time, 1)
	t := newTimer(when(d), 0, sendTime, c, syncTimer(c))
	t.C = c
	return t
}

// Reset changes the timer to expire after duration d.
// It returns true if the timer had been active, false if the timer had
// expired or been stopped.
//
// For a func-based timer created with [AfterFunc](d, f), Reset either reschedules
// when f will run, in which case Reset returns true, or schedules f
// to run again, in which case it returns false.
// When Reset returns false, Reset neither waits for the prior f to
// complete before returning nor does it guarantee that the subsequent
// goroutine running f does not run concurrently with the prior
// one. If the caller needs to know whether the prior execution of
// f is completed, it must coordinate with f explicitly.
//
// For a chan-based timer created with NewTimer, as of Go 1.23,
// any receive from t.C after Reset has returned is guaranteed not
// to receive a time value corresponding to the previous timer settings;
// if the program has not received from t.C already and the timer is
// running, Reset is guaranteed to return true.
// Before Go 1.23, the only safe way to use Reset was to call [Timer.Stop]
// and explicitly drain the timer first.
// See the [NewTimer] documentation for more details.
func (t *Timer) Reset(d Duration) bool {
	t.checkValid("Reset")
	if t.iso != nil {
		return t.iso.reset(d)
	}
	w := when(d)
	return resetTimer(t, w, 0)
}

// sendTime does a non-blocking send of the current time on c.
func sendTime(c any, seq uintptr, delta int64) {
	// delta is how long ago the channel send was supposed to happen.
	// The current time can be arbitrarily far into the future, because the runtime
	// can delay a sendTime call until a goroutine tries to receive from
	// the channel. Subtract delta to go back to the old time that we
	// used to send.
	select {
	case c.(chan Time) <- Now().Add(Duration(-delta)):
	default:
	}
}

// After waits for the duration to elapse and then sends the current time
// on the returned channel.
// It is equivalent to [NewTimer](d).C.
//
// Before Go 1.23, this documentation warned that the underlying
// [Timer] would not be recovered by the garbage collector until the
// timer fired, and that if efficiency was a concern, code should use
// NewTimer instead and call [Timer.Stop] if the timer is no longer needed.
// As of Go 1.23, the garbage collector can recover unreferenced,
// unstopped timers. There is no reason to prefer NewTimer when After will do.
func After(d Duration) <-chan Time {
	return NewTimer(d).C
}

// AfterFunc waits for the duration to elapse and then calls f
// in its own goroutine. It returns a [Timer] that can
// be used to cancel the call using its Stop method.
// The returned Timer's C field is not used and will be nil.
func AfterFunc(d Duration, f func()) *Timer {
	if runtime_isolateClockEnabled() {
		state := &isolateTimer{f: f, generation: 1, active: true}
		t := &Timer{iso: state}
		t.self = t
		go state.wait(d, 1)
		return t
	}
	if runtime_isolateActive() {
		// The timer fires on a runtime goroutine. Its go statement would
		// start f without the isolate that registered it.
		panic("time: AfterFunc is unavailable inside an isolate")
	}
	return newTimer(when(d), 0, goFunc, f, nil)
}

//go:linkname runtime_isolateActive runtime.isolateActive
func runtime_isolateActive() bool

//go:linkname runtime_isolateDeterministic runtime.isolateDeterministic
func runtime_isolateDeterministic() bool

//go:linkname runtime_isolateTimerChannel runtime.isolateTimerChannel
func runtime_isolateTimerChannel(unsafe.Pointer)

//go:linkname runtime_isolateClockEnabled runtime.isolateClockEnabled
func runtime_isolateClockEnabled() bool

//go:linkname runtime_isolateTimerSleep runtime.isolateTimerSleep
func runtime_isolateTimerSleep(int64) error

func goFunc(arg any, seq uintptr, delta int64) {
	go arg.(func())()
}
