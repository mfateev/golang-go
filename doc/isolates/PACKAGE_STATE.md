# Phase 2B package-state partition

This is the first explicit partition for the opt-in compiler probe. It is not
yet a linked-program reachability manifest or an audit of the standard library.

| State | Current owner | Reason |
|---|---|---|
| Scheduler, goroutine records, allocator, process init-task state | Process | Runtime infrastructure must coordinate all instances. The process init task runs once. |
| `internal/isolateproto` name and handle registry | Process | A stable entry name and handle are registered once in the host. Its current function value is a Phase 1 limitation: an adapter that captures isolate state needs a per-instance callable table. |
| Generated `isolateLayoutType` and `isolateLayoutKey` symbols | Process | They are immutable package metadata used to allocate and select instance state. |
| Globals in a package compiled with `-d=isolateglobals=1` | Isolate | Every local external variable is placed in that package's generated, GC-described layout. Initializers are rerun with that layout selected. |
| Other package globals, including standard-library packages not opted in | Process in this probe | No isolation claim follows from using them. Each package must be classified before an ordinary-Go conformance claim. |

The current selection boundary is a **whole package**. A package that contains
both a process registry and mutable workflow state cannot safely opt in as it
stands; move the registry into a host package or implement an explicit,
verified split. The earlier `e4compiletoy` special-cased two variables and is
not evidence that the generated layout mode supports such a split.

## Two-package dependency probe

`e4deptoy` and `e4importtoy` are both opted in. The runtime's tagged probe
allocates one typed layout per package and maps each package's unique key to
its layout. The selected table is inherited by a native child goroutine. A
missing key in a selected table panics instead of silently reading process
state. With no table selected, ordinary process initialization uses the
process globals, and the older single-package probe can still use its base.

The tagged host helper `NewPackageInstance` accepts an explicit list of
compiler-owned package descriptors. Each descriptor contains the package path,
identity key, layout type slot, selected dependency record, and immutable
initializer record. The helper validates missing selected dependencies and
cycles, allocates each selected layout, and runs initializers in dependency
order. The test deliberately lists the importer before its dependency. The
importer reads an initialized dependency global, then both packages mutate
their own graphs. Two instances and an inherited child passed 100 native
arm64 race-detector runs.

This establishes descriptor-driven ordering for the selected package graph.
Package discovery, automatic dependency selection, and a single contiguous
base with linker-assigned package offsets remain to be implemented. The tagged
table and host helper are probes for the access rule, not the final API or
layout architecture.

## Standard-library initialized-state probe

`encoding/base64` is a useful initialized-state case: its exported
`StdEncoding` and `URLEncoding` pointers are built by `NewEncoding`, and the
raw variants derive from them. With `encoding/base64` compiled in the opt-in
layout mode, a tagged test reruns its generated variable initializer for two
instances. The four encoding pointers differ between instances and from the
process values. An importing package compiled with
`-d=isolateimports=encoding/base64` can read and reassign `StdEncoding` through
the selected layout; changing one instance leaves the other and the process
unchanged. The tagged host helper now replays the compiler's initializer
record for this package; the test passed 100 native arm64 race-detector runs.

This imported-global flag is a manual probe setting, not package discovery.
Every compiler invocation that directly accesses an opted-in package's
exported globals must select it. Passing
`-gcflags=all=-d=isolateimports=encoding/base64` to `go test` covers every
importing compiler invocation in the build. A tagged test checks direct
accesses from its test package, `e4base64toy`, and `e4base64caller` against the
same selected instance; 100 native arm64 race runs passed. With only
`e4base64toy` flagged, the separate caller reads and writes the process global
and the test fails. The package's generated offset and identity symbols are
linkable so the importing compiler can find its layout. The compiler's
initializer and selected dependency records are linkable for host replay. Its
imports and any calls into process-owned runtime
services still need review before `encoding/base64` can be placed in a
supported isolate package set. `encoding/json` has shared caches and pools,
so using it as the first proof would mix initialization with a broader
process-state audit.

