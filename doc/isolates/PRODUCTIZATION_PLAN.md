# Productization plan for Go isolates and the Temporal SDK

Status: proposed release plan; POC baseline updated 2026-10-10. This follows the trusted Temporal POC in
[TEMPORAL_POC.md](./TEMPORAL_POC.md) and the runtime contract in
[ISOLATE_API.md](./ISOLATE_API.md).

## Release target and current baseline

The first supported release is for **reviewed Temporal workflow code**. A
worker statically links workflow functions marked `//go:isolate`, runs many executions
in one Go runtime, and sends external effects through the host and Temporal
Go SDK. Replay must work across supported CPU architectures. The release must
say which Go and standard-library APIs are supported and reject unsupported
effects before they reach process state. It must not claim safety for hostile
tenants or bounded termination of uninterrupted CPU loops.

Today `golang-go` discovers marked functions during ordinary builds, supplies
selected per-instance package globals, a copied-byte `Call` boundary, and
whole-group revocation and cleanup. Opt-in deterministic mode now provides FIFO native
goroutines, reproducible select, canonical integer/string map iteration, and
exact suspend/resume fences. The Temporal adapter enables it and handles all
concurrent commands before ending a workflow task. Its concurrent activity and
timer example and the SleepForDays signal path completed on a local server and
replayed in fresh Linux arm64 processes at different GOMAXPROCS settings.
Dispatcher stress and race tests passed. See
[NATIVE_DETERMINISM_PLAN.md](./NATIVE_DETERMINISM_PLAN.md) for the contract and
evidence. Native Linux/macOS arm64/amd64 conformance and saved-history replay
passed on 2026-10-05. The runtime still uses
one shared Go collector with owner-specific heap spans. Lifecycle failures are
contained within the instance, and the SDK retires local callbacks on completion
and eviction. Resource controls are implemented; operating targets, unreviewed
runtime/library operations and remaining Temporal features retain separate
release gates.

The first release keeps static linking and one selected dependency version per
module/import path. Independently loaded `.so` programs, two versions behind
the same import path, snapshots, migration, hostile-code containment, and
forceful termination of non-yielding code remain separate future work.

## High-level productization features

For the first production release supporting trusted Temporal workflow code:

1. **Complete determinism:** harden map iteration, select, goroutine scheduling,
   time, and cancellation; verify replay across supported platforms and runtime
   versions. The POC implements the core mechanisms; release work expands their
   coverage and validation.
2. **Memory ownership:** isolate-owned package state and heaps, with checks
   preventing mutable objects from crossing isolate boundaries.
3. **Efficient GC for cached workflows:** freeze suspended isolates, avoid
   repeatedly tracing their internal memory, and reclaim an entire isolate safely
   on eviction. This requires heap ownership first. Frozen memory still counts
   toward memory limits, and references to shared objects must remain visible to
   GC.
4. **Enforced effect restrictions:** reject unsupported I/O, randomness, process
   state changes, and unsafe operations; provide replay-aware logging.
5. **Reliable lifecycle:** handle startup failures, panics, exit, cancellation,
   shutdown, and eviction across every goroutine and pending operation.
6. **Resource controls:** account for memory and goroutines, enforce limits, and
   diagnose CPU loops or stuck isolates.
7. **Complete Temporal integration:** support retry and failure semantics,
   continue-as-new, workflow versioning, queries, updates, and child workflows.
   Publish explicit exclusions for any features deferred beyond the first release.
8. **Converter support:** safely use worker-configured data converters and codecs
   inside isolates, including protobuf and type validation.
9. **Stable integration contracts:** version the compiler metadata, isolate API,
   and host protocol; establish a supported Temporal SDK integration.
10. **Operational visibility:** metrics, isolate stack traces, replay diagnostics,
    and tools for investigating hangs and memory growth.
11. **Release and compatibility testing:** reproducible toolchains, native platform
    CI, stress and soak tests, saved-history replay, and upgrade/rollback checks.

