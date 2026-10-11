// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"bytes"
	"context"
	"internal/isolatebridge"
	"runtime"
	"testing"
	"time"
)

func priorityInstance(t *testing.T, main func(), clock ...time.Time) *Isolate {
	t.Helper()
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main:     main,
	}}
	config := Config{Program: program, Deterministic: true}
	if len(clock) != 0 {
		config.InitialTime, config.TimerOp = &clock[0], 2
	}
	instance, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := instance.Kill(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	return instance
}

func priorityCommand(t *testing.T, instance *Isolate) *Command {
	t.Helper()
	select {
	case command := <-instance.Commands():
		return command
	case <-time.After(5 * time.Second):
		t.Fatal("priority dispatch did not progress")
		return nil
	}
}

func TestPriorityFIFOAndOrdinaryChildren(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		instance := priorityInstance(t, func() {
			events := make(chan byte, 6)
			GoWithPriority(PriorityNormal, func() { events <- 10 })
			GoWithPriority(PriorityHigh, func() {
				events <- 1
				go func() { events <- 11 }()
				runtime.Gosched()
				events <- 2
			})
			GoWithPriority(PriorityHigh, func() {
				events <- 3
				runtime.Gosched()
				events <- 4
			})
			runtime.Gosched()
			var got []byte
			for range 6 {
				got = append(got, <-events)
			}
			_, _ = Call(99, got)
		})
		command := priorityCommand(t, instance)
		if command.Op != 99 || !bytes.Equal(command.Payload, []byte{1, 3, 2, 4, 10, 11}) {
			t.Fatalf("GOMAXPROCS=%d: priority/FIFO/child order = %v", procs, command.Payload)
		}
		command.Reply(nil, nil)
		if err := instance.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriorityAdmissionAndWakeup(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		instance := priorityInstance(t, func() {
			GoWithPriority(PriorityHigh, func() {
				_, _ = Call(2, nil)
				_, _ = Call(4, nil)
			})
			go func() {
				_, _ = Call(3, nil)
				_, _ = Call(6, nil)
			}()
			_, _ = Call(1, nil)
			_, _ = Call(5, nil)
		})
		commands := make(map[uint32]*Command)
		for range 3 {
			command := priorityCommand(t, instance)
			commands[command.Op] = command
		}
		if commands[1] == nil || commands[2] == nil || commands[3] == nil {
			t.Fatalf("unexpected startup commands: %v", commands)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		// Wake normal goroutines first, including the root.
		commands[1].Reply(nil, nil)
		commands[3].Reply(nil, nil)
		commands[2].Reply(nil, nil)
		for _, resume := range []func() error{instance.ResumeInbound, instance.ResumeReadOnly} {
			if err := resume(); err != nil {
				t.Fatal(err)
			}
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
			select {
			case command := <-instance.Commands():
				t.Fatalf("priority bypassed admission restrictions: %d", command.Op)
			default:
			}
		}
		if err := instance.ResumePriority(); err != nil {
			t.Fatal(err)
		}
		handler := priorityCommand(t, instance)
		if handler.Op != 4 {
			t.Fatalf("normal goroutine bypassed priority admission: %d", handler.Op)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		// Kill must release both queues even while ordinary work is fenced.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := instance.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
}

func TestPriorityChannelWakeupDoesNotPreempt(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		instance := priorityInstance(t, func() {
			wake := make(chan struct{})
			events := make(chan byte, 3)
			GoWithPriority(PriorityHigh, func() { <-wake; events <- 1 })
			go func() { events <- 3 }()
			runtime.Gosched()
			// The earlier ordinary child runs before the root gets its turn.
			// Queue another child before waking the blocked priority handler.
			go func() { events <- 4 }()
			wake <- struct{}{}
			events <- 2
			runtime.Gosched()
			var got []byte
			for range 4 {
				got = append(got, <-events)
			}
			_, _ = Call(99, got)
		})
		command := priorityCommand(t, instance)
		if !bytes.Equal(command.Payload, []byte{3, 2, 1, 4}) {
			t.Fatalf("GOMAXPROCS=%d: channel wakeup order = %v", procs, command.Payload)
		}
		command.Reply(nil, nil)
		if err := instance.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriorityNativeTimersAndOrdinaryCallbacks(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		for _, callback := range []bool{false, true} {
			instance := priorityInstance(t, func() {
				if callback {
					time.AfterFunc(time.Second, func() { _, _ = Call(3, nil) })
				} else {
					GoWithPriority(PriorityHigh, func() {
						timer := time.NewTimer(time.Second)
						<-timer.C
						_, _ = Call(3, nil)
					})
				}
				_, _ = Call(1, nil)
				_, _ = Call(4, nil)
			}, time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC))
			commands := make(map[uint32]*Command)
			for range 2 {
				command := priorityCommand(t, instance)
				commands[command.Op] = command
			}
			if commands[1] == nil || commands[2] == nil {
				t.Fatalf("unexpected timer startup: %v", commands)
			}
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
			commands[1].Reply(nil, nil)
			commands[2].Reply(nil, nil)
			if err := instance.ResumeInbound(); err != nil {
				t.Fatal(err)
			}
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
			select {
			case command := <-instance.Commands():
				t.Fatalf("timer admitted user code in delivery phase: %d", command.Op)
			default:
			}
			if err := instance.ResumePriority(); err != nil {
				t.Fatal(err)
			}
			if !callback {
				command := priorityCommand(t, instance)
				if command.Op != 3 {
					t.Fatalf("native timer did not wake priority handler: %d", command.Op)
				}
				if err := instance.Suspend(); err != nil {
					t.Fatal(err)
				}
				command.Reply(nil, nil)
				if err := instance.ResumePriority(); err != nil {
					t.Fatal(err)
				}
			}
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
			select {
			case command := <-instance.Commands():
				t.Fatalf("ordinary timer callback received priority: %d", command.Op)
			default:
			}
			if err := instance.Resume(); err != nil {
				t.Fatal(err)
			}
			root := priorityCommand(t, instance)
			if root.Op != 4 {
				t.Fatalf("ordinary callback bypassed earlier root wakeup: %d", root.Op)
			}
			if callback {
				command := priorityCommand(t, instance)
				if command.Op != 3 {
					t.Fatalf("callback did not run: %d", command.Op)
				}
				command.Reply(nil, nil)
			}
			root.Reply(nil, nil)
			if err := instance.Wait(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
