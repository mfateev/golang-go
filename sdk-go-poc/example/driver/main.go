// The driver exercises an isolate workflow through the Temporal bindings
// without requiring a running Temporal service.
package main

import (
	"bytes"
	"fmt"
	"isolate"
	"time"

	"github.com/mfateev/golang-go/sdk-go-poc/temporalbridge"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	bindings "go.temporal.io/sdk/internalbindings"
	goWorkflow "go.temporal.io/sdk/workflow"
)

type environment struct {
	bindings.WorkflowEnvironment
	activity bindings.ResultHandler
	timer    bindings.ResultHandler
	signal   func(string, *commonpb.Payloads, *commonpb.Header) error
	result   *commonpb.Payloads
	err      error
	info     goWorkflow.Info
	seq      int64
}

func (e *environment) RegisterSignalHandler(h func(string, *commonpb.Payloads, *commonpb.Header) error) {
	e.signal = h
}
func (e *environment) GetDataConverter() converter.DataConverter {
	return converter.GetDefaultDataConverter()
}
func (e *environment) WorkflowInfo() *goWorkflow.Info { return &e.info }
func (e *environment) GenerateSequence() int64        { e.seq++; return e.seq }
func (e *environment) ExecuteActivity(p bindings.ExecuteActivityParams, cb bindings.ResultHandler) bindings.ActivityID {
	if p.ActivityType.Name != "echo" || p.StartToCloseTimeout != time.Minute {
		panic("unexpected activity")
	}
	e.activity = cb
	return bindings.ActivityID{}
}
func (e *environment) NewTimer(d time.Duration, _ goWorkflow.TimerOptions, cb bindings.ResultHandler) *bindings.TimerID {
	if d != time.Second {
		panic("unexpected timer")
	}
	e.timer = cb
	return nil
}
func (e *environment) Complete(result *commonpb.Payloads, err error) { e.result, e.err = result, err }

func main() {
	program, ok := isolate.LookupProgram("temporal-order")
	if !ok {
		panic("missing program")
	}
	d := (temporalbridge.Factory{Program: program}).NewWorkflowDefinition()
	e := &environment{}
	input, err := e.GetDataConverter().ToPayloads([]byte("hello"))
	if err != nil {
		panic(err)
	}
	d.Execute(e, nil, input)
	d.OnWorkflowTaskStarted(time.Second * 5)
	if e.activity == nil {
		panic("activity not scheduled")
	}
	if err := e.signal("unused", input, nil); err != nil {
		panic(err)
	}
	d.OnWorkflowTaskStarted(time.Second * 5) // unrelated signal must not fail the parked activity
	e.activity(input, nil)
	d.OnWorkflowTaskStarted(time.Second * 5)
	if e.timer == nil {
		panic("timer not scheduled")
	}
	e.timer(nil, nil)
	d.OnWorkflowTaskStarted(time.Second * 5)
	if e.err != nil {
		panic(e.err)
	}
	var result []byte
	if err := e.GetDataConverter().FromPayloads(e.result, &result); err != nil {
		panic(err)
	}
	if !bytes.Equal(result, []byte("hello")) {
		panic(fmt.Sprintf("result = %q", result))
	}
	d.Close()
	runSignal()
	fmt.Println("temporal isolate serial path passed")
}

func runSignal() {
	program, ok := isolate.LookupProgram("temporal-signal")
	if !ok {
		panic("missing signal program")
	}
	d := (temporalbridge.Factory{Program: program}).NewWorkflowDefinition()
	e := &environment{}
	input, err := e.GetDataConverter().ToPayloads([]byte("signal result"))
	if err != nil {
		panic(err)
	}
	d.Execute(e, nil, nil)
	d.OnWorkflowTaskStarted(5 * time.Second)
	if e.signal == nil {
		panic("signal handler not registered")
	}
	if err := e.signal("ready", input, nil); err != nil {
		panic(err)
	}
	d.OnWorkflowTaskStarted(5 * time.Second)
	if e.err != nil {
		panic(e.err)
	}
	var result []byte
	if err := e.GetDataConverter().FromPayloads(e.result, &result); err != nil {
		panic(err)
	}
	if !bytes.Equal(result, []byte("signal result")) {
		panic(fmt.Sprintf("signal result = %q", result))
	}
	d.Close()
}
