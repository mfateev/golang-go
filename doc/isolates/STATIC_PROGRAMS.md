# Statically linked isolate programs

Status: **experimental static build, application-package state, selected
standard-library state, and trusted host bridge implemented**. Broad
standard-library ownership and effect classification and the native scheduler
remain pending. Dynamic loading is a
[future enhancement](./DYNAMIC_LOADING.md).

## Directory contract

Each isolate program has its own directory containing `package main` and an
`isolate.json` file. For example:

```text
worker/
  main.go                         # process host
  isolates/
    orders/
      main.go                     # package main
      isolate.json                # {"name":"orders"}
    billing/
      main.go                     # package main
      isolate.json                # {"name":"billing"}
```

The config's `name` is the stable logical program identity used by the host
and persisted instance metadata. It must be unique in the worker. The
directory is the build input and the config sits beside that program's
sources. The MVP config does not list dependencies, global variables, or
plugin paths; the Go loader and compiler must derive the dependency graph and
state descriptors from the sources. A build should take an explicit set of
program directories, rather than silently including every directory it can
find.

Each program's `main` keeps the ordinary Go signature. It imports the new
[`isolate` package](../../src/isolate/isolate.go) for host input and calls:

```go
package main

import "isolate"

func main() {
    input := <-isolate.Inbox()
    // Decode input, run the workflow, and use isolate.Call for host work.
    _ = input
}
```

`Call` and `Inbox` work through a trusted boundary. The static build registers
each entry by its configured name, and `isolate.New` plus `Start` bind a new
transport to one invocation. The bridge has no separate isolate heap or
deterministic scheduler.

From the worker module, build one executable with the host package and the
selected programs:

```bash
go build -isolate-dir=./isolates/orders -isolate-dir=./isolates/billing -o worker ./host
```

Add `-isolate-report=ownership.json` to write a JSON inventory after a
successful build. For each configured program it lists every reachable
package. The package records distinguish instance state, a selected standard
package probe, and standard packages still left process-wide. A
`host_reachable` field identifies packages that have both a process copy and
instance copies. This report describes the current build selection; a
process-wide standard package in it has **not** been certified isolate-safe.

The host can look up a program, start it, and answer its calls:

```go
program, ok := isolate.LookupProgram("orders")
if !ok { panic("missing orders program") }
instance, err := isolate.New(isolate.Config{Program: program, Input: request})
if err != nil { panic(err) }
if err := instance.Start(); err != nil { panic(err) }
for {
    select {
    case command := <-instance.Commands():
        result, err := handle(command.Op, command.Payload)
        command.Reply(result, err)
    case <-instance.Done():
        return
    }
}
```

This host loop is a temporary transport API. It does not yet implement the
planned `Resume`/quiescence contract. The build automatically selects each
configured `package main`, every reachable non-standard package, and audited
standard packages `encoding/base32` and `encoding/base64` for the tagged
package-state probe.
`isolate.New` allocates their global layouts and replays initializers in
dependency order for each instance. A build test starts one program twice and
another once, with both importing an initialized shared package; each run
observes fresh state. The test also checks separate host and instance copies
of `encoding/base32.StdEncoding` and `encoding/base64.StdEncoding`. Other
standard-library packages remain process-owned in this probe and initialize
once at process startup.
The script also round-trips data through `encoding/json`, which remains
unclassified and process-owned in the report. A working round trip does not
establish safe package-state ownership; see PACKAGE_STATE.md.
An isolate-selected package cannot directly assign to an unselected imported
global. The build script checks direct, indexed, and field writes from
initializers, `main`, and a closure. Selecting `encoding/json` alone rejects
its v2 callback assignments into `encoding/json/internal`.
A second negative build checks direct mutation through `copy`, `clear`,
`delete`, `append`, channel send, and channel close.
The generated top-level entry retains selected application packages but omits
startup init tasks for packages unused by the host. Selected initializers run
when `isolate.New` creates each instance. Those initializers should be pure:
the trusted probe does not enforce determinism, and `Call`/`Inbox` are
unavailable until `Start`. The runtime nevertheless binds the instance's
stable owner token during initializer replay and throughout the state runner,
`main`, and its child goroutines. Heap allocation still uses the ordinary
process allocator.
If the host and an isolate both import a selected application package, the
package has one compiled definition but separate process and per-instance
globals. Its process initializer runs at startup, and its initializer also
runs for each instance. A build test mutates the host copy and starts an
isolate that sees its own initial state; the host copy stays unchanged.
Deliberate process-owned application packages need a separate classification
mechanism; keep them outside the isolate program's import graph for now.
While an instance runs or initializes, `sync.Pool` returns fresh values and
discards puts so its process-wide storage cannot pass temporary objects
between instances. Other standard-library caches remain under audit.

