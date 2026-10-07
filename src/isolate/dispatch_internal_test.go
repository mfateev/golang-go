// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"internal/isolatebridge"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDeterministicSuspendBatchReplies(t *testing.T) {
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			var wg sync.WaitGroup
			wg.Add(4)
			for op := uint32(1); op <= 4; op++ {
				go func() {
					defer wg.Done()
					_, _ = Call(op, nil)
					_, _ = Call(op+100, nil)
				}()
			}
			wg.Wait()
		},
	}}
	i, err := New(Config{Program: program, Deterministic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := i.Kill(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	next := func() *Command {
		select {
		case c := <-i.Commands():
			return c
		case <-deadline:
			t.Fatal("dispatch did not progress")
			return nil
		}
	}
	var commands []*Command
	for op := uint32(1); op <= 4; op++ {
		c := next()
		if c.Op != op {
			t.Fatalf("creation order = %d, want %d", c.Op, op)
		}
		commands = append(commands, c)
	}
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	for index := 3; index >= 0; index-- {
		commands[index].Reply(nil, nil)
	}
	select {
	case c := <-i.Commands():
		t.Fatalf("ran while suspended: %d", c.Op)
	default:
	}
	if err := i.Resume(); err != nil {
		t.Fatal(err)
	}
	for op := uint32(104); op >= 101; op-- {
		c := next()
		if c.Op != op {
			t.Fatalf("reply order = %d, want %d", c.Op, op)
		}
		c.Reply(nil, nil)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicInitializerCannotRequestTimer(t *testing.T) {
	clock := time.Unix(0, 0)
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			time.Sleep(time.Hour)
			return func(fn func()) { fn() }, nil
		},
		Main: func() {},
	}}
	instance, err := New(Config{Program: program, Deterministic: true, InitialTime: &clock, TimerOp: 77})
	failure, ok := err.(*PanicError)
	if instance != nil || !ok || failure.Phase != "initialization" || failure.Message != "isolate: Call outside an active isolate" {
		t.Fatalf("New=(%v,%v)", instance, err)
	}
}

// Revocation must release a host suspension waiter and drain runnable members
// even when the host has fenced dispatch after replying to their Calls.
func TestDeterministicSuspendRevocation(t *testing.T) {
	for _, mode := range []string{"paused", "running"} {
		t.Run(mode, func(t *testing.T) {
			program := Program{entry: isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main: func() {
					if mode == "running" {
						for {
							runtime.Gosched()
						}
					}
					_, _ = Call(1, nil)
					var never chan struct{}
					<-never
				},
			}}
			i, err := New(Config{Program: program, Deterministic: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := i.Start(); err != nil {
				t.Fatal(err)
			}
			var suspended chan error
			if mode == "paused" {
				command := <-i.Commands()
				if err := i.Suspend(); err != nil {
					t.Fatal(err)
				}
				command.Reply(nil, nil) // This runnable continuation stays in the FIFO.
			} else {
				suspended = make(chan error, 1)
				go func() { suspended <- i.Suspend() }()
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := i.Kill(ctx); err != nil {
				t.Fatal(err)
			}
			if suspended != nil {
				select {
				case err := <-suspended:
					if err == nil {
						t.Fatal("revoked suspension returned nil")
					}
				case <-ctx.Done():
					t.Fatal("revocation leaked suspension waiter")
				}
			}
			if i.boundary.LiveGoroutines() != 0 {
				t.Fatal("revocation retained group members")
			}
		})
	}
}
