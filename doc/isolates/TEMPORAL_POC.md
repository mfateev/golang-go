# Trusted Temporal steel thread

This is the next implementation target. A worker built with this Go fork links
ordinary `package main` workflow programs with `-isolate-dir`, runs each
workflow execution in an isolate instance, and uses the Temporal Go SDK for
polling, history replay, and command emission. The separate `sdk-go-poc` module
contains the worker adapter and the small workflow-side API.

## Integration boundary

The Go SDK accepts a `WorkflowDefinitionFactory` via
`worker.RegisterWorkflowWithOptions`. Its `WorkflowDefinition` receives workflow
task activations and exposes a `WorkflowEnvironment` for activities, timers,
signals, and completion. The PHP SDK's RoadRunner Go bridge uses this same
`internalbindings` boundary. Our adapter replaces its process transport with
`isolate.Call` and host `Command.Reply`; it does not reimplement Temporal's
history processing.

`Execute` only stores input and registers a signal handler. It starts no
application code. `OnWorkflowTaskStarted` delivers queued callbacks to the
isolate, lets the workflow run until it reaches a host operation, then emits
Temporal commands through `WorkflowEnvironment`. Callbacks only queue results;
they never run isolate code themselves. A new factory instance is created per
workflow execution, and replay reconstructs its isolate from the beginning.

The isolate-side SDK owns stable `Call` operation numbers and wire encoding.
Initially the workflow API carries byte slices and offers input, activity,
timer, signal, and completion operations. Typed wrappers can be built above it.
The host uses Temporal's data converter at the boundary.

The first implementation is in `sdk-go-poc`. Its local driver runs the serial
activity/timer/completion path and a signal path through the actual statically
linked isolate boundary. The worker builds with both workflow programs. A
Temporal server run and recorded-history replay remain to be verified.

## Scope and gates

1. **Serial vertical slice:** one ordinary Go workflow program receives input,
   schedules an activity, waits for its result, sleeps on a durable Temporal
   timer, and completes. A worker registers it through the Go SDK factory.
2. **Replay:** run the same history with a fresh isolate and verify the same
   Temporal commands and result. Test an actual worker/server path as well as
   a local bridge test.
3. **Native quiescence:** give `OnWorkflowTaskStarted` an exact suspend point
   after all runnable isolate goroutines have blocked. A channel receive,
   `sync.WaitGroup`, and concurrent `Call`s must work without polling delays or
   host commands leaking into a later workflow task.
4. **Deterministic execution:** virtualize time and scheduler choices needed by
   the example; prove fan-out/replay with real `go`, channels, `select`, and
   `sync.WaitGroup`. The host records external effects, not internal goroutine
   scheduling.
5. **Lifecycle:** close or cancel a workflow without leaving live isolate
   goroutines, and report stuck revocation clearly.

The first slice is a trusted POC. Workflow code is selected and reviewed; it
must not use unsafe I/O APIs. Complete I/O interception, broad standard
library ownership, heap containment, and production security are deferred.
The provisional isolate runtime currently lacks deterministic scheduling and
an exact quiescence barrier, so the serial adapter must not be presented as
the completed steel thread. `internalbindings` is an unstable Go SDK API;
the POC pins a tested SDK version and will need an adapter update when that
version changes.

## Build shape

From `sdk-go-poc`, build the worker with the fork's `bin/go` and select each
workflow directory using `-isolate-dir`. The worker binary shares one Go
runtime while each execution receives its own selected package state. The
activity implementation remains host-side. Workflow code imports only the
small `sdk-go-poc/workflow` package and normal Go packages; it does not import
the Temporal Go SDK.
