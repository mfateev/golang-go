# Memory ownership: productization item 2

Prerequisite: determinism item 1 passed local gates and all four native jobs;
the results and source revisions are checked in at `cf8d69e7b9`. This feature
does not include freezing cached heaps or implementing a separate collector.

## Allocation and collection

- Give each instance an allocator cache independent of the processor executing
  its goroutines. Moving between processors must not move its allocation owner.
- Small and large object spans have exactly one owner. The process remains
  owner zero. The shared page allocator and GC remain process machinery; object
  placement and central-list reuse must never mix owners within a span.
- Tiny allocation blocks must not combine host and instance objects. Initially
  disable tiny packing for instance allocations rather than introduce another
  hidden GC root in each cache. Preserve the ordinary process fast paths.
- Keep owner caches in a runtime registry that GC can flush at the correct
  sweep boundaries and include in allocation/scan accounting. The registry must
  not keep instances alive. A separate GC-visible cache handle can retire the
  non-GC cache after the group becomes unreachable, including groups with
  cyclic transport callbacks.
- Reuse compatible partial spans through the existing central lists. A bounded
  owner search may allocate a fresh span; measure fragmentation before tuning
  it or adding owner-specific central queues. Never retag a span containing live
  objects. Fully swept empty spans may change owner.
- Keep ordinary concurrent tracing and sweeping. Owner tags prepare for later
  cached-heap GC work, but this feature must not skip tracing or free a heap
  while runtime queues can still reference it.

## Compiler and boundary checks

- Extend the build manifest beyond its POC package allowlist. Every reachable
  mutable global needs instance state or an explicit process-service policy;
  an unclassified state access must fail closed.
- Instrument pointer reads/writes, pointer publication, slice/closure/interface
  access, maps, channels, reflection, and bulk copies. Enforce the same owner
  across all supported access paths, including small objects and interior
  pointers. Host execution keeps its ordinary behavior for process objects.
- Retain the copied-byte `Call` boundary and typed, compiler-generated invokers.
  Workflow arguments and results are converted within their instance. Host
  callbacks and allocator/runtime metadata use explicit trusted paths.
- Share immutable code, static type metadata, and proven read-only data. Do not
  treat arbitrary process-owned heap objects as immutable merely because their
  owner is zero.

## Process metadata services: accepted policy

Reflection and protobuf use process-wide type registries and lazy caches. Some
hold locks shared with the host, and some allocate metadata on their first use.
The POC currently classifies the default converter dependency graph as a process
service; that broad exemption is insufficient for enforced heap ownership.

The user approved a small audited set of trusted metadata operations:
their shared caches and immutable metadata remain process-owned, decoded user
values remain instance-owned, and revocation waits until a service has released
its process locks before discarding the caller. Ordinary application locks and
mutable objects remain private to an instance. The no-shared-locks rule applies to application locks.
Shared service access is not an application privilege: only compiler/runtime-approved metadata operations
may enter it. Arbitrary converters, callbacks, and mutable workflow values may
not inherit the service scope. Service entry precedes acquiring any shared lock;
exit follows releasing every such lock. Nested calls restore the original owner
and a pending Kill takes effect at the outermost exit.

Keep the service manifest narrow and versioned. Auditing a new dependency version
is required before granting its implementation service access. The broad POC
converter dependency exemption must not be mistaken for a completed ownership
audit.
## Implemented foundation

The runtime now gives each instance its own allocator cache. Generated fast paths
and generic/race allocation paths select the same cache; small and large spans
retain a single owner, including interior pointers. Instance tiny packing is
disabled. GC sweep preparation, scan/allocation accounting, profiling and
`ReadMemStats` include these caches. An acyclic finalizable handle retires an
unreachable cache without freeing still-live heap objects.

The Temporal adapter also constructs the pinned default converter separately
in each instance's workflow package state. Its mutable converter map, ordered
list and options no longer reuse the host worker's singleton. The worker keeps
its standard default object. Wire compatibility tests and the retained history
cover the independently constructed converter; its external dependency graph
still needs the broader package audit below.

Metadata scopes preserve the instance's group, clock and process restrictions,
while switching allocation and cache access to the process owner. They keep the
dispatch token while waiting on shared service locks. Kill remains pending until
outermost service exit, after lock cleanup; application continuation and defers
then remain discarded. A scope cannot start application goroutines, call the
host or exit the process. Runtime housekeeping goroutines, including GC workers
started by service allocations, remain outside the instance. Nested scopes and
panic unwinding restore the original owner.

### Initial service manifest

- Reflection type construction: `PointerTo`, `ChanOf`, `FuncOf`, `SliceOf`,
  `StructOf`, `ArrayOf` and `MapOf`. Canonical type descriptions are shared;
  reflected values and caller callbacks keep instance ownership.
- Protobuf `google.golang.org/protobuf@v1.36.11`: explicitly listed lazy
  descriptor/index builders, message/extension type initialization, and registry
  lookup/count methods. The exact function list is in
  `src/cmd/internal/isolatepolicy/metadata.go`.
- Generated Temporal descriptors: `go.temporal.io/api@v1.63.6`. Both modules
  require these audited versions, without replacements, nested modules or vendored source.
- Shared operations reject instance/stack receivers. Message initialization
  accepts only the built-in protobuf descriptor implementation. Dynamic call
  targets inside scopes must belong to the reviewed runtime/library or pinned
  metadata namespaces; custom descriptor callbacks are rejected before execution.
- Registry mutation/visitors, legacy descriptor implementations, lazy option
  decoders and custom descriptors remain unsupported inside instances. Ordinary
  host registration, visitors and custom descriptors retain their behavior.

