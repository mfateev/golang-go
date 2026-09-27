package isolateproto

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// This fixture is meant to run unchanged on each supported architecture.
// Its command bytes, IDs, seeded randomness, and logical time are fixed.
// It exercises only Phase 1's explicit deterministic primitives.
var archReplayEntry = Register("isolateproto.test.arch-replay", func(t *Task, _ []byte) ([]byte, error) {
	values := map[int]string{11: "eleven", -7: "negative", 2: "two"}
	for _, key := range SortedKeys(values) {
		if _, err := t.Call(81, []byte(fmt.Sprintf("%d=%s", key, values[key]))); err != nil {
			return nil, err
		}
	}
	return []byte(fmt.Sprintf("%d|%s", t.RandUint64(), t.Now().Format(time.RFC3339Nano))), nil
})

func TestCrossArchitectureReplayFixture(t *testing.T) {
	iso := newTestIsolate(t, Config{
		Entry: archReplayEntry,
		Clock: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC),
		Seed:  17,
	})
	for index, want := range []string{"-7=negative", "2=two", "11=eleven"} {
		var events []Event
		if index > 0 {
			events = []Event{{ID: uint64(index)}}
		}
		state, commands, err := iso.Resume(context.Background(), events)
		if err != nil || state != Quiescent || len(commands) != 1 {
			t.Fatalf("command %d: state=%v commands=%v err=%v", index+1, state, commands, err)
		}
		command := commands[0]
		if command.ID != uint64(index+1) || command.Op != 81 || string(command.Payload) != want {
			t.Fatalf("command %d: got %+v, want ID=%d Op=81 Payload=%q", index+1, command, index+1, want)
		}
	}
	state, commands, err := iso.Resume(context.Background(), []Event{{ID: 3}})
	if err != nil || state != Completed || len(commands) != 0 {
		t.Fatalf("final state=%v commands=%v err=%v", state, commands, err)
	}
	result, err := iso.Result()
	if err != nil || string(result) != "6577935280906314593|2026-09-27T00:00:00Z" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}
