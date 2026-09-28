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

The test explicitly reruns the dependency's generated variable initializer
and user `init`, followed by the importer's variable initializer and user
`init`. The importer reads an initialized dependency global, then both
packages mutate their own graphs. Two instances and an inherited child passed
100 native arm64 race-detector runs.

This establishes the needed ordering for one known dependency edge. Package
discovery, a general dependency DAG, automatic per-instance initialization,
and a single contiguous base with linker-assigned package offsets remain to
be implemented. The tagged table is a probe for the access rule, not the final
layout architecture.

## Standard-library case to audit next

`encoding/base64` is a useful initialized-state case: its exported
`StdEncoding` and `URLEncoding` pointers are built by `NewEncoding`, and the
raw variants derive from them. Their initializer graph must be independent
per instance if the package is reachable from workflow code. Its imports and
any calls into process-owned runtime services also need review before the
package can be placed in the isolate set. `encoding/json` has shared caches
and pools, so using it as the first proof would mix initialization with a
broader process-state audit.
