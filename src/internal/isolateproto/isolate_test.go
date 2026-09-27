package isolateproto

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

var fanoutEntry = Register("isolateproto.test.fanout", func(task *Task, input []byte) ([]byte, error) {
	results := NewChannel[string](task, 0)
	for _, name := range []string{"first", "second"} {
		name := name
		task.Go(func(t *Task) {
			answer, err := t.Call(7, []byte(name))
			if err != nil {
				panic(err)
			}
			if err := results.Send(t, string(answer)); err != nil {
				panic(err)
			}
		})
	}
	var output string
	for range 2 {
		value, ok := results.Receive(task)
		if !ok {
			return nil, errors.New("unexpected closed channel")
		}
		output += value
	}
	return []byte(output), nil
})

var inboxEntry = Register("isolateproto.test.inbox", func(t *Task, _ []byte) ([]byte, error) {
	return t.Inbox(), nil
})

var deadlockEntry = Register("isolateproto.test.deadlock", func(t *Task, _ []byte) ([]byte, error) {
	ch := NewChannel[int](t, 0)
	ch.Receive(t)
	return nil, nil
})

var timerEntry = Register("isolateproto.test.timer", func(t *Task, _ []byte) ([]byte, error) {
	before := t.Now()
	if err := t.Sleep(3 * time.Hour); err != nil {
		return nil, err
	}
	return []byte(t.Now().Sub(before).String()), nil
})

var selectEntry = Register("isolateproto.test.select", func(t *Task, _ []byte) ([]byte, error) {
	a, b := NewChannel[int](t, 1), NewChannel[int](t, 1)
	if err := b.Send(t, 22); err != nil {
		return nil, err
	}
	if err := a.Send(t, 11); err != nil {
		return nil, err
	}
	index, value, ok := SelectReceive(t, a, b)
	return []byte(fmt.Sprintf("%d:%d:%t", index, value, ok)), nil
})

var nilChannelEntry = Register("isolateproto.test.nil-channel", func(t *Task, _ []byte) ([]byte, error) {
	ch := NewChannel[any](t, 0)
	t.Go(func(child *Task) {
		if err := ch.Send(child, nil); err != nil {
			panic(err)
		}
	})
	value, ok := ch.Receive(t)
	if !ok || value != nil {
		return nil, errors.New("nil value was not delivered")
	}
	if err := ch.Close(t); err != nil {
		return nil, err
	}
	_, ok = ch.Receive(t)
	if ok || !errors.Is(ch.Send(t, nil), ErrClosed) {
		return nil, errors.New("closed channel semantics broken")
	}
	return []byte("ok"), nil
})

var randomEntry = Register("isolateproto.test.random", func(t *Task, _ []byte) ([]byte, error) {
	first := t.RandUint64()
	t.Yield()
	second := t.RandUint64()
	return []byte(fmt.Sprintf("%d/%d", first, second)), nil
})

var cancellationID atomic.Uint64

func newTestIsolate(t *testing.T, cfg Config) *Isolate {
	t.Helper()
	iso, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return iso
}

