# Trusted Temporal steel thread

This plan describes the trusted POC and its remaining gates. A worker built
with this Go fork links
functions marked `//go:isolate` through an ordinary `go build`, runs each
workflow execution in an isolate instance, and uses the Temporal Go SDK for
polling, history replay, and command emission. The separate
[`sdk-go-poc` repository](https://github.com/mfateev/sdk-go-poc/tree/task/modify-go-runtime-for-isolates)
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
isolate while dispatch is fenced, resumes its FIFO dispatcher, and services
all commands until the runtime reports exact suspension. It emits Temporal
commands through `WorkflowEnvironment`. Callbacks only queue results;
they never run isolate code themselves. A new factory instance is created per
workflow execution, and replay reconstructs its isolate from the beginning.

The isolate-side SDK owns stable `Call` operation numbers and wire encoding.
Initially the workflow API carries byte slices and offers input, activity,
timer, signal, and completion operations. Typed wrappers can be built above it.
The bridge accepts byte handlers and typed workflow functions. The isolate
adapter requires Temporal's default data converter on the worker and rejects
custom converters when a workflow task starts. Typed handlers receive
protobuf-serialized Temporal `Payloads` through the copied-byte isolate
boundary and use `converter.GetDefaultDataConverter()` inside the isolate to
decode arguments and encode the result. A small wire codec handles the
`Payloads` envelope without invoking protobuf's reflective decoder inside an
isolate. This avoids a second gob serialization format. The supported payload
encodings are `binary/null`, `binary/plain`, and `json/plain`; protobuf message
encodings and external payload references fail with a workflow error. The
converter's external dependency graph is provisionally process-owned. The host
activity SDK graph is also process-owned so workflows and activities can live
in the same package; its services must only be called by host activities. Its
mutable caches and effects, custom worker converters, payload codecs, and
serialization context remain productization work.

The host now injects the Workflow Task's history time into each isolate before
running it or replying to commands. `time.Now`, `time.Since`, and `time.Until`
read that clock locally; the runtime scheduler keeps using real time. Native
`time.Sleep`, `time.NewTimer`, and `time.After` send the SDK's durable timer
operation through the isolate boundary. Timer completion is delivered after
the host advances the clock for the next task. `time.AfterFunc` and tickers are
outside this POC's workflow-time subset. `Timer.Stop` and `Timer.Reset` suppress
local delivery but do not yet cancel an already scheduled Temporal timer; that
can cause extra history events. Concurrent timer, activity, and signal handling now uses the deterministic
dispatcher and exact suspension fence described in
[NATIVE_DETERMINISM_PLAN.md](./NATIVE_DETERMINISM_PLAN.md).

The first implementation is in `sdk-go-poc`. Its local driver runs the serial
activity/timer/completion path and a signal path through the actual statically
linked isolate boundary. With Temporal CLI 1.9.1 and its in-memory development
server, both workflows completed through the real worker. The Go SDK replayer
then accepted each exported history in a fresh process with a fresh isolate.

The separate [`samples-go-poc` repository](https://github.com/mfateev/samples-go-poc/tree/task/modify-go-runtime-for-isolates)
ports upstream `samples-go/helloworld`
and `samples-go/choice-exclusive`. Both completed against the development
server, and their exported histories replayed in fresh isolate processes.

## Scope and gates

1. **Serial vertical slice — passed locally:** one ordinary Go workflow program receives input,
   schedules an activity, waits for its result, sleeps on a durable Temporal
   timer, and completes. A worker registers it through the Go SDK factory.
2. **Replay — passed locally for both examples:** run the same history with a fresh isolate and verify the same
   Temporal commands and result. This covers the actual worker/server path and
   a local bridge driver; it does not cover cross-architecture replay.
3. **Native quiescence — passed locally for the trusted subset:** give `OnWorkflowTaskStarted` an exact suspend point
   after all runnable isolate goroutines have blocked. A channel receive,
   `sync.WaitGroup`, and concurrent `Call`s must work without polling delays or
   host commands leaking into a later workflow task.
4. **Deterministic execution — passed locally:** virtualize time and scheduler choices needed by
   the example; prove fan-out/replay with real `go`, channels, `select`, and
   `sync.WaitGroup`. The host records external effects, not internal goroutine
   scheduling.
5. **Lifecycle:** close or cancel a workflow without leaving live isolate
   goroutines, and report stuck revocation clearly.

The first slice is a trusted POC. Workflow code is selected and reviewed; it
must not use unsafe I/O APIs. Complete I/O interception, broad standard
library ownership, heap containment, and production security are deferred.
The trusted subset now has FIFO native goroutines, reproducible select,
canonical integer/string map iteration, logical time, and exact host
suspension. Live concurrent activity/timer execution and SleepForDays signal
completion replayed in fresh Linux arm64 processes with GOMAXPROCS 1, 2, and 8.
Cross-architecture replay remains a release gate; unsupported map key kinds,
sync.Map.Range, and iter.Pull are rejected in deterministic mode. `internalbindings` is an unstable Go SDK API;
the POC pins a tested SDK version and will need an adapter update when that
version changes. The follow-on release gates are in
[PRODUCTIZATION_PLAN.md](./PRODUCTIZATION_PLAN.md).

## Build shape

Clone `golang-go` and `sdk-go-poc` as sibling directories. From `sdk-go-poc`,
build the worker with `../golang-go/bin/go build -o worker ./example/worker`.
Workflow functions carry `//go:isolate`; the host imports them and uses the
POC SDK's `worker.RegisterWorkflow` API. The build generates their handles and
typed invokers. No workflow `main` or `isolate.json` is needed. Unmarked
ordinary workflows are forwarded to the Temporal SDK, including their
configured converters. The worker binary shares one Go runtime while each
execution receives its own selected package state. The activity implementation
remains host-side. Workflow code imports only the small
`github.com/mfateev/sdk-go-poc/workflow` package and normal Go packages; it
does not require a host-owned Temporal `workflow.Context`. State selection and
initializer replay still operate at package level; finer function reachability
and general framework support-package declarations remain TODO.
