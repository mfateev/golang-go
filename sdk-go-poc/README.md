# Temporal isolate SDK POC

This module adapts statically linked Go isolates to the Temporal Go SDK's
`WorkflowDefinitionFactory` boundary, the same level used by the Go bridge for
Temporal's PHP SDK. It needs this repository's `bin/go` compiler and runtime.

Build the example from this directory:

```sh
../bin/go build -isolate-dir=./example/order -isolate-dir=./example/signal -o worker ./example/worker
```

Run `./worker` against a local Temporal server (or set `TEMPORAL_ADDRESS`). It
registers workflow types `IsolateOrder` and `IsolateSignal`, task queue
`isolate-poc`, and the host activity `echo`. `IsolateOrder` takes one `[]byte`
argument and returns it after an activity and a durable one-second timer.
`IsolateSignal` returns the bytes from its next signal.

The order and signal examples completed on Temporal CLI 1.9.1's local
development server, and their exported histories replayed in fresh processes.
To repeat the live check, start `temporal server start-dev --headless` and run
`./worker` in another terminal. Then run:

```sh
temporal workflow execute --workflow-id isolate-poc-order --type IsolateOrder --task-queue isolate-poc --input aGVsbG8= --input-base64 --input-meta encoding=binary/plain
temporal workflow start --workflow-id isolate-poc-signal --type IsolateSignal --task-queue isolate-poc
temporal workflow signal --workflow-id isolate-poc-signal --name ready --input c2lnbmFsLXJlc3VsdA== --input-base64 --input-meta encoding=binary/plain
temporal workflow result --workflow-id isolate-poc-signal
```

Export and replay a completed history with a fresh isolate process:

```sh
../bin/go build -isolate-dir=./example/order -isolate-dir=./example/signal -o replay ./example/replay
temporal workflow show --workflow-id isolate-poc-order --output json > /tmp/isolate-order-history.json
./replay temporal-order /tmp/isolate-order-history.json
```

Run the local bridge driver without a server:

```sh
../bin/go build -isolate-dir=./example/order -isolate-dir=./example/signal -o /tmp/isolate-temporal-driver ./example/driver
/tmp/isolate-temporal-driver
```

`workflow` is the API imported by isolate code. `temporalbridge` stays in the
host and depends on the pinned Temporal Go SDK. This initial adapter accepts
serial workflow programs only. The fork still needs a native quiescence barrier
and deterministic scheduling for concurrent workflow goroutines and replay.
See [the implementation plan](../doc/isolates/TEMPORAL_POC.md).