The highest-priority foundations are **memory ownership, lifecycle safety, and
enforced effect restrictions**. Dynamic loading, independent dependency versions,
snapshots, and hostile-code containment remain later enhancements.

### Current implementation sequence

Implement high-level feature **1 (complete determinism)** first. Its acceptance
gates include runtime/library and compiler regression tests, repeated dispatcher
race tests, SDK/sample tests, real-server recording, and fresh-process replay of
the same history on native Linux/macOS arm64/amd64. The implementation, fixtures,
CI, documentation, and validation results must be checked in before beginning
high-level feature **2 (memory ownership)**. These feature numbers refer to the
list above, not to the milestone numbers below.

Feature 1's local and native gates passed on 2026-10-05; its implementation,
history fixture, sample fixes, and CI are checked in and pushed in all three
repositories. See the native validation checkpoint in
[Native isolate determinism](./NATIVE_DETERMINISM_PLAN.md). Feature 2 is also
complete: compulsory dependency-wide memory checks, instance allocator caches
and package state, precise immutable metadata sharing, audited process services,
and fatal cross-owner error reporting. Full `src/all.bash`, cached-instance
reclamation stress, SDK/sample suites and all four native Linux/macOS arm64/amd64
jobs passed on 2026-10-06. See the acceptance evidence in
[Memory ownership](./MEMORY_OWNERSHIP_PLAN.md).
Feature 3 (frozen-heap GC) is deferred at the user's request. Feature 4
(enforced effect restrictions) is complete: compulsory dependency-wide effect
checks, worker-configured replay-aware logging and fatal Workflow Task failure
reporting. Full `src/all.bash`, compiler integration, race/lock-ranking stress,
SDK/sample tests, live-server failure-history checks and all four native
Linux/macOS arm64/amd64 jobs passed on 2026-10-07. See the acceptance evidence in
[Effect restrictions](./EFFECT_RESTRICTIONS_PLAN.md).
Feature **5 (reliable lifecycle)** is complete for the agreed scope: runtime and
definition cleanup, panic containment, startup and pending-termination
diagnostics, and SDK callback retirement. Full Go, SDK/sample, live-server and
all four native gates passed on 2026-10-07. Automatic worker-shutdown integration
is deferred at the user's request: the pinned SDK's shared sticky cache is not
evicted by `Worker.Stop`, so safe per-worker cleanup needs an SDK hook or an
explicitly narrower process-wide shutdown contract. See
[Reliable lifecycle](./LIFECYCLE_PLAN.md) for the scope decision and evidence.
Feature **6 (resource controls)** is complete for the agreed scope: host-only
memory/stack/runtime accounting, memory and goroutine
limits, active-task duration and progress watchdogs, and worker/replayer policy
with copied observer events. Limit violations fail the Workflow Task. Full local
Go, SDK/sample, real-server, 10,000-instance density and all four native platform
checks passed on 2026-10-07, with all 24 native replay results unchanged. See
[Resource controls](./RESOURCE_CONTROLS_PLAN.md) for scope and validation.
Feature **7 (complete Temporal integration)** is complete for the agreed POC
scope: SDK-compatible feature and interceptor APIs, native concurrency, queries
and validators that cannot mutate workflow state, completed-query retention,
child workflows, continuations, versioning, local activities, external operations,
sessions, Nexus and tracing sinks. SideEffect APIs remain intentionally excluded;
documented native Go API differences and limitations remain release constraints.
Feature **8 (converter support)** is complete for the agreed POC split:
worker-configured isolate serializer factories and host-owned codecs, with
serialization contexts and structured failure transport. Custom failure
converters, batch codecs and arbitrary converter I/O inside isolates remain
excluded. See the SDK README and INTERCEPTORS.md for contracts and checks.
Feature **9 (stable integration contracts)** versions compiler metadata, the
isolate API, determinism and the startup byte protocol independently. The
Temporal SDK integration uses supported hooks in an audited, pinned SDK fork
instead of private field access. Upgrade/rollback replay checks keep both older
and candidate histories. See [Integration contracts](./INTEGRATION_CONTRACTS_PLAN.md).
Feature 3 remains deferred. The validated determinism contract still excludes
its documented unsupported operations. Operational visibility is the next
feature; these completed POC subsets do not by themselves imply release readiness.

