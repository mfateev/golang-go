// Package workflow exposes host-mediated Temporal operations to a statically
// linked isolate program. Its byte-oriented API keeps the Temporal Go SDK on
// the host side of the isolate boundary.
package workflow

import (
	"encoding/json"
	"errors"
	"isolate"
	"time"
)

// Operation numbers are a wire contract. Never renumber an existing operation.
const (
	OpInput    uint32 = 1
	OpActivity uint32 = 2
	OpSleep    uint32 = 3
	OpSignal   uint32 = 4
	OpComplete uint32 = 5
)

// ActivityRequest is the wire representation of a host activity call.
type ActivityRequest struct {
	Name                string        `json:"name"`
	Input               []byte        `json:"input"`
	StartToCloseTimeout time.Duration `json:"start_to_close_timeout"`
}

// Signal is one incoming workflow signal.
type Signal struct {
	Name  string `json:"name"`
	Input []byte `json:"input"`
}

// Completion is the wire representation of a workflow result.
type Completion struct {
	Result []byte `json:"result"`
	Error  string `json:"error,omitempty"`
}

// Input returns the workflow's first byte-slice argument.
func Input() ([]byte, error) { return isolate.Call(OpInput, nil) }

// ExecuteActivity schedules a host activity and waits for its result.
func ExecuteActivity(name string, input []byte, timeout time.Duration) ([]byte, error) {
	if name == "" || timeout <= 0 {
		return nil, errors.New("workflow: activity name and timeout are required")
	}
	request, err := json.Marshal(ActivityRequest{Name: name, Input: input, StartToCloseTimeout: timeout})
	if err != nil {
		return nil, err
	}
	return isolate.Call(OpActivity, request)
}

// Sleep waits on a durable Temporal timer. Do not use time.Sleep for this POC.
func Sleep(duration time.Duration) error {
	if duration < 0 {
		return errors.New("workflow: negative sleep duration")
	}
	payload, err := json.Marshal(duration)
	if err != nil {
		return err
	}
	_, err = isolate.Call(OpSleep, payload)
	return err
}

// NextSignal waits for one incoming signal.
func NextSignal() (Signal, error) {
	var signal Signal
	payload, err := isolate.Call(OpSignal, nil)
	if err != nil {
		return signal, err
	}
	err = json.Unmarshal(payload, &signal)
	return signal, err
}

// Complete finishes the workflow. The program should return from main next.
func Complete(result []byte, cause error) error {
	completion := Completion{Result: result}
	if cause != nil {
		completion.Error = cause.Error()
	}
	payload, err := json.Marshal(completion)
	if err != nil {
		return err
	}
	_, err = isolate.Call(OpComplete, payload)
	return err
}
