// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"errors"
	"internal/isolatebridge"
	"isolate"
	"strings"
	"testing"
)

func TestHostCallsFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func()
	}{
		{"Call", func() { _, _ = isolate.Call(1, []byte("input")) }},
		{"Inbox", func() { _ = isolate.Inbox() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(got.(string), "outside an active isolate") {
					t.Errorf("panic = %v, want outside-an-isolate failure", got)
				}
			}()
			tt.call()
		})
	}
}

func TestCallAndInbox(t *testing.T) {
	initial := []byte("initial")
	b := isolatebridge.New(initial)
	initial[0] = 'X'

	type result struct {
		initial string
		second  string
		reply   string
		err     error
	}
	done := make(chan result, 1)
	go b.Run(func() {
		first := <-isolate.Inbox()
		ready := make(chan string, 1)
		go func() {
			// This native child must inherit the same boundary.
			ready <- string(<-isolate.Inbox())
		}()
		second := <-ready
		reply, err := isolate.Call(7, []byte("request"))
		done <- result{string(first), second, string(reply), err}
	})

	second := []byte("second")
	b.Send(second)
	second[0] = 'X'
	cmd := <-b.Commands()
	if cmd.ID != 1 || cmd.Op != 7 || string(cmd.Payload) != "request" {
		t.Fatalf("command = %+v", cmd)
	}
	reply := []byte("reply")
	cmd.Reply(reply, nil)
	reply[0] = 'X'
	got := <-done
	if got != (result{initial: "initial", second: "second", reply: "reply"}) {
		t.Fatalf("result = %+v", got)
	}
}

func TestConcurrentCallsKeepTheirReplies(t *testing.T) {
	b := isolatebridge.New(nil)
	done := make(chan string, 2)
	b.Run(func() {
		for i := range 2 {
			go func() {
				response, err := isolate.Call(uint32(i+1), nil)
				if err != nil {
					done <- err.Error()
					return
				}
				done <- string(response)
			}()
		}
	})
	first := <-b.Commands()
	second := <-b.Commands()
	if first.ID == second.ID || first.Op == second.Op {
		t.Fatalf("commands not distinct: %+v, %+v", first, second)
	}
	second.Reply([]byte("second"), nil)
	first.Reply([]byte("first"), errors.New("host error"))
	a, z := <-done, <-done
	if !(a == "second" && z == "host error" || a == "host error" && z == "second") {
		t.Fatalf("replies = %q, %q", a, z)
	}
}