See [Native isolate determinism](./NATIVE_DETERMINISM_PLAN.md) for the supported
operations and reproducibility contract. Unsupported map key kinds and
machine-dependent effects require explicit restrictions rather than an implied
promise that arbitrary Go code can replay.

### Feature 4 policy decisions (2026-10-06)

| Area | Agreed policy |
|---|---|
| Printing and standard logging | Forward through a dedicated host call, with routing and handling configured on the worker. Preserve replay-aware handling. |
| Environment and file configuration | Reject workflow reads; pass configuration as workflow input so history records it. |
| Unsafe and native escape paths | Reject application escape paths; permit explicitly audited implementations in the runtime and supported libraries. |
| Enforcement | Compile-time diagnostics for identifiable forbidden operations, plus runtime enforcement for indirect and reflected calls. Ordinary host code and activities retain their existing behavior. |
| Violation handling | Terminate the isolate and report a Workflow Task failure with the offending operation and stack trace. Workflow code cannot recover the violation. |

The logging call carries copied bytes and metadata across the isolate boundary;
worker loggers, writers and callbacks remain host-owned. Replay information must
come from the host so worker configuration can control duplicate output. The
implementation must define deterministic return behavior for printing APIs that
return a byte count or error, including host sink failures.

All five policy decisions are agreed. A forbidden operation must be rejected
before its effect occurs; revocation and trusted cleanup must preserve the
memory-ownership and lifecycle guarantees established by feature 2.

Dependency auditing and runtime hooks now enforce the supported operation
matrix, including application finalizers, cleanup callbacks and unaudited
native/unsafe entry points. Feature 4 is complete; see its
[operation matrix, audit boundaries and acceptance evidence](./EFFECT_RESTRICTIONS_PLAN.md).

### Feature 7 API policy (2026-10-07)

Stay as close as possible to the current pinned Temporal Go SDK. Match its API
names, option types and fields, defaults, error types and behavior for activities,
retries, continue-as-new, versioning, child workflows, queries and updates.
SDK API innovations are deferred until after this compatibility work.

Differences should follow the native Go execution model already agreed for the
POC: standard `context.Context`, `go`, channels, `select`, and deterministic
`time` replace the SDK's workflow context and concurrency/time abstractions.
Activities use SDK-style futures; ordinary Go goroutines and channels adapt
them for native select. Typed activity helpers are retained as comments at the
user's request. Avoid adding
new API variations beyond those needed for these native replacements.

Use SDK-compatible activity options and context option helpers when expanding
the provisional timeout-only activity API. Preserve existing worker/client
registration behavior and ordinary workflow support. Continue delegating history
processing and Temporal command semantics to the host Go SDK; the isolate byte
protocol carries the corresponding operations and structured outcomes.

### Feature 7 read-only query enforcement (2026-10-08)

Implemented requirement: query handlers must not change workflow state. Invoke them
at the existing suspension boundary, when all workflow goroutines are already
blocked, and keep workflow execution and host-event delivery suspended until
the query finishes. A separate pause mechanism is not required.

- Permit reads of existing workflow state and mutation of local variables and
  objects allocated during the query. A local pointer, slice, map, or interface
  alias to existing state does not make that state writable.
- Allocate temporary query objects under a distinct query owner. Allow the
  query to borrow workflow state for reads, without granting writes or allowing
  query-owned references to escape into workflow state.
- Enforce the restriction through compiler checks and runtime mutation checks,
  including indirect and reflected writes, map/slice updates, atomics, and
  channel or lock operations that change existing workflow objects or wake
  blocked workflow goroutines. Reject mutations before they occur.
- Reject activities, durable timers, and other workflow commands or external
  effects. Serialize the query result across the copied-byte boundary, then
  release temporary state and stop any query helpers before resuming workflows.