func resume(t *testing.T, iso *Isolate, events ...Event) (State, []Command) {
	t.Helper()
	state, cmds, err := iso.Resume(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	return state, cmds
}

func TestFanoutReplayAndCopyBoundary(t *testing.T) {
	var baseline []Command
	for run := range 20 {
		iso := newTestIsolate(t, Config{Entry: fanoutEntry})
		state, cmds := resume(t, iso)
		if state != Quiescent || len(cmds) != 2 || cmds[0].ID != 1 || cmds[1].ID != 2 {
			t.Fatalf("run %d: state=%v commands=%v", run, state, cmds)
		}
		if !reflect.DeepEqual([]string{string(cmds[0].Payload), string(cmds[1].Payload)}, []string{"first", "second"}) {
			t.Fatalf("run %d: wrong command order: %v", run, cmds)
		}
		if run == 0 {
			baseline = []Command{
				{ID: cmds[0].ID, Op: cmds[0].Op, Payload: clone(cmds[0].Payload)},
				{ID: cmds[1].ID, Op: cmds[1].Op, Payload: clone(cmds[1].Payload)},
			}
		} else if !reflect.DeepEqual(baseline, cmds) {
			t.Fatalf("run %d: command replay mismatch: %v vs %v", run, baseline, cmds)
		}
		cmds[0].Payload[0] = 'X' // host mutation cannot reach the stored command
		_, again := resume(t, iso)
		if string(again[0].Payload) != "first" {
			t.Fatalf("host mutated isolate command: %q", again[0].Payload)
		}
		payload := []byte("B")
		state, cmds = resume(t, iso, Event{ID: 2, Payload: payload}, Event{ID: 1, Payload: []byte("A")})
		payload[0] = 'X' // host mutation after Resume cannot reach the task
		if state != Completed || len(cmds) != 0 {
			t.Fatalf("run %d: final state=%v commands=%v", run, state, cmds)
		}
		got, err := iso.Result()
		if err != nil || string(got) != "BA" {
			t.Fatalf("run %d: result=%q err=%v", run, got, err)
		}
		got[0] = 'X'
		againResult, _ := iso.Result()
		if string(againResult) != "BA" {
			t.Fatalf("host mutated isolate result: %q", againResult)
		}
		if run%2 == 0 {
			runtime.GC()
		}
	}
}

func TestInboxAndEventValidation(t *testing.T) {
	iso := newTestIsolate(t, Config{Entry: inboxEntry})
	state, cmds := resume(t, iso)
	if state != Quiescent || len(cmds) != 0 {
		t.Fatalf("state=%v commands=%v", state, cmds)
	}
	if _, _, err := iso.Resume(context.Background(), []Event{{ID: 99}}); err == nil {
		t.Fatal("unknown reply accepted")
	}
	message := []byte("signal")
	state, _ = resume(t, iso, Event{Payload: message})
	message[0] = 'X'
	if state != Completed {
		t.Fatalf("state=%v", state)
	}
	got, _ := iso.Result()
	if string(got) != "signal" {
		t.Fatalf("result=%q", got)
	}
}

func TestDeadlockAndSelect(t *testing.T) {
	iso := newTestIsolate(t, Config{Entry: deadlockEntry})
	state, _, err := iso.Resume(context.Background(), nil)
	if state != Deadlocked || err == nil {
		t.Fatalf("state=%v err=%v", state, err)
	}
	iso = newTestIsolate(t, Config{Entry: selectEntry})
	state, _ = resume(t, iso)
	if state != Completed {
		t.Fatalf("select state=%v", state)
	}
	got, _ := iso.Result()
	if string(got) != "0:11:true" {
		t.Fatalf("select result=%q", got)
	}
	iso = newTestIsolate(t, Config{Entry: nilChannelEntry})
	state, _ = resume(t, iso)
	if state != Completed {
		t.Fatalf("nil channel state=%v", state)
	}
}

func TestTimerAndClock(t *testing.T) {
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	iso := newTestIsolate(t, Config{Entry: timerEntry, Clock: start, TimerOp: 42})
	state, cmds := resume(t, iso)
	if state != Quiescent || len(cmds) != 1 || cmds[0].Op != 42 || time.Duration(binary.LittleEndian.Uint64(cmds[0].Payload)) != 3*time.Hour {
		t.Fatalf("state=%v commands=%v", state, cmds)
	}
	if err := iso.SetClock(start.Add(-time.Hour)); err == nil {
		t.Fatal("clock moved backwards")
	}
	if err := iso.SetClock(start.Add(3 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, _ = resume(t, iso, Event{ID: cmds[0].ID})
	if state != Completed {
		t.Fatalf("state=%v", state)
	}
	got, _ := iso.Result()
	if string(got) != "3h0m0s" {
		t.Fatalf("result=%q", got)
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]int{"z": 1, "a": 2, "m": 3})
	if !reflect.DeepEqual(got, []string{"a", "m", "z"}) {
		t.Fatal(got)
	}
}

func TestSeededRandomReplay(t *testing.T) {
	var baseline string
	for index, seed := range []uint64{17, 17, 18} {
		iso := newTestIsolate(t, Config{Entry: randomEntry, Seed: seed})
		state, _ := resume(t, iso)
		if state != Completed {
			t.Fatalf("state=%v", state)
		}
		output, _ := iso.Result()
		if index == 0 {
			baseline = string(output)
		} else if (string(output) == baseline) != (index == 1) {
			t.Fatalf("seed %d yielded %q, baseline %q", seed, output, baseline)
		}
	}
}

func TestCanceledResumePreservesActiveTask(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	entry := Register(fmt.Sprintf("isolateproto.test.canceled-resume.%d", cancellationID.Add(1)), func(_ *Task, _ []byte) ([]byte, error) {
		close(started)
		<-release // unsupported native blocking, released by the test
		return []byte("done"), nil
	})
	iso := newTestIsolate(t, Config{Entry: entry})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, _, err := iso.Resume(ctx, nil)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("entry did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Resume err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled Resume did not return")
	}
	if err := iso.SetClock(time.Now()); err == nil {
		t.Fatal("clock changed while task still active")
	}
	close(release)
	state, _ := resume(t, iso)
	if state != Completed {
		t.Fatalf("state=%v", state)
	}
}

func TestAlreadyCanceledResumeDoesNotStart(t *testing.T) {
	iso := newTestIsolate(t, Config{Entry: inboxEntry})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := iso.Resume(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if iso.started {
		t.Fatal("canceled Resume started the entry")
	}
	state, _ := resume(t, iso)
	if state != Quiescent {
		t.Fatalf("state=%v", state)
	}
	state, _ = resume(t, iso, Event{ID: 0, Payload: []byte("ok")})
	if state != Completed {
		t.Fatalf("state=%v", state)
	}
}
