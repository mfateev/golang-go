// Package temporalbridge connects one statically linked isolate program to
// Temporal's Go workflow worker. It is a trusted, serial POC adapter.
package temporalbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"isolate"
	"time"

	"github.com/mfateev/golang-go/sdk-go-poc/workflow"
	commonpb "go.temporal.io/api/common/v1"
	bindings "go.temporal.io/sdk/internalbindings"
	"go.temporal.io/sdk/worker"
	goWorkflow "go.temporal.io/sdk/workflow"
)

// Register makes a configured isolate program available as a Temporal workflow.
// The Temporal workflow name may differ from the isolate program name.
func Register(w worker.Worker, name string, program isolate.Program) {
	w.RegisterWorkflowWithOptions(Factory{Program: program}, goWorkflow.RegisterOptions{Name: name})
}

// Factory creates one definition, and therefore one isolate, per execution.
type Factory struct{ Program isolate.Program }

func (f Factory) NewWorkflowDefinition() bindings.WorkflowDefinition {
	return &definition{program: f.Program}
}

type reply struct {
	command *isolate.Command
	payload []byte
	err     error
}

type definition struct {
	program    isolate.Program
	env        bindings.WorkflowEnvironment
	input      *commonpb.Payloads
	instance   *isolate.Isolate
	started    bool
	completed  bool
	pending    []reply
	signals    []workflow.Signal
	wantSignal *isolate.Command
}

// Execute must be asynchronous. History callbacks only queue data here.
func (d *definition) Execute(env bindings.WorkflowEnvironment, _ *commonpb.Header, input *commonpb.Payloads) {
	d.env, d.input = env, input
	env.RegisterSignalHandler(func(name string, payloads *commonpb.Payloads, _ *commonpb.Header) error {
		var payload []byte
		if err := env.GetDataConverter().FromPayloads(payloads, &payload); err != nil {
			return err
		}
		d.signals = append(d.signals, workflow.Signal{Name: name, Input: payload})
		return nil
	})
}

func (d *definition) OnWorkflowTaskStarted(deadline time.Duration) {
	if d.completed {
		return
	}
	resuming := len(d.pending) != 0 || (d.wantSignal != nil && len(d.signals) != 0)
	newInstance := !d.started
	if !d.started {
		d.started = true
		var err error
		d.instance, err = isolate.New(isolate.Config{Program: d.program})
		if err == nil {
			err = d.instance.Start()
		}
		if err != nil {
			d.fail(err)
			return
		}
	}
	if !newInstance && !resuming {
		// A signal or unrelated history event need not wake a serial workflow.
		return
	}
	for _, r := range d.pending {
		r.command.Reply(r.payload, r.err)
	}
	d.pending = nil
	if d.wantSignal != nil && len(d.signals) != 0 {
		payload, _ := json.Marshal(d.signals[0])
		d.signals = d.signals[1:]
		d.wantSignal.Reply(payload, nil)
		d.wantSignal = nil
	}
	// This timeout detects a broken serial adapter. A native quiescence barrier
	// must replace the one-command-at-a-time assumption for concurrent code.
	if deadline <= 0 {
		deadline = time.Second
	}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for !d.completed {
		select {
		case command := <-d.instance.Commands():
			if command == nil {
				d.fail(errors.New("isolate command stream closed"))
				return
			}
			blocked, err := d.handle(command)
			if err != nil {
				command.Reply(nil, err)
				d.fail(err)
				return
			}
			if blocked {
				return
			}
		case <-d.instance.Done():
			if !d.completed {
				if err := d.instance.Wait(); err != nil {
					d.fail(fmt.Errorf("isolate returned without workflow.Complete: %w", err))
				} else {
					d.fail(errors.New("isolate returned without workflow.Complete"))
				}
			}
			return
		case <-timer.C:
			d.fail(errors.New("isolate did not reach a host operation before the workflow task deadline"))
			return
		}
	}
}

// handle returns true when the isolate is waiting for a future history event.
func (d *definition) handle(command *isolate.Command) (bool, error) {
	switch command.Op {
	case workflow.OpInput:
		var input []byte
		if d.input != nil {
			if err := d.env.GetDataConverter().FromPayloads(d.input, &input); err != nil {
				return false, err
			}
		}
		command.Reply(input, nil)
	case workflow.OpActivity:
		var request workflow.ActivityRequest
		if err := json.Unmarshal(command.Payload, &request); err != nil {
			return false, err
		}
		if request.Name == "" || request.StartToCloseTimeout <= 0 {
			return false, errors.New("invalid activity request")
		}
		input, err := d.env.GetDataConverter().ToPayloads(request.Input)
		if err != nil {
			return false, err
		}
		params := bindings.ExecuteActivityParams{
			ExecuteActivityOptions: bindings.ExecuteActivityOptions{
				TaskQueueName:       d.env.WorkflowInfo().TaskQueueName,
				StartToCloseTimeout: request.StartToCloseTimeout,
				ScheduleID:          d.env.GenerateSequence(),
			},
			ActivityType: bindings.ActivityType{Name: request.Name},
			Input:        input,
		}
		d.env.ExecuteActivity(params, func(result *commonpb.Payloads, cause error) {
			d.queueResult(command, result, cause)
		})
		return true, nil
	case workflow.OpSleep:
		var duration time.Duration
		if err := json.Unmarshal(command.Payload, &duration); err != nil {
			return false, err
		}
		if duration < 0 {
			return false, errors.New("negative timer duration")
		}
		if duration == 0 {
			command.Reply(nil, nil)
			return false, nil
		}
		d.env.NewTimer(duration, goWorkflow.TimerOptions{}, func(result *commonpb.Payloads, cause error) {
			d.pending = append(d.pending, reply{command: command, err: cause})
		})
		return true, nil
	case workflow.OpSignal:
		if len(d.signals) == 0 {
			d.wantSignal = command
			return true, nil
		}
		payload, _ := json.Marshal(d.signals[0])
		d.signals = d.signals[1:]
		command.Reply(payload, nil)
	case workflow.OpComplete:
		var completion workflow.Completion
		if err := json.Unmarshal(command.Payload, &completion); err != nil {
			return false, err
		}
		var result *commonpb.Payloads
		var err error
		if completion.Error != "" {
			err = errors.New(completion.Error)
		} else {
			result, err = d.env.GetDataConverter().ToPayloads(completion.Result)
		}
		if err == nil || completion.Error != "" {
			d.env.Complete(result, err)
			d.completed = true
			command.Reply(nil, nil)
			return true, nil
		}
		return false, err
	default:
		return false, fmt.Errorf("unknown isolate operation %d", command.Op)
	}
	return false, nil
}

func (d *definition) queueResult(command *isolate.Command, result *commonpb.Payloads, cause error) {
	var payload []byte
	if cause == nil && result != nil {
		cause = d.env.GetDataConverter().FromPayloads(result, &payload)
	}
	d.pending = append(d.pending, reply{command: command, payload: payload, err: cause})
}

func (d *definition) fail(err error) {
	if d.completed {
		return
	}
	d.completed = true
	d.env.Complete(nil, err)
}

func (d *definition) StackTrace() string { return "isolate workflow stack trace unavailable" }

func (d *definition) Close() {
	if d.instance == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = d.instance.Kill(ctx)
}
