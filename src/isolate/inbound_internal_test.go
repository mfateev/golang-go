// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.isolates

package isolate

import (
	"context"
	"internal/isolatebridge"
	"runtime"
	"testing"
	"time"
)

func TestInboundDeliveryFencesApplication(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		program := Program{entry: isolatebridge.ProgramEntry{MetadataVersion: MetadataVersion,
			NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
			Main: func() {
				ch := make(chan byte, 32)
				GoInbound(func() {
					_, _ = Call(2, nil)
					done := make(chan struct{})
					go func() {
						defer close(done)
						for value := byte(1); value <= 32; value++ {
							ch <- value
							runtime.Gosched()
						}
					}()
					<-done
					_, _ = Call(3, nil)
				})
				_, _ = Call(1, nil)
				var got []byte
			drain:
				for {
					select {
					case value := <-ch:
						got = append(got, value)
					default:
						break drain
					}
				}
				_, _ = Call(4, got)
			},
		}}
		instance, err := New(Config{Program: program, Deterministic: true})
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
		next := func() *Command {
			select {
			case c := <-instance.Commands():
				return c
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch did not progress")
				return nil
			}
		}
		app := next()
		delivery := next()
		if app.Op != 1 || delivery.Op != 2 {
			t.Fatalf("startup ops %d, %d", app.Op, delivery.Op)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		// Wake the application first. Its FIFO position must not bypass admission.
		app.Reply(nil, nil)
		delivery.Reply(nil, nil)
		if err := instance.ResumeInbound(); err != nil {
			t.Fatal(err)
		}
		delivered := next()
		if delivered.Op != 3 {
			t.Fatalf("application ran before delivery: %d", delivered.Op)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		delivered.Reply(nil, nil)
		if err := instance.ResumeReadOnly(); err != nil {
			t.Fatal(err)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		select {
		case c := <-instance.Commands():
			t.Fatalf("read-only dispatch admitted mutable work: %d", c.Op)
		default:
		}
		if err := instance.ResumeInbound(); err != nil {
			t.Fatal(err)
		}
		if err := instance.Suspend(); err != nil {
			t.Fatal(err)
		}
		if err := instance.Resume(); err != nil {
			t.Fatal(err)
		}
		result := next()
		if result.Op != 4 || len(result.Payload) != 32 {
			t.Fatalf("partial drain: op=%d payload=%v", result.Op, result.Payload)
		}
		for index, value := range result.Payload {
			if value != byte(index+1) {
				t.Fatalf("delivery order = %v", result.Payload)
			}
		}
		result.Reply(nil, nil)
		if err := instance.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboundCannotAdmitReadOnlyServices(t *testing.T) {
	program := Program{entry: isolatebridge.ProgramEntry{MetadataVersion: MetadataVersion,
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			GoInbound(func() {
				go func() {
					_, _ = ReadOnlyCall(5, nil)
					_, _ = ReadOnlyCall(6, nil)
				}()
				_, _ = Call(2, nil)
				_, _ = Call(3, nil)
			})
			_, _ = Call(1, nil)
			_, _ = Call(4, nil)
		},
	}}
	instance, err := New(Config{Program: program, Deterministic: true})
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
	next := func() *Command {
		select {
		case command := <-instance.Commands():
			return command
		case <-time.After(5 * time.Second):
			t.Fatal("dispatch did not progress")
			return nil
		}
	}
	requests := make(map[uint32]*Command)
	for range 3 {
		command := next()
		requests[command.Op] = command
	}
	if err := instance.Suspend(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []uint32{1, 5, 2} {
		requests[op].Reply(nil, nil)
	}
	if err := instance.ResumeInbound(); err != nil {
		t.Fatal(err)
	}
	delivered := next()
	if delivered.Op != 3 {
		t.Fatalf("inbound dispatch admitted a query or application: %d", delivered.Op)
	}
	if err := instance.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := instance.ResumeReadOnly(); err != nil {
		t.Fatal(err)
	}
	query := next()
	if query.Op != 6 {
		t.Fatalf("read-only dispatch admitted mutable work: %d", query.Op)
	}
	if err := instance.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := instance.Resume(); err != nil {
		t.Fatal(err)
	}
	app := next()
	if app.Op != 4 {
		t.Fatalf("application dispatch = %d", app.Op)
	}
	app.Reply(nil, nil)
	if err := instance.Wait(); err != nil {
		t.Fatal(err)
	}
}
