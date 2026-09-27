// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolateproto

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// These operation numbers are wire values. Never derive them from iota.
const (
	opCharge = uint32(10)
	opShip   = uint32(11)
	opTimer  = uint32(12)
	opChild  = uint32(13)
)

type orderRequest struct {
	Card  string
	Items []string
}

type orderReceipt struct {
	ChargeID string
	Shipped  []string
	ChildID  string
	Signal   string
}

type shipResult struct {
	index int
	name  string
	err   error
}

var orderEntry = RegisterJSON("isolateproto.test.order", func(t *Task, req orderRequest) (orderReceipt, error) {
	charged, err := CallJSON[string, string](t, opCharge, req.Card)
	if err != nil {
		return orderReceipt{}, err
	}
	shipped := make([]string, len(req.Items))
	results := NewChannel[shipResult](t, 0)
	for index, item := range req.Items {
		index, item := index, item
		t.Go(func(child *Task) {
			name, err := CallJSON[string, string](child, opShip, item)
			_ = results.Send(child, shipResult{index: index, name: name, err: err})
		})
	}
	for range req.Items {
		r, ok := results.Receive(t)
		if !ok {
			return orderReceipt{}, fmt.Errorf("ship results closed")
		}
		if r.err != nil {
			return orderReceipt{}, r.err
		}
		shipped[r.index] = r.name
	}
	if err := t.Sleep(24 * time.Hour); err != nil {
		return orderReceipt{}, err
	}
	childID, err := CallJSON[string, string](t, opChild, charged)
	if err != nil {
		return orderReceipt{}, err
	}
	return orderReceipt{ChargeID: charged, Shipped: shipped, ChildID: childID, Signal: string(t.Inbox())}, nil
})

func TestOrderHostLoop(t *testing.T) {
	input, err := json.Marshal(orderRequest{Card: "card", Items: []string{"apple", "pear"}})
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := Lookup("isolateproto.test.order")
	if !ok || entry != orderEntry {
		t.Fatal("entry lookup failed")
	}
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	iso := newTestIsolate(t, Config{Entry: entry, Input: input, Clock: start, Seed: 17, TimerOp: opTimer})
	input[0] = 'X' // New copied the input

	state, commands := resume(t, iso)
	if state != Quiescent || len(commands) != 1 || commands[0].Op != opCharge {
		t.Fatalf("charge: state=%v commands=%v", state, commands)
	}
	state, commands = resume(t, iso, Event{ID: commands[0].ID, Payload: []byte(`"charged"`)})
	if state != Quiescent || len(commands) != 2 || commands[0].Op != opShip || commands[1].Op != opShip {
		t.Fatalf("fanout: state=%v commands=%v", state, commands)
	}
	state, commands = resume(t, iso,
		Event{ID: commands[1].ID, Payload: []byte(`"pear-shipped"`)},
		Event{ID: commands[0].ID, Payload: []byte(`"apple-shipped"`)},
	)
	if state != Quiescent || len(commands) != 1 || commands[0].Op != opTimer {
		t.Fatalf("timer: state=%v commands=%v", state, commands)
	}
	if err := iso.SetClock(start.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, commands = resume(t, iso, Event{ID: commands[0].ID})
	if state != Quiescent || len(commands) != 1 || commands[0].Op != opChild {
		t.Fatalf("child workflow: state=%v commands=%v", state, commands)
	}
	state, commands = resume(t, iso, Event{ID: commands[0].ID, Payload: []byte(`"child-1"`)})
	if state != Quiescent || len(commands) != 0 {
		t.Fatalf("signal wait: state=%v commands=%v", state, commands)
	}
	state, commands, err = iso.Resume(context.Background(), []Event{{ID: 0, Payload: []byte("approved")}})
	if err != nil || state != Completed || len(commands) != 0 {
		t.Fatalf("done: state=%v commands=%v err=%v", state, commands, err)
	}
	output, err := iso.Result()
	if err != nil {
		t.Fatal(err)
	}
	var got orderReceipt
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	want := orderReceipt{ChargeID: "charged", Shipped: []string{"apple-shipped", "pear-shipped"}, ChildID: "child-1", Signal: "approved"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result=%+v want=%+v", got, want)
	}
}