- An attempted mutation fails the query without changing workflow state or
  completing the Workflow Execution. Define safe query-failure cleanup so the
  existing state remains usable for subsequent queries and workflow tasks.

Acceptance tests must cover allowed local computation, mutation through aliases
and callbacks, runtime mutation helpers, attempted goroutine wakeups, command
rejection, and repeated queries followed by normal workflow execution. These
are enforcement requirements, not a documentation-only read-only convention.
Completed workflow state with registered queries is retained until eviction, as
agreed on 2026-10-08. These rules apply whenever state is available for querying.

## Milestones and gates

| Order | Owner | Deliverable | Exit gate |
|---|---|---|---|
| 0. Define the supported contract | All three repositories | Freeze the trusted-code and platform scope, supported APIs, workload corpus, and failure semantics | Each advertised behavior has a conformance test or an explicit exclusion |
| 1. Suspend and replay native Go | `golang-go` | Exact quiescence, logical time, deterministic scheduling and observable operations | Native goroutines, channels, `select`, `sync`, timers, and supported map iteration replay across architectures |
| 2. Finish lifecycle | `golang-go`, `sdk-go-poc` | Complete revocation, child failure reporting, cleanup on worker close | A nil `Kill` proves no isolate code can run; pending kills retain a durable fence and useful diagnostics |
| 3. Own state and effects | `golang-go` | Broad package-state selection, heap ownership, cross-owner checks, and an enforced effect policy | Two instances and the host cannot exchange mutable objects through any supported path; unclassified paths fail closed |
| 4. Complete the Temporal adapter | `sdk-go-poc`, `samples-go-poc` | Stable workflow protocol, supported feature set, history and upgrade tests | Workflow tasks, replay, cancellation, and code upgrades behave as documented against a real server |
| 5. Meet operating targets | All three repositories | Resource accounting, observability, native platform CI, density and soak results | Supported workloads meet published memory, latency, and cleanup targets without unexplained leaks |
| 6. Release | All three repositories | Versioned artifacts, compatibility policy, migration and operator guides | Clean-clone build, full test matrix, old-history replay, and rollback drill pass |

The milestones are gates, not date estimates. SDK API design and test-corpus
work can proceed while runtime scheduling work is underway, but a release
cannot skip an unmet runtime gate.

### 0. Supported contract and regression corpus

- Write a support matrix for workflow operations, standard-library packages,
  architectures, failure modes, and resource limits. The trusted-code rule
  allows reviewed programs, but unsupported I/O and process effects still
  need an enforceable policy before release.
- Record histories from the existing samples and new cases for concurrent
  activities, timers, signals, cancellation, failures, and code changes.
  Replay each history in a fresh process. Keep input, command, and result
  bytes as fixtures rather than depending only on a live server.
