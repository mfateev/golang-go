# Memory ownership: productization feature 2

Status: complete and validated on 2026-10-06.
Feature 1 was checked in and validated before this work began. This feature
provides ownership for statically linked, reviewed Go workflow code. It does
not freeze suspended heaps or implement a separate collector (feature 3).

## Enforced contract

Every marked-function or configured-program build enables level-two memory
checks throughout its compiled dependency graph. A user-supplied
`-d=isolateheap=0` cannot disable enforcement. An ordinary build without isolate
entries keeps the normal Go execution path. Checked functions also retain their
ordinary host behavior when executed outside an instance.

Each instance has its own package globals and allocator cache. Small and large
heap spans have one owner, independent of the processor running the goroutine.
Instance tiny allocations do not share packing blocks. Owner caches participate
in the ordinary Go collector's flushing, sweeping, accounting and profiling;
a GC-visible acyclic handle retires an unreachable cache without retaining its
instance or freeing live objects prematurely.

Reads, writes and reference publication validate ownership before the operation.
Coverage includes typed pointer access, interfaces and boxing, closure access,
strings, slices, bulk copies, maps and map keys, channels and select operands,
reflected calls and collection operations, and direct/indirect atomics. Current
stack storage is permitted; another goroutine's stack, process globals, and
unclassified memory are denied to private code. Pointer equality does not read
the pointed-to object. Nil and language bounds behavior remain Go behavior.
Ownership probes are GC safepoints: stack publication checks precede destination liveness, ABI result
areas are copied before probes can reuse them, and package addressing selects
valid storage before adding an instance offset. A forced stack-movement program
test covers closure initialization and aggregate copies under race detection.

A detected violation permanently revokes the managed instance, suppresses
application recovery and defers, and reports `*isolate.OwnershipError` through
`New`, `Wait`, `Suspend` or `Resume`. Trusted metadata cleanup releases shared
locks before discard. GC startup and process-service channel/select waits finish
through ordinary wakeups, retaining their process locks until cleanup; revocation
is honored when the outer service exits. `Kill(ctx)` waits for cleanup and
attached goroutines; a pending result does not restore permission to run.
Retained host error objects do not retain failed instances or allocator caches.

The host boundary still copies bytes and error text into the receiving owner's
allocations. Workflow arguments/results use the compiler-generated typed
invoker and the default converter inside the instance. Application objects,
channels, closures and mutable caches are not shared with the host or peers.

## Build manifest and supported library policy

`-isolate-report` classifies every reachable package:

| Classification | State and access policy |
|---|---|
| `instance-state` | Replay the selected initializer and route mutable globals to each instance; the host retains separate state. |
| `process-state-denied` | Initialize on the host. Compile with the same compulsory checks; private access to mutable process state fails. Stateless operations may still work. Exact audited metadata operations have the service policy below. |
| `trusted-runtime` | GOROOT allocator, scheduler, compiler ABI, sanitizer and transport implementations enforce explicit internal contracts. External modules cannot inherit this classification by matching a package name. |

Application dependencies normally receive instance state. The Temporal activity
SDK and converter dependency graphs retain host initialization because they
include worker services, registries, environment reads and gRPC configuration.
This startup classification grants no access exemption. The converter package
itself and SDK workflow support state are instantiated separately for each
workflow. Custom worker converters/codecs and general protobuf workflow values
remain future converter-support work (feature 8).

Selected standard-library state covers `bytes`, `strings`, `errors`, `io`,
`fmt`, `strconv`, `context`, `time`, `reflect`, `internal/reflectlite`,
`math`, `math/rand`, `math/rand/v2`,
`regexp`/`regexp/syntax`, `unicode`/`unicode/utf8`/`unicode/utf16`,
`encoding/binary`, `encoding/base32`, `encoding/base64`, and JSON's v1/v2
implementation packages. The authoritative package list is
[src/cmd/go/internal/work/isolate.go](../../src/cmd/go/internal/work/isolate.go).
Other standard packages are checked and their unselected mutable state is
unavailable inside an instance. For example, `net/textproto`'s process-wide MIME
header cache is rejected; listing a dependency is not a support promise.

`sync.Pool` keeps instance objects out of process pool lists. Its collector hook
is trusted because it runs with the world stopped. `sync/atomic.Value` validates
receiver access and retained interface contents explicitly before its pinned
first-store section; compiler checks do not run inside that sentinel sequence.
Raw reflected/indirect atomic calls receive equivalent operand checks.
`runtime/metrics.All` copies descriptions and their string backing data into
the caller's owner. SHA-256 validates digest/input ranges before any assembly
backend; its exact backend selector cells expose implementation configuration
without allowing writes or sharing application objects. Fixed CPU feature records
permit bounded reads during per-instance library initialization. Other
`runtime/*` library packages receive normal checks; the runtime exemption covers
only core machinery and sanitizer/cgo hooks.

## Shared immutable metadata and audited services

Only linker-defined read-only ranges and explicitly published canonical
metadata can be shared. Owner zero is not an immutable classification. Shared
reflection metadata includes precise type headers, function parameter arrays,
method/field tables, packed name ranges and canonical equality/hash closures.
Mutable/lazy parts of an unrelated metadata graph gain no permission. Cold itab
creation and interface assertion/switch cache allocation use the process owner;
immutable dynamic itabs receive exact provenance. These caches retain types and
code, never workflow receivers or private allocations.