One final executable contains the host and all selected programs. The host
selects a program by logical name and creates many instances of it. A build
artifact identity is separate from that name; it changes when code or build
inputs change. The source `main()` is an entry point for one instance, not a
process startup function. The build must generate or retain a wrapper that
binds each program's `main` to the host's entry table. Input, host calls, and
completion use the isolate boundary API because `main()` has no parameters or
return value. The Phase 1 `isolateproto.Register` function registry remains a
reference-model mechanism, not the proposed source-level program contract.

The build compiles each selected `package main` under its own import path and
generates a synthetic top-level `main` that registers each program entry and
calls the host's `main`. Ordinary `go build` still builds one top-level main;
the experimental `-isolate-dir` path provides this explicit multi-program
support.

For every program, the build must identify the reachable packages, classify
their mutable state as isolate-owned or process-owned, pass one coherent
selection to all compiler invocations, and retain the selected package
descriptors in a linked manifest. Instance creation replays only its program's
selected package initializers in dependency order. Shared code and immutable
metadata can be used by many instances; selected mutable state is allocated
per instance. A package reached by several programs must have one compatible
compiled definition and a consistent ownership classification.

## Dependency versions

The MVP has one linked Go package definition per import path. If two programs
import the same path, the build uses one compatible version of that package.
Ordinary Go module selection also resolves a module graph to one selected
version per module path. Programs that need distinct major-version import
paths, such as `example.org/dep` (v1) and `example.org/dep/v2`, can carry
both definitions, subject to the same state and subset checks. This follows
Go's [major-version import-path rule](https://go.dev/ref/mod#major-version-suffixes).

Two arbitrary versions behind the **same** import path are deferred. Making
that work would require per-program package-path rewriting or another
namespace mechanism, with consequences for type identity, shared packages,
linking, and the ownership audit. Separate `go.mod` files in program
directories do not by themselves make those versions coexist in one Go
executable. The config therefore has no dependency-version override field.

## Linker feasibility probe

On 2026-09-29, a direct `go tool compile`/`go tool link` probe on this fork's
Linux arm64 toolchain compiled two separate `package main` units as
`isolate/a` and `isolate/b`. A host blank-imported both compiled units and
used entry stubs to call their `main` functions; the executable printed
`2 12` after each changed its own global. This proves that distinct internal
package paths can coexist in one static binary. It does not yet prove safe
per-instance state, generated entry wrappers, or `cmd/go` integration.

A second probe compiled the two units against different definitions of
`example.org/dep`. With the **same** dependency import path, linking failed
with a package fingerprint mismatch. After compiling the definitions as
`example.org/dep` and `example.org/dep/v2`, the same host linked and
printed `1 2`. These probes use manual import configurations; a supported
build must generate and validate them.

After the `isolate` boundary probe was implemented, a third direct-toolchain
probe compiled `orders` and `billing` directories, each with a normal
`package main`, an `isolate.json`, and calls to `isolate.Inbox` and
`isolate.Call`. The host linked both mains under unique internal package
paths, bound a separate trusted boundary to each invocation, and answered
their commands. It printed:

```text
orders orders:orders
billing billing:billing
seen ok ok
```

This proved the source-level API works from two separately compiled mains in
one binary. The later `cmd/go` integration now reads the directory configs
and generates the entry table automatically; the probe's native owned-state
manifest remains separate.

## Next build slice

The [config reader](../../src/cmd/go/internal/isolatecfg/config.go),
multi-main loader, generated entry table, reachable selected-package
descriptor list, and package ownership report are in place. The remaining
build work is to classify the broad standard library, support deliberate
process-owned application packages, validate the complete selection against
every reachable package, and replace the tagged table with the final
contiguous instance layout.