- Define what counts as a Workflow Task failure versus a Workflow Execution
  failure. Temporal retries these differently; the bridge must preserve that
  distinction. See [Temporal's task model](https://docs.temporal.io/tasks).
- Keep the initial native platform matrix (Linux/macOS arm64/amd64) green.
  Its first conformance and saved-history replay run passed on 2026-10-05.

### 1. Exact suspension and deterministic execution

The trusted POC implements the core FIFO/suspension/select/map work. The
following release gate expands and validates it across supported APIs and
platforms, including the new deterministic sync.Map.Range and iter.Pull paths.

- Validate the implemented `Resume`/`Suspend` fence and SDK command batching:
  return only when no isolate goroutine can make progress without a host event.
  Distinguish completed, awaiting `Call`, timer-only, and deadlocked states.
  Keep millisecond polling out of the host lifecycle.
- Account for every runnable, running, parked, and waking goroutine, including
  scheduler handoffs, preemption, GC assist, `sync.Cond`, network poll, and
  runtime-internal callbacks. A host command must not leak into a later
  Workflow Task after a quiescence decision.
- Give each instance a deterministic scheduler, clock, and random source.
  Implement the selected canonical map-range contract for supported key
  types, including reflection, and reject unsupported iteration forms.
- Run the same histories on native arm64 and amd64 workers and compare
  command streams byte for byte. Test different CPU features where they can
  change behavior. Add stress cases with native `go`, channels, `select`,
  `sync.WaitGroup`, `sync.Mutex`, and timers.

### 2. Revocation and failure isolation

- Complete the execution fence on every route back into isolate code.
  Finish queue detachment and cleanup for all supported waits, including
  signal-versus-kill races and syscalls. A nil `Kill(ctx)` must be stable:
  no later wakeup can execute user code. A deadline returns a pending result
  without undoing revocation.
- Report a pending kill with best-effort stack and thread diagnostics. Make
  initializer and child-goroutine panics, `os.Exit`, uncaught errors, and
  worker shutdown produce defined host outcomes. Replace the current
  main-goroutine-only `Wait` behavior.
- Test cancellation, completion, and worker eviction repeatedly under the
  race detector. Verify that no runtime queue points to reclaimed stacks or
  instance memory. A stuck CPU loop remains a documented reason to restart
  the worker process; this release does not promise an in-process hard kill.

### 3. Package, heap, and effect ownership

- Turn `-isolate-report` into an enforced whole-program manifest. Classify
  every reachable mutable package state as instance-owned or an explicit
  process service. Replay selected initializers in dependency order and audit
  callbacks, caches, timers, `sync.Pool`, and standard-library globals.
- Add allocation ownership for small and large objects, cross-owner pointer
  read/write checks, and safe heap reclamation after lifecycle cleanup.
  Preserve immutable sharing where proven safe; do not copy large read-only
  tables into every instance.
- Enforce the supported language and effect policy at build time or with
  isolate-fatal runtime checks. Reject unclassified process I/O, entropy,
  environment, reflection/unsafe escapes, cleanup callbacks, and process
  globals before claiming broad standard-library support. A returned error
  or recoverable panic is insufficient for a containment-sensitive violation.
- Use a growing conformance suite with host-plus-two-isolate execution,
  indirect aliases, reflection, initialization, and GC pressure. A package
  being listed in a build report is not proof that it is safe.

### 4. Temporal SDK and workflow compatibility

- Stabilize the provisional operation numbers and JSON payload format as a
  documented, versioned protocol. Command IDs must survive suspension and
  never derive from addresses. Specify payload conversion, error encoding,
  cancellation, duplicate replies, and protocol mismatch behavior.
- Define the first supported Temporal feature set. Activities, durable timers,
  signals, cancellation, retries/failures, and replay are required. Add
  continue-as-new and workflow code versioning before long-lived production
  workflows. Queries, updates, child workflows, and other APIs need explicit
  support decisions and diagnostics; do not silently accept them through the
  old Go SDK API. Temporal requires deterministic replay across code changes;
  see [Workflow versioning](https://docs.temporal.io/workflow-definition#versioning-workflows).
- Keep Temporal's history processing in its Go SDK. The present adapter uses
  `internalbindings` and pins one SDK version. Establish a compatibility test
  matrix and either secure a supported extension point or maintain an
  isolated adapter with an explicit SDK upgrade gate.
- Extend the POC's `//go:isolate` entry generation to `go run`, `go install`,
  and test binaries, trim dependency selection with finer function reachability,
  and generalize the trusted SDK support-package classification. Marked
  functions now have generated typed invokers for any argument count and use
  the ordinary worker registration API; unmarked workflows retain the Go SDK
  path. The POC moves
  protobuf-serialized `Payloads` across the byte boundary and uses Temporal's
  default converter inside the isolate for nil, byte, and ordinary JSON
  values. The default converter has instance state; its dependency graph and the host
  activity SDK graph are checked, with narrow audited metadata services. General
  protobuf message values still require their value-path audit. Worker-configured
  isolate serializers, host codecs and serialization contexts are implemented;
  custom failure converters and batch codecs retain separate support gates.
  Test real worker/server execution, exported history replay, worker restart,
  eviction, and upgrades from an older worker build. Keep activities and other
  I/O in the host.

### 5. Resource limits and operations

- Measure real suspended instances, including stacks, heap, globals,
  channels, timers, and bridge records. The existing working target is
  median at most 8 KB and p99 at most 32 KB attributed retained memory per
  suspended instance, plus a separate 10,000-instance process RSS result.
  Re-evaluate the target from real ownership accounting, not the proxy alone.
- Measure GC CPU/assist, pause time, scheduler throughput, Workflow Task
  latency, replay speed, and cleanup time at 10,000 instances. Add
  per-instance memory and goroutine limits before claiming resource control.
  CPU accounting and stuck-run diagnostics must be explicit; a strict CPU
  stop limit depends on future forceful-kill work.
- Expose metrics and traces for live/quiescent/pending-kill instances,
  commands, ownership failures, replay mismatches, and worker restarts.
  Provide stack diagnostics and a process-restart runbook for pending kills.
- Run native CI on every platform advertised by the docs, including macOS if
  it remains supported. Cross-compilation alone is not a platform gate.

### 6. Release and upgrade gate

- Produce versioned, reproducible toolchain artifacts and an SDK release.
  Embed toolchain, isolate protocol, and selected-program identity in the
  worker build. Reject incompatible combinations early.
- Run the fork's full `src/all.bash`, focused race/stress suites, SDK tests,
  fresh-clone sample build, local-server integration, and cross-architecture
  history replay. The release matrix must include every supported native OS
  and architecture.
- Replay old histories after each compiler, runtime, SDK, or workflow-code
  upgrade. Exercise worker rollback and the chosen Temporal workflow
  versioning strategy before labeling the release stable.
- Publish the support matrix, known restrictions, upgrade procedure, failure
  recovery, performance numbers, and the difference between trusted and
  hostile-code guarantees.

## Release labels and deferred work

The current concurrent adapter remains a **technical preview**. A **trusted beta**
requires milestones 0–4 for a deliberately bounded workload. A **trusted
production release** also requires milestones 5–6 and an operations policy
for pending kills. A release for hostile tenants is a separate gate: a sound
Tier 1 standard-library audit, complete cross-owner containment, resource
limits, and a proven bound for forceful whole-isolate termination. Snapshot
and restore, dynamic loading, and same-import-path dependency versions do not
block the trusted release.

### Future enhancement: generic library integration adapters

Explore a standard runtime integration interface for libraries such as protobuf
that combine shared metadata, process-wide caches, and isolate-owned values.
Implement the mechanism once, then describe each supported library through a
versioned, audited adapter instead of adding library-specific compiler rules.
This is a proposal, not an implemented extension API or a trusted-release gate.

An adapter would declare:

- Immutable metadata roots and the precise regions isolates may read.
- Controlled runtime services for accessing mutable process-owned caches.
- Callback boundaries that restore ordinary isolate ownership and effect
  restrictions before invoking application code.
- Supported library versions and internal layouts, with incompatible versions
  rejected rather than granted broader access.

Existing general ownership checks would remain active. Cache services could
temporarily inspect explicitly borrowed caller data, but could not retain
isolate pointers in shared state. Application messages, decoded values, and
clone destinations must remain isolate-owned. Registration must be restricted
to trusted adapters; a package-wide exemption or arbitrary permission elevation
would undermine containment. Mutable caches also need review for deterministic
observable behavior, beyond memory ownership alone.

For protobuf, an adapter could describe message metadata and controlled cache
operations so cloning and failure conversion work without sharing mutable
message contents. Validation should cover host-plus-two-isolate execution,
concurrent cache initialization, callback permissions, and pointer retention.

Libraries with suitable hooks could call the runtime interface directly.
Libraries with private caches and no hooks may need a small library patch or
generic compiler interception support implemented once. Avoid promising that
every unmodified library can be integrated through configuration alone. The
goal is reusable enforcement with per-library adapters; each library's sharing,
initialization, callback, and effect behavior still requires an audit.

## First five implementation tasks

1. Add the history corpus and a native concurrent workflow fixture in
   `samples-go-poc`. Record the expected commands and retain a failing
   isolate replay check on arm64/amd64 until milestone 1 passes.
2. Specify the runtime goroutine-state invariant and implement the exact
   quiescence return for the fixture, including timer-only and deadlock cases.
3. Replace the SDK's one-command-at-a-time loop with the new suspend/resume
   contract and verify Workflow Task boundaries against a real server.
4. Close the remaining revocation wake paths and child-failure reporting;
   prove `Kill`'s nil result under wakeup races.
5. Make the package ownership report reject unclassified reachable state and
   effects, then expand the verified standard-library set from real samples.

### Feature 7 activity API checkpoint (2026-10-08)

Per user direction, typed activity helpers are commented out and retained for
later API work. The active API follows the pinned SDK: context option helpers,
`ExecuteActivity(ctx, activity, args...)`, `Future.Get(ctx, valuePtr)`, and
`Future.IsReady()`. Scheduling completes before ExecuteActivity returns, even
if the future is ignored. Host SDK cancellation semantics, including
WaitForCancellation, determine the future outcome. Native Go goroutines and
channels can adapt futures for select. Samples and compiled probes use this API.

Structured failures remain required feature 7 work. An ownership-checked probe
confirmed that SDK DefaultFailureConverter.FailureToError currently reads
foreign protobuf fast-path metadata during proto.Clone. Do not disable ownership
checks to accept it. Children, queries, and updates remain pending; this
activity checkpoint does not mark feature 7 complete.

Activity checkpoint validation: SDK package suite; race tests for workflow,
bridge, and worker; compiled ownership-checked driver with race detection;
all tracked sample package tests; all four saved sample histories; six
fresh-process determinism variants (GOMAXPROCS 1/2/8, CPU features on/off).
The 195-observation replay checksum remains
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
Compiler work-package tests and function/metadata build scripts passed.
Real Temporal server checks passed for ordinary SDK workflows, activity
aliases, concurrent activities, native context deadlines, and cancellation.
SDK callbacks retire their command and outcome references after consumption
or eviction; ignored futures still schedule activities before returning.

### Feature 7 continuation and versioning checkpoint (2026-10-08)

Added SDK-compatible `NewContinueAsNewError`, `NewContinueAsNewErrorWithOptions`,
workflow context option helpers, `GetVersion` and `IsReplaying`. Continuation
uses the actual SDK error type, supports wrapped errors and workflow registration
aliases, and retires the previous isolate before reporting completion. Each
new run initializes fresh state. The host SDK owns continuation commands and
version markers, including replay defaults and supported-range checks.

Compiled and live-server checks exercise a four-run chain with fresh globals,
aliased function references and inherited options. Saved histories cover
continue-as-new and final completion, an old execution without a marker,
a new execution with exactly one version marker, repeated lookup and rejection
of a recorded version outside the supported range. Replay checks compare
computed completion values against the history. Custom context propagation
retains the feature 8 boundary. Structured failures, children, queries and
updates still block completion of feature 7.

Checkpoint validation passed: full SDK package suite, tracked sample package
suite, workflow/bridge/worker race tests, compiled continuation under the race
detector, live Temporal recording and fresh compiled saved-history replay.
The existing determinism history also passed its computed-result comparison
and corrupted-history negative check. The full Go toolchain/native platform
matrix was not rerun for this SDK-only checkpoint.

The user also requested `Future.ToChannel()` as the native select adapter. Each
call returns a buffered one-result channel carrying `FutureResult{Value, Err}`;
the value implements SDK `converter.EncodedValue`, with typed extraction through
`Value.Get(&result)`. The active future and conversion contracts remain the same.
Channel adaptation neither consumes the future nor changes activity cancellation.
Earlier typed activity helpers remain commented out.

Channel adapter validation passed: full SDK and tracked sample suites, race
unit tests, compiled ownership-checked race driver (including empty-message
activity failures, extraction failures and late callbacks), and fresh replay
of the goroutines sample with its computed result checked against history.
