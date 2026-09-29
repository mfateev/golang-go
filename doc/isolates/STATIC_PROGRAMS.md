# Statically linked isolate programs

Status: **MVP build direction; config reader implemented, build integration
pending**. Dynamic loading is a [future enhancement](./DYNAMIC_LOADING.md).

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

`Call` and `Inbox` work in the trusted boundary probe when the host binds
one transport to the entry goroutine. The static build does not yet generate
that entry binding, and the probe has no separate isolate heap or deterministic
scheduler.

One final executable contains the host and all selected programs. The host
selects a program by logical name and creates many instances of it. A build
artifact identity is separate from that name; it changes when code or build
inputs change. The source `main()` is an entry point for one instance, not a
process startup function. The build must generate or retain a wrapper that
binds each program's `main` to the host's entry table. Input, host calls, and
completion use the isolate boundary API because `main()` has no parameters or
return value. The Phase 1 `isolateproto.Register` function registry remains a
reference-model mechanism, not the proposed source-level program contract.

The build must compile each `package main` under a distinct internal package
path. Ordinary `go build` assigns a top-level executable's main package the
path `main`, and `cmd/go` rejects importing another directory's `package
main`. The static isolate build path will need explicit support in `cmd/go` to
load these directories as program units, assign unique symbol paths, and
retain their entry wrappers. A config file alone does not provide that support.

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

## Next build slice

The [config reader](../../src/cmd/go/internal/isolatecfg/config.go) handles
step 1, including duplicate names and deterministic ordering. The remaining
build slices are:

1. Load each directory as a `package main` program under a unique internal
   path while keeping the source import graph intact.
2. Generate the host entry table and linked package-descriptor manifest.
3. Derive and validate one package-state selection, then run two instances of
   one program and one instance of another with independent initialized state.
