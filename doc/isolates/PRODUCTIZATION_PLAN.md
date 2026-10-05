# Productization plan for Go isolates and the Temporal SDK

Status: proposed release plan; POC baseline updated 2026-10-05. This follows the trusted Temporal POC in
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
provisional revocation. Opt-in deterministic mode now provides FIFO native
goroutines, reproducible select, canonical integer/string map iteration, and
exact suspend/resume fences. The Temporal adapter enables it and handles all
concurrent commands before ending a workflow task. Its concurrent activity and
timer example and the SleepForDays signal path completed on a local server and
replayed in fresh Linux arm64 processes at different GOMAXPROCS settings.
Dispatcher stress and race tests passed. See
[NATIVE_DETERMINISM_PLAN.md](./NATIVE_DETERMINISM_PLAN.md) for the contract and
evidence. Native Linux/macOS arm64/amd64 conformance and saved-history replay
passed on 2026-10-05. The runtime still uses
the ordinary shared Go heap; child failures, unreviewed runtime waits,
process-owned state, and effects remain uncontained.

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
[Native isolate determinism](./NATIVE_DETERMINISM_PLAN.md). Feature 2 can now
begin. The validated determinism contract still excludes the unsupported
operations listed there.

See [Native isolate determinism](./NATIVE_DETERMINISM_PLAN.md) for the supported
operations and reproducibility contract. Unsupported map key kinds and
machine-dependent effects require explicit restrictions rather than an implied
promise that arbitrary Go code can replay.

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
  values. Audit the provisional process-owned converter and host activity SDK graphs,
  including protobuf's lazy descriptor caches, before supporting protobuf
  message values. TODO: support the worker's configured custom converter, its
  payload codecs and serialization context, and validate boundary types and
  conversion errors.
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

The current serial adapter remains a **technical preview**. A **trusted beta**
requires milestones 0–4 for a deliberately bounded workload. A **trusted
production release** also requires milestones 5–6 and an operations policy
for pending kills. A release for hostile tenants is a separate gate: a sound
Tier 1 standard-library audit, complete cross-owner containment, resource
limits, and a proven bound for forceful whole-isolate termination. Snapshot
and restore, dynamic loading, and same-import-path dependency versions do not
block the trusted release.

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
