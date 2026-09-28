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

The tagged host helper `NewPackageInstance` accepts an explicit package
manifest: identity key, layout type, selected dependencies, and the compiler's
immutable initializer record. It validates missing dependencies
and cycles, allocates each selected layout, and runs initializers in dependency
order. The test deliberately lists the importer before its dependency. The
importer reads an initialized dependency global, then both packages mutate
their own graphs. Two instances and an inherited child passed 100 native
arm64 race-detector runs.

This establishes manifest-driven ordering for the selected package graph.
Package discovery, generated dependency metadata, and a single contiguous
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
linkable so
the importing compiler can find its layout. The compiler's initializer record
is linkable for host replay. Its imports and any calls into process-owned runtime
services still need review before `encoding/base64` can be placed in a
supported isolate package set. `encoding/json` has shared caches and pools,
so using it as the first proof would mix initialization with a broader
process-state audit.
