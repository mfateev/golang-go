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

### Heap access diagnostic

The compiler's opt-in `-d=isolateheap=1` diagnostic now checks ordinary typed
loads, stores, zeroing and bulk moves, plus map/channel operations and every
select operand before the operation. Map checks include stack-backed headers,
missing-key lookups, length, iteration, assignment, deletion and clearing.
Channel checks precede send, receive and close, and cover length/capacity.
Current-instance access is allowed; host/instance and instance/instance heap
access is rejected. Metadata builders may read borrowed private arguments from
their own instance, but cannot mutate them or access another instance's data.

Level two (`-d=isolateheap=2`) additionally validates references in typed stores
and bulk moves, including pointers, slice/string backing data, interface data and
closure objects. It scans the compiler type's GC bitmap for pointer-bearing bulk
copies. Read-only borrowing by a metadata builder cannot retain private arguments
in process caches. A reference to an instance object cannot be stored in a
linker-allocated process global. Large returned structures are copied to a local
temporary before diagnostic calls can overwrite their ABI result/spill area.

The diagnostic is **not enabled by normal isolate builds**. General stack/static
ownership, complete publication coverage, raw compiler accesses, reflected operations,
library intrinsics and assembly still require coverage. Process metadata beyond the canonical reflection roots below
still needs positive provenance before sharing; owner zero grants no blanket
read exemption here. Collection checks emitted in application code do not audit
the runtime and library operations it calls. The copied-byte bridge remains a
trusted path. These checks are a way to test the next ownership layer, not a
completed containment claim.

Compiler regressions exercise actual language operations with deliberately
exposed foreign pointers/maps/channels, verify rejection before mutation, and
retain ordinary own-state and nil behavior. A multiple-result regression ensures
inserted checks preserve call-result extraction and large returned structures.

Local validation passed the full short runtime/isolate/map and compiler
typecheck/SSA suites, five runtime ownership repetitions under both race and
static lock ranking, and the language-operation compiler script. The SDK
dispatcher driver compiled with the diagnostic on all `sdk-go-poc` packages and
passed at `GOMAXPROCS=1/2/8` with `GOGC=1`. Native CI includes these gates and
preserves their output alongside ordinary builds and saved-history replay.

Level-two store/move regressions also passed, including a late foreign pointer
in a 10,000-pointer object, instance/instance and host/instance reference checks,
and preventing a metadata service from retaining borrowed data. These runtime
checks passed five race and static-lock-ranking repetitions. Pointer-bearing
multi-result structures exposed an ABI spill-area overwrite; a local snapshot
before inserted calls fixed the regression.

The first stricter SDK audit rejected a dynamic `reflect.Type` returned by
`ArrayOf` when stored in a `StructField`. The canonical-root policy and subsequent
panic/entry fixes below resolve those observed failures. Native CI now uses level
two for SDK application packages; the complete external dependency graph still
requires its separate audit. Owner zero never grants blanket immutable sharing.

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
Linux/macOS arm64/amd64; the checkpoint below records those results, which do not
close the remaining feature 2 gates.

The first native foundation run (`37347075109`, Go `4891462c93`, SDK
`27e2eb5`) passed both Linux jobs and exposed an existing Darwin preemption
lock-ranking mismatch on both macOS architectures: `preemptM` acquires the exec
read lock with `sched`/`allp` held, while the table placed the read lock earlier.
The audited thread-creation read section only calls OS/C thread-start code.
The corrected DAG places `execR` after `allp`; a regression exercises this order
on every platform. Full runtime/isolate short suites with static lock ranking
passed locally after the correction. The native checkpoint below includes this
correction.

The second native run (`37348765744`, Go `52aa269caa`, SDK `52210a4`)
passed both macOS jobs and exposed a GC startup regression in both Linux SDK driver
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

Goroutine entry classification subsequently stopped constructing a temporary
`g` in `newproc`. ARM64 disassembly confirms its frame fell from 672 to 80 bytes;
ordinary goroutine creation keeps its small frame even when no service is
active. Cold GC/service rejection tests passed five race and lock-ranking
repetitions, and finalizer/cleanup/goroutine/Goexit regressions passed.

## Native foundation checkpoint (2026-10-05)

