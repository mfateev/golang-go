// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"fmt"
	"internal/isolatebridge"
	"strings"
	"testing"
	"time"
)

func TestLoggingWithoutHostReceipt(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var initial []LogRecord
		program := lifecycleProgram(func() {
			if n, err := fmt.Print("formatted"); n != 9 || err != nil {
				panic("printing changed byte count or returned a sink error")
			}
			fmt.Print("last")
		})
		program.entry.NewState = func() (func(func()), error) {
			fmt.Print("first initializer")
			fmt.Print("last initializer")
			return func(fn func()) { fn() }, nil
		}
		i, err := New(Config{Program: program, Deterministic: deterministic, LogHandler: func(record LogRecord) {
			initial = append(initial, record)
		}})
		if err != nil {
			t.Fatal(err)
		}
		if len(initial) != 2 || initial[0].Message != "first initializer" || initial[1].Message != "last initializer" {
			t.Fatalf("initializer writes not drained: %+v", initial)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		// Every print must return even if the host services neither stream.
		if err := lifecycleWait(t, i); err != nil {
			t.Fatal(err)
		}
		for _, expected := range []struct{ source, message string }{
			{"fmt", "formatted"}, {"fmt", "last"},
		} {
			message := nextLogMessage(t, i.Writes())
			if message.Op != LogOp {
				t.Fatalf("wrong logging operation: %d", message.Op)
			}
			record, err := DecodeLog(message.Payload)
			if err != nil || record.Source != expected.source || !strings.Contains(record.Message, expected.message) {
				t.Fatalf("log=%+v error=%v want=%+v", record, err, expected)
			}
		}
		if i.PendingCalls() != 0 || i.DroppedWrites() != 0 {
			t.Fatal("printing required replies or lost a short record")
		}
	}
}

func TestLoggingReadOnlyWithoutHostReceipt(t *testing.T) {
	b := isolatebridge.New()
	if !b.ConfigureLogging() {
		t.Fatal("logging configuration failed")
	}
	if err := b.EnableDeterminism(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(func() {
			_, _ = b.ReadOnlyCall(1, nil)
			fmt.Print("read-only query")
			fmt.Print("read-only final")
		})
	}()
	command := <-b.Commands()
	if err := b.Suspend(); err != nil {
		t.Fatal(err)
	}
	command.Reply(nil, nil)
	if err := b.ResumeReadOnly(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read-only logging waited for a host reply")
	}
	for _, source := range []string{"fmt", "fmt"} {
		message := nextLogMessage(t, b.Writes())
		record, err := DecodeLog(message.Payload)
		if err != nil || record.Source != source || !strings.Contains(record.Message, "read-only") {
			t.Fatalf("read-only log=%+v error=%v", record, err)
		}
	}
	if b.PendingCalls() != 0 {
		t.Fatal("read-only logging created a pending Call")
	}
}

func nextLogMessage(t *testing.T, messages <-chan *Message) *Message {
	t.Helper()
	select {
	case message := <-messages:
		return message
	default:
		t.Fatal("missing log after writer finished")
		return nil
	}
}