Reviewed metadata services may inspect their original caller's private arguments
without mutation or retention. They enter before acquiring a process lock,
release all locks before leaving, restore nested ownership, and honor pending
revocation on outermost exit. They cannot start application goroutines, call the
host or exit the process. Compiler-recorded function provenance, preserved
through generic exports and instantiation, prevents service privileges from
reaching application callbacks, including packages named like GOROOT libraries.
Reflection's assembly invocation path checks the actual target. Defer targets are
validated before registration, covering normal returns and panic unwinding while
preserving ordinary nil-defer behavior.

The service manifest consists of:

- Reflection type constructors: `PointerTo`, `ChanOf`, `FuncOf`, `SliceOf`,
  `StructOf`, `ArrayOf`, and `MapOf`.
- Protobuf `google.golang.org/protobuf@v1.36.11`: enumerated lazy descriptor/index
  builders, message/extension type initialization, and registry lookup/count
  methods. Generated descriptors use `go.temporal.io/api@v1.63.6`. Replacement,
  vendored, nested-module and unaudited-version sources are rejected throughout
  the compiled graph, including host-only implementations reachable from
  registries.
- Exact process metadata handle cells, fixed CPU feature records, SHA-256
  backend-selection scalar reads and its fixed round table, and a proven
  immutable prefix of canonical protobuf `MessageInfo` records. This does not
  approve their mutable cache fields or all protobuf marshal/unmarshal paths.

The authoritative function/cell manifest is
[src/cmd/internal/isolatepolicy/metadata.go](../../src/cmd/internal/isolatepolicy/metadata.go).
Private service receivers and custom descriptors are rejected. Registry mutation,
visitors, legacy descriptors, lazy option decoders and unreviewed callbacks remain
unsupported in instances; host implementations retain their normal behavior.

## Scope and remaining productization

This is the memory contract for reviewed code using supported Go operations.
It does not claim containment of hostile assembly, cgo, arbitrary unsafe pointer
fabrication or external native memory. Complete effect/unsafe enforcement is
feature 4. Custom converter support is feature 8. Ordinary child-failure semantics,
resource limits, replay-aware logging and bounded CPU-loop termination retain
their separate productization gates. Suspended instances are still traced by the
shared collector; whole-heap eviction and frozen-heap GC are feature 3.

## Acceptance gates

1. Full toolchain bootstrap and `src/all.bash`.
2. Six compiler integration scripts, including default enforcement, host plus
   two-instance state, namespace impersonation, reflection, pointer publication,
   atomics, immutable metadata and metadata source rejection.
3. Runtime/library/compiler suites and five ownership/dispatcher/metadata
   repetitions under both race detection and static lock ranking.
4. Three batches of 1,024 cached instances, with 64 KiB live state each, weak
   instance/cache/error references and reclamation checks. Five race runs evict
   15,360 cached instances. Go's reusable free pages are not counted as live leaks.
5. SDK and tracked sample suites; default and strict dependency drivers at
   `GOMAXPROCS=1/2/8`, `GOGC=1`, and a race driver. Unmarked workflows keep the
   ordinary Temporal SDK path.
6. Six fresh-process saved-history replays with different processor counts,
   CPU features and host time zones. Preserve 195 observations and SHA-256
   `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
7. Commit/push the implementation and pass all native Linux/macOS arm64/amd64
   CI jobs before closing feature 2.

## Final acceptance results (2026-10-06)

All seven gates passed. The final local Linux arm64 run bootstrapped the complete
toolchain with Go 1.26.7 and finished `src/all.bash` with `ALL TESTS PASSED`.

| Gate | Passed evidence |
|---|---|
| Full toolchain | Three bootstrap stages, standard-library and command tests, experiments, cgo/race integration, and the language regression suite. |
| Compiler integration | All six `build_isolate_*` scripts; the program script also forces stack movement at runtime entry under race detection. |
| Ownership and dispatch stress | Five repetitions under race detection and five under static lock ranking; includes fault cleanup during trusted channel/select waits. |
| Cached-instance reclamation | The race runs evicted 15,360 cached instances, checking weak references, allocator-cache counts, live heap memory, goroutines, and retained fault errors. |
| SDK and samples | All SDK and tracked sample packages; default/strict drivers at `GOMAXPROCS=1/2/8`, `GOGC=1`, and a race-enabled driver. |
| Replay | All six fresh-process configurations preserve the 195 observations and SHA-256 above. |
| Native platforms | Linux and macOS, each on arm64 and amd64; runtime, compiler, race, lock ranking, SDK, samples, and saved-history replay all pass. |

Native evidence: [Isolate determinism run 37514145654](https://github.com/mfateev/golang-go/actions/runs/37514145654).
Each job's retained artifact records the same tested revisions:

- `golang-go`: `ebbfe9bc539805f28a313400b1e4827597b69b77`.
- `sdk-go-poc`: `d40fbc06fdb29c0f17adffdce480af4f5928b9a1`.
- `samples-go-poc`: `a2f364746f75cb42dd0381c46597b5e8c782b94d`.

The revisions above identify the tested acceptance checkpoint. Installation
instructions continue to follow `task/modify-go-runtime-for-isolates` in the
forks. The local run captured every stage in `/tmp/feature2-*.log`, with the
stage exit codes in `/tmp/feature2-validation-complete-status.log` and the full
toolchain output in `/tmp/feature2-all-final.log`. Earlier partial checkpoints
and debugging evidence are retained in
[Memory ownership implementation history](./MEMORY_OWNERSHIP_HISTORY.md).

Feature 3, efficient GC for suspended/cached workflows, is the next foundation.
The scope exclusions above retain their separate productization gates.
