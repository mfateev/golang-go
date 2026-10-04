# Temporal Go samples on the isolate POC SDK

This separate module ports two examples from
[Temporal's samples-go](https://github.com/temporalio/samples-go) at commit
`aaf79b6`: [helloworld](https://github.com/temporalio/samples-go/tree/aaf79b6/helloworld)
and [choice-exclusive](https://github.com/temporalio/samples-go/tree/aaf79b6/choice-exclusive).
The adapted sample code is under the upstream Apache 2.0 license in `LICENSE`.

Each workflow directory is an ordinary `package main` with `isolate.json`.
Workflow code imports the local `sdk-go-poc/workflow` API. Activities and
workers remain host-side and use the Temporal Go SDK. The module's local
`replace` points to the sibling `sdk-go-poc` source; build with this fork's
`../bin/go` from this directory.

## Build and run

Start a local server with `temporal server start-dev --headless`. In this
directory, build the two workers:

```sh
mkdir -p bin
../bin/go build -isolate-dir=./helloworld/workflow -o bin/helloworld-worker ./helloworld/worker
../bin/go build -isolate-dir=./choice-exclusive/workflow -o bin/choice-worker ./choice-exclusive/worker
```

Run each worker in its own terminal. `TEMPORAL_ADDRESS` overrides the default
`localhost:7233` for workers and starters.

```sh
./bin/helloworld-worker
./bin/choice-worker
```

Run the corresponding starters from this directory:

```sh
../bin/go run ./helloworld/starter Temporal
../bin/go run ./choice-exclusive/starter
```

`helloworld` returns `Hello Temporal!`. `choice-exclusive` runs `GetOrder` and
then exactly one of `OrderApple`, `OrderBanana`, `OrderCherry`, or
`OrderOrange`; the host activity chooses randomly, and its recorded result
drives the workflow branch on replay.

The POC adapter currently supports serial workflow code. These ports use
`[]byte` arguments and results because the isolate-side SDK's wire API is
byte-oriented. The original sample workflows' `workflow.Context` and future
operations become blocking calls from ordinary Go `main` functions.

## Replay a completed run

The starters print their Workflow IDs. Export a history while the development
server is running, then replay it in a fresh process:

```sh
../bin/go build -isolate-dir=./helloworld/workflow -isolate-dir=./choice-exclusive/workflow -o bin/replay ./replay
temporal workflow show --workflow-id YOUR_HELLO_WORKFLOW_ID --output json > /tmp/hello-history.json
./bin/replay helloworld-poc HelloWorld /tmp/hello-history.json
temporal workflow show --workflow-id YOUR_CHOICE_WORKFLOW_ID --output json > /tmp/choice-history.json
./bin/replay choice-exclusive-poc ExclusiveChoice /tmp/choice-history.json
```

Both sample workflows completed on a local Temporal CLI 1.9.1 server and
passed this fresh-process replay check. This validates the serial POC path;
native quiescence and deterministic concurrent goroutines remain future work.