## Build-wide selected-package probe

The opt-in `-d=isolatepackages=path1:path2` compiler flag now carries one
selected-package set to every compiler invocation in a build. A compiler
automatically enables layout generation and replayable initialization when its
own package path is selected. It redirects direct accesses to globals of
selected imported packages through their layout, and records selected direct
imports for initializer ordering. This removes the need to pair per-package
layout flags with a separate imported-global flag for each caller.

The tagged `e4importtoy` test selects both toy packages through one build-wide
flag. Its external test package is not itself selected, yet direct reads and
writes of an exported dependency global follow the selected instance. Two
instances retain independent values and the process value is unchanged. The
package's complete tagged suite passed 100 native arm64 race-detector runs.
The existing `encoding/base64` tagged suite also passed 100 native arm64 race
runs using only `-d=isolatepackages=encoding/base64` for selection.

In the direct tagged probe, the selected set is an explicit build input, not
an audited whole-program package partition. The compiler does not yet prove
that every package reachable from an isolate entry has been classified. The
current per-package table and descriptor list remain probe machinery.

The descriptor keeps each package's key, type slot, and initializer records
together in compiler output. The host supplies descriptor pointers rather
than pairing those fields manually. The static build now discovers application
dependencies; a linked-program classification proof remains open.

The compiler's selected-dependency record now includes each imported package's
generated identity key as a relocation. A linked importer whose dependency
record is retained requires that key symbol from the selected dependency.
The host checks the recorded key against its manifest before replaying any
initializers. This closes a silent manifest mismatch in the probe; it does not
discover packages that the build did not select.

The tagged `e4linktoy` fixture retains its generated dependency record at
link time without directly reading the dependency's globals. It supports a
negative link check: compile only the importer in isolate mode and select its
dependency as an import, leaving the dependency unselected. The linker must
reject the missing layout key. A build-wide selection of both packages must
link and run. Both outcomes were observed: the negative build failed with an
undefined `e4deptoy.isolateLayoutKey` relocation from only
`e4linktoy.isolateDependencyTask`, while the positive build passed. The
fixture uses a non-inlined dependency call so the initializer does not
independently reference the dependency's layout symbols.

## Static build integration

The experimental `go build -isolate-dir` path now walks each configured
program's package graph and selects its non-standard packages with one
build-wide `-d=isolatepackages` value. The generated top-level main retains
one descriptor for each selected package and passes the reachable subset to
`NewPackageInstance` when the host creates an instance. This reuses the
descriptor validation and dependency-ordered initializer replay above.

The build script starts `orders` twice and `billing` once. Both programs
import one initialized application package. `orders` also initializes one of
its own globals from that package. Each instance observes the dependency's
fresh initial value and the correct initialization order. This establishes a
working build-to-runtime path for application package state.

Standard-library packages remain process-owned in this probe. The build does
not yet classify them or support a deliberate process-owned application
package in an isolate program's import graph. A generated-entry compiler mode
omits selected packages' startup init tasks while retaining their code; the
entry imports reachable standard packages so their process initializers run
once. A build test counts selected initializer calls and confirms there is no
extra startup run. The build rejects a host that reaches selected application
packages. Initializers should still be pure: the probe does not enforce
determinism, and the host boundary is unavailable until `Start`. The
per-package table remains a tagged probe rather than the final contiguous
isolate layout.

## Process-wide pool boundary

The trusted source-level boundary now treats `sync.Pool` as empty while an
isolate goroutine executes or its selected package initializers replay.
`Get` uses `New` if provided, and `Put` drops the value. This prevents a
process-wide per-P pool from handing an object to another instance or
retaining an isolate-owned object. A source-level test covers the entry
goroutine and an inherited child; the static build script also exercises a
pool inside a selected package initializer.

This handles `sync.Pool` only. Standard packages such as `encoding/json` and
`fmt` also have process-wide caches; their values and mutation paths still
need an ownership and determinism audit before those packages can be claimed
as isolate-safe.
