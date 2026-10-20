# Package state and initialization

## Compiler layout and addressing

For each selected package, collect local external variables, sort them by symbol
name and build a synthetic struct with their exact Go types/alignment. Emit its
runtime type and GC pointer map, a unique package key, read-only per-variable
offsets, a dependency record, a replayable init record and a versioned descriptor.
A type pointer cannot be the package key because the linker can deduplicate
equal layout types.

The descriptor's current logical layout is:

```go
struct {
    MetadataVersion uint64
    Path string
    Key, TypeSlot, DependencyTask, InitTask unsafe.Pointer
}
```

`TypeSlot` addresses a cell containing the layout's runtime type pointer.
`DependencyTask` begins with a uint32 count, padded to eight bytes, followed by
`{Path string; Key unsafe.Pointer}` records. `InitTask` has an eight-byte prefix
containing a uint32 function count, followed by initializer function PCs. Its
record is immutable; it does not reuse the process init-task completion flag.
The metadata version is checked before interpreting the remaining fields.

Lower a selected global address as:

```text
base = currentG.packageState.lookup(packageKey)
address = base != nil ? base + variableOffset : originalHostAddress
```

Select the base before adding an offset so GC never sees an invalid interim
pointer. Local, imported, inlined and instantiated generic accesses use the same
rule. Imported offsets and keys are linker symbols. Generic dictionaries and
immutable compiler data are not mutable global slots. Missing keys in an active
instance fail instead of falling back to host state.

## Building an instance

The generated process entry registers typed function handles and state factories
using compiler-owned descriptor references. `internal/isolatestate.New`:

1. Checks metadata versions and complete descriptor fields.
2. Sorts paths; rejects duplicate paths/keys, absent dependencies, key mismatches
   and dependency cycles.
3. Allocates all typed layouts under the instance owner before replaying init.
4. Attaches the package table to the initializer goroutine and walks dependencies
   in deterministic order, running each selected initializer once.
5. Restores the caller's prior table on return. Native children inherit the
   selected table, owner and group; reused goroutine records clear them.

Selected embedded strings replay their literal bindings before ordinary source
initializers. Embedded byte slices get private mutable backing arrays. Function
closures, maps, pointers and objects allocated by init belong to the instance.
Clock, random seed, resource limits and initializer logging are installed first.
General host requests require `Start`; initialization cannot wait on that path.

The host keeps its normal globals. Host-reachable packages initialize once for
the process as well as separately in instances. Selected nonstandard packages
unreachable from the host can omit process startup init. Dependency selection
and support-factory graph composition belong to the SDK; the Go core receives
an explicit manifest. Shared dependencies in a composed program initialize once.

## Selection and enforcement

Selection is a whole-package decision. Direct writes from selected code into
unselected imported globals are rejected. Runtime ownership probes cover aliases,
heap pointers and dynamic calls. Compiling unselected packages with the same
checks prevents private execution from silently accessing their mutable process
state. An unselected stateless helper may still be usable.

The SDK's `internal/toolchain/selection.go` defines the selected standard-library
set. It includes JSON/reflection, time/context, formatting/logging, random APIs,
Unicode/regexp, strings/bytes and common encoding packages. Its graph planner
also selects ordinary workflow dependencies, private protobuf and SDK workflow
support while keeping host-only worker services initialized on the host.
Generated service descriptors reachable solely through host SDK internals are
excluded from private startup. Use `-isolate-report` to inspect the actual graph.

## Frozen tables and read-only services

Before instance creation the generated process entry prepares independent
snapshots of audited `unicode`, `regexp/syntax` and `strconv` state. Initialization
records exact allocation extents, then atomically publishes the reachable
immutable backing graphs. Each new layout copies private bindings to those
backings. Host globals are separate, so changing a host binding cannot affect
future instances. Writes to a frozen graph fail; replacing a private binding
with new private data is allowed.

A read-only service copies the workflow package table, retaining application
layout references for reads, and replaces configured library layouts with new
scratch-owned ones. It replays their init tasks in dependency order, except
frozen-library layouts copy their bindings directly. Protobuf runtime and
workflow-generated message layouts are included in scratch rebuilding.
Captured protobuf values are snapshotted into scratch representations before
cloning/conversion, avoiding reuse of workflow-owned mutable reflection caches.

See [read-only handlers](READ_ONLY_HANDLERS.md) for admission and write rules.

## Implementation locations

| Location under `src/` | Role |
| --- | --- |
| `cmd/compile/internal/ssagen/isolate.go` | Layout type, offsets and keys |
| `cmd/compile/internal/ssagen/ssa.go` | Current-package and imported global addresses |
| `cmd/compile/internal/pkginit/init.go` | Replayable initializer construction and embedded bindings |
| `cmd/compile/internal/isolate/globals.go` | Direct imported-global validation |
| `cmd/go/internal/work/isolate.go` | Generated registration, graph retention and startup selection |
| `internal/isolatestate/state.go` | Descriptor validation, ordering and state runner |
| `internal/isolatestate/frozen.go` | Frozen library preparation |
| `runtime/isolate_state.go`, `runtime/isolate_library*.go` | Typed table, scratch layouts and frozen backings |
| `runtime/proc.go`, `runtime/runtime2.go` | Child inheritance, roots and goroutine reuse |