[Native run 37350494589](https://github.com/mfateev/golang-go/actions/runs/37350494589)
passed every job with Go `0859219659163634ca710c6772112a481e9a369e`, SDK
`52210a45ba52fb5b47fb94fb642b87831653966a`, and samples
`1e77ee7ed61514455a3382b0bbe0e9050468a214`.

| Platform | Result |
| --- | --- |
| Linux AMD64 | Passed |
| Linux ARM64 | Passed |
| macOS AMD64 | Passed |
| macOS ARM64 | Passed |

All jobs passed runtime/compiler conformance, cached eviction, race and static
lock ranking, SDK/sample tests, and ordinary/race SDK dispatcher drivers. Across
24 fresh-process replays, each retained 195 observations and SHA-256
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
Artifacts preserve complete logs and checked-out revisions. This validates the
allocator/metadata/eviction foundation; the later compiler heap diagnostics have
their own native gates, and feature 2 remains incomplete.

The level-one heap/collection diagnostic subsequently passed all four jobs in
[native run 37352477411](https://github.com/mfateev/golang-go/actions/runs/37352477411),
Go `7ac505c77ed281f9e05503e1e0e0a44f63333dc6`, with the same SDK/sample revisions.
This adds the negative language-operation script and SDK driver compiled with
the diagnostic at `GOMAXPROCS=1/2/8`, `GOGC=1`. It does not validate later
level-two publication work or close the outstanding ownership gates.

## Boundary response ownership

`Call` clones both response bytes and nonempty error text under the receiving
instance's restored owner. Constructing `errors.New` alone would retain the
host's string backing allocation. `internal/isolatebridge.TestBoundaryResponseOwnership`
checks the payload, error object and message allocation owners in concurrent
and deterministic instances. Native CI includes this regression in ordinary,
race and static-lock-ranking runs.

## Canonical reflection descriptor provenance

The seven supported reflection constructors now register their canonical result
with the runtime after cache insertion and shared-lock cleanup, before restoring
the caller's owner. Registration accepts only process-owned allocation roots and
records the ABI descriptor header's exact extent. The registry shares the
existing reflection-offset lock. It holds GC-visible roots with the same process
lifetime as reflection's canonical caches, preventing an unrelated object from reusing an address with previously
granted provenance.

The heap diagnostics allow references to these exact roots to enter instance
objects and allow reads within their registered header ranges. Instance writes,
interior-pointer publication, reads beyond the header, and unregistered objects
remain rejected. Allocation-slot lookup accounts for malloc headers on larger
pointer-bearing allocations; it never grants access to the runtime header or
size-class padding.

This policy does not freeze or approve the entire reachable metadata graph.
Names, struct-field arrays, equality closures, lazy GC masks and protobuf handles
still need separate accessor/provenance policies. The subsequent string-panic transfer fix below makes the SDK application
diagnostic pass at level two. That gate does not instrument its entire external
dependency graph or close the broader package audit. Default builds continue to leave these
partial diagnostics disabled.

`runtime.TestIsolateHeapCanonicalTypes` covers concurrent cold/cache construction,
canonical identity, every constructor, method-bearing struct types with malloc
headers, bounded reads, rejected writes/publication and an unregistered metadata
lookalike. The compiler language-operation script also exercises retaining the
canonical interfaces in owned storage and rejects mutation/interior publication.

The later publication diagnostic checkpoint
[native run 37355982299](https://github.com/mfateev/golang-go/actions/runs/37355982299)
passed all four platforms at Go `8c69f7224a8b00c5f5c2cd55a8a8f3cf0d8b047e`.
The receiving-owner `Call` error fix also passed all four platforms in
[native run 37357047704](https://github.com/mfateev/golang-go/actions/runs/37357047704),
Go `54d7cf934a4f91b79e1a7f63b1d0e2237aad6eaa`. Both used SDK `52210a4`
and samples `1e77ee7`. Their SDK gate uses diagnostic level one; level-two
language/runtime tests do not imply that the full SDK passes level two.

Canonical descriptor roots passed local publication/access tests, five race and
static-lock-ranking repetitions, the compiler heap/metadata scripts, and full
short suites for runtime, isolates, reflection, maps and SSA generation. These
results include the malloc-header correction exposed by the reflection suite.
The SDK full suite and level-one driver passed at `GOMAXPROCS=1/2/8`, `GOGC=1`.
Six freshly built saved-history replays retained the same 195 observations and
SHA-256 `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.

## Compiler-lowered slice copies

The heap diagnostic now validates compiler-lowered slice copies, make-and-copy,
append growth and clearing. Pointerless `memmove` and `slicecopy` paths validate
both accessed ranges; clearing validates the destination. Typed copying and
allocation/growth helpers additionally scan every copied pointer at level two.
The count is the smaller source/destination length, so zero-length operations
retain their usual semantics and untouched tail elements are excluded. Range
multiplication checks overflow before inspecting memory.

A not-yet-allocated destination uses the caller's owner for publication checks.
Every reference in the copied elements is validated before the actual copying
helper runs,
so a late foreign pointer cannot cause a partially updated destination. Direct
SSA append-growth calls and lowered IR calls both receive the check. This remains
an opt-in diagnostic; raw/reflected operations and the broader package audit are
still outstanding.

`runtime.TestIsolateHeapSliceCopies` checks the process/two-instance access matrix,
empty copies, truncated tails and a foreign last reference in 10,000 elements.
The compiler script covers overlap, empty copies, owned copy/append/clear,
foreign byte/pointer copies, make-and-copy, append with and without growth,
clearing, canonical type interface copying, and late-reference rejection before
any destination element changes. Test-only allocation helpers force heap-backed
fixtures so separate stack-policy exclusions cannot hide a missing heap check.

Slice-copy validation: the compiler language-operation script and five normal,
race and static-lock-ranking ownership repetitions passed. After restoring the
execution environment, full short runtime/isolate/bridge/reflection/map/compiler
suites passed, including ptrace and local-socket tests. The SDK level-one driver
passed at `GOMAXPROCS=1/2/8`, `GOGC=1`. The first broad run under the restricted
execution profile failed because ptrace and socket creation were denied;
no tests were skipped or changed to accommodate that profile.

## Metadata string panic transfer

A service may build its diagnostic panic string under owner zero. At outermost
service exit, after shared-lock cleanup and restoring the caller's allocator,
the runtime copies a propagating string panic's interface box and backing bytes
into the caller's heap. Named strings retain their exact dynamic type and text.
Nested scopes defer transfer until the outermost exit; ordinary host execution
and already-private/static payloads retain their behavior. Another instance's
allocations are never absorbed by this transfer.

This is a narrow string-payload policy. Non-string service panics still need a
separate safe transfer policy; it does not authorize arbitrary process-owned
error objects or panic values. Runtime regressions check dynamic/named strings,
nested scopes, allocation owners and lock cleanup. The compiler script recovers
a real `reflect.StructOf` panic into heap-backed application storage, where
level-two publication checks reject an untransferred process-owned box.

The SDK application's stricter audit exposed this boundary in a rejected custom
descriptor callback. Native CI now compiles the SDK application packages with
`-d=isolateheap=2` (previously level one). Its external dependencies retain their
separate service/ownership audits; successful application checks are not proof
that feature 2 is complete.

## Function entry metadata transfer

`Handle.ProgramWithHandle` creates a trusted entry wrapper that passes a copy of
the compiler-created handle to a dispatcher. The SDK factory now uses a
noncapturing dispatcher, avoiding an application read from a host-owned closure
containing the handle. The wrapper is not inlined into instrumented callers.
Its handle contains sealed compiler-created function metadata; other captured
application state continues to require the ordinary ownership rules.

The marked-function compiler script exercises the wrapper under race detection
and level-two host-package instrumentation, checks the original workflow
signature and state isolation, and rejects a nil dispatcher. Native SDK driver
race builds now also use level-two application instrumentation. Existing
`Handle.Program` and ordinary Temporal worker registration remain available.

Panic/entry validation: five race and static-lock-ranking ownership runs passed,
including cached eviction (15,360 real instances in the race gate). The marked
function, heap and metadata compiler scripts passed. Full short runtime,
isolate, bridge, reflection and map suites passed. The full SDK suite passed;
its level-two driver passed normally and under race detection at
`GOMAXPROCS=1/2/8`, `GOGC=1`. Six level-two fresh-process history replays retained
195 observations and SHA-256
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.

The preceding canonical-root checkpoint also passed all four native jobs in
[native run 37360385290](https://github.com/mfateev/golang-go/actions/runs/37360385290),
Go `76997944b0a4a94a3b327ad685f70fdd58b3570b`, SDK `52210a4`, samples `1e77ee7`.
That checkpoint predates the slice-copy and panic/entry changes above.

## Reflection metadata strings

Public reflection accessors copy process-heap strings into the calling instance:
type descriptions, method/field names, package paths and struct tags. This permits
ordinary application storage and use without approving the metadata object's
entire reachable graph. Static strings and strings already owned by the caller
retain their backing storage. Host callers and metadata builders retain the
canonical process storage. Strings belonging to another instance are rejected.

Repeated access to a dynamic description can therefore allocate a new string in
the instance; the POC does not add an instance-local accessor cache. Canonical
type identity and constructor caching remain unchanged. Field arrays, equality
closures, lazy GC masks, protobuf handles and non-string service panics still
require their separate policies.

`TestIsolateMetadataStringOwnership` verifies backing owners and text for a
dynamic type description, field name, tag and package path, then constructs
another canonical type using the returned private name. The compiler script
stores returned fields and descriptions in heap-backed application objects under
level-two checks, covering the actual publication path.

## GC safety of inserted entry checks

Result slots in functions with defers are live to GC from function entry. The
compiler now initializes these slots with direct SSA stores/zeroing before
loading closure captures, and emits no ownership calls during initialization.
An inserted call can grow the stack even when its argument is nil, so checking
a zero store before finishing initialization is unsafe. Normal body stores
retain their ownership checks.

The SDK level-two driver reproduced an invalid pointer in `RunFunction` during
stack growth. The same failure appears in the Linux arm64 and macOS arm64 logs
of [native run 37364058144](https://github.com/mfateev/golang-go/actions/runs/37364058144).
Those failures are compiler bugs, not container permission failures. The heap
compiler regression now exercises multiple pointer-bearing result types,
closure captures, defers, nested frames, stack reuse and concurrent GC.

Reflection/entry validation: the full toolchain bootstrap and short runtime,
isolate, bridge, reflection, compiler SSA/generation/typecheck suites passed.
Marked/legacy function, metadata and heap compiler scripts passed. Five race
and static-lock-ranking ownership runs passed, including 15,360 cached isolate
evictions under race detection. The SDK full suite and level-two driver passed;
the driver passed normally and under race detection at `GOMAXPROCS=1/2/8`,
`GOGC=1`. Six level-two fresh-process history replays retained the same 195
observations and SHA-256
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
The prior native failures require a new run of the corrected source; local
results do not establish the four-platform gate.

## Direct atomic operations

The heap diagnostic checks direct `sync/atomic` primitive calls at both compiler
boundaries: intrinsic expansion and ordinary calls (including race-intercepted
calls and pointer operations with write barriers). Checks cover the full scalar
width and precede loads, stores, add, swap, compare-and-swap, AND and OR. Inlined
typed atomic methods receive the same checks. Level two checks the new reference
before pointer store/swap/compare-and-swap, including an unsuccessful comparison;
it never permits retaining another owner's pointer based on the comparison.

This closes an observed host mutation through `atomic.Int64.Store`. The compiler
script exercises host/instance and instance/instance scalar access, host access
to instance storage, pointer publication, unchanged rejected destinations and
owned 32/64-bit, boolean, uintptr and pointer operations. Canonical reflection
descriptor pointers retain their explicit immutable-sharing policy. Race builds
exercise the non-intrinsic path.

Indirect calls through function values, typed methods that remain in
uninstrumented dependency code, `atomic.Value` and runtime/internal assembly still
need their separate audit. This diagnostic remains opt-in and does not replace
the outstanding static/stack and whole-program policies.

Atomic validation: the heap/marked-function compiler scripts and compiler
SSA/generation suites passed; `sync/atomic` passed normally and under race
detection. The SDK level-two driver passed at `GOMAXPROCS=1/2/8`, `GOGC=1`,
and under race detection at `GOMAXPROCS=8`, `GOGC=1`. The full SDK suite and
all tracked sample packages passed. These local checks do not establish the
complete memory-ownership or native platform gates.