This foundation does **not** complete feature 2. General pointer access and
publication checks, immutable metadata provenance, and replacement of the broad
POC converter/activity package exemptions still require implementation and audit.
An owner-zero allocation alone is not proof of safe immutable sharing.

## Initial audit

- `malloc.go` and generated `malloc_generated.go` both fetch the processor's
  cache directly. Update `_mkmalloc` and regenerate its output; changing only
  the generic allocator leaves the normal fast paths mixing owners.
- `mcentral.cacheSpan` currently returns any compatible size-class span.
  Owner matching must cover swept and unswept partial/full paths, not merely
  newly grown spans. Preserved empty spans can be retagged after sweeping.
- GC resets scan accounting at mark termination and prepares caches for the
  new sweep generation. It also flushes idle caches before the next cycle.
  Owner caches must participate without keeping their groups alive.
- `profilealloc` updates per-cache sample counters, and `ReadMemStats` flushes
  allocation counts. Selecting an owner cache for allocation must also select
  the correct profiling counters and include it in those flushes.
- The existing selected package layouts already include reflection and time;
  the converter/activity dependency graphs remain broad process exemptions.
  Narrowing those exemptions depends on the process-service policy above.

## Gates

1. Allocation tests cover tiny/small/large, scan/noscan, interior pointers,
   processor migration, concurrent instances, repeated GC, cache retirement,
   and allocation statistics. Test both generated fast paths and race paths.
   `isolate.TestIsolateCachedEviction` additionally holds 1,024 real deterministic
   instances suspended with 64 KiB of live heap state each, forces repeated GC,
   resumes instances to verify state, then evicts them. Three batches check weak
   instance references, zero live members, allocator-registry counts, goroutine
   counts and live heap reclamation. Short runs use 256 instances/two batches.
   Freed pages retained by Go for reuse are excluded from the live-object test.
2. Negative tests reject host/instance and instance/instance crossings through
   every supported language and reflection operation. Positive tests retain
   immutable sharing and copied-byte transport.
3. Package-state tests use the same package on the host and in two instances,
   including initialization, caches, callbacks, and default converter use.
4. Run compiler/runtime regression and lock-ranking gates, SDK/sample tests,
   and the retained determinism history on the native platform matrix. Ownership
   work must preserve the item 1 trace and ordinary Temporal workflows.
5. Check in results and all three repositories; publish the supported service
   manifest and any remaining exclusions before claiming feature 2 complete.

## Foundation validation (2026-10-05)

- Full `src/all.bash`: `ALL TESTS PASSED` on Linux arm64. This run preceded
  addition of the read-only allocator-count diagnostic and cached-eviction test;
  those additions passed the focused normal/race/lock-ranking gates below.
- Cached eviction: three batches of 1,024 suspended instances passed. Each
  retained approximately 69.6 MB of live heap; after eviction, all weak instance
  references cleared, allocator-cache count returned to zero, goroutine count
  returned to two, and live heap returned to approximately 1.1 MB.
- `go test -race runtime isolate -run
  'TestIsolateAlloc|TestIsolateMetadata|TestIsolateCachedEviction|TestDeterministic|TestReflectionTypeRegistryAcrossIsolates'
  -count=5`: passed. The cached test alone created/evicted 15,360 instances.
- The same filter with `GOEXPERIMENT=staticlockranking`, `-short -count=5`:
  passed. Metadata revocation/suspension also passed ten ordinary repeated runs.
- Marked/legacy compiler scripts and the new metadata-source policy script:
  passed. SDK tests, tracked sample packages, and the real SDK dispatcher driver
  passed; the dispatcher driver also passed under the race detector.
- Six fresh-process saved-history replays (`GOMAXPROCS=1/2/8`, default/disabled
  CPU features, host `TZ=America/Los_Angeles`): all retained 195 observations and
  SHA-256 `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.

The native CI workflow now includes cached eviction, allocation/metadata race
and lock-ranking gates, rejected metadata-source builds, and the SDK driver
under the race detector. Its next run validates this ownership foundation on
Linux/macOS arm64/amd64; these results do not close the remaining feature 2 gates.

The first native foundation run (`37347075109`, Go `4891462c93`, SDK
`27e2eb5`) passed both Linux jobs and exposed an existing Darwin preemption
lock-ranking mismatch on both macOS architectures: `preemptM` acquires the exec
read lock with `sched`/`allp` held, while the table placed the read lock earlier.
The audited thread-creation read section only calls OS/C thread-start code.
The corrected DAG places `execR` after `allp`; a regression exercises this order
on every platform. Full runtime/isolate short suites with static lock ranking
passed locally after the correction. The native rerun must pass before this
foundation's platform validation is complete.

The second native run (`37348765744`, Go `52aa269caa`, SDK `52210a4`)
passed macOS arm64 and exposed a GC startup regression in both Linux SDK driver
jobs. The service goroutine restriction also rejected runtime GC workers when
the first collection was triggered by a reflection metadata allocation. The
restriction now uses the same system-goroutine classification as creation;
runtime workers never inherit the instance, while application starts still fail.
A fresh-process regression covers cold startup and additional workers after
increasing `GOMAXPROCS`, checks instance membership and scope restoration, and
separately tests application goroutine rejection. The allocation/metadata,
determinism and cached-eviction race and static-lock-ranking gates passed five
local repetitions after this fix. The SDK driver also passed at
`GOMAXPROCS=1/2/8` with both default GC and `GOGC=1`.
