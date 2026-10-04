# Productization plan for Go isolates and the Temporal SDK

Status: proposed plan, 2026-10-04. This follows the trusted Temporal POC in
[TEMPORAL_POC.md](./TEMPORAL_POC.md) and the runtime contract in
[ISOLATE_API.md](./ISOLATE_API.md).

## Release target and current baseline

The first supported release is for **reviewed Temporal workflow code**. A
worker statically links workflow `package main` programs, runs many executions
in one Go runtime, and sends external effects through the host and Temporal
Go SDK. Replay must work across supported CPU architectures. The release must
say which Go and standard-library APIs are supported and reject unsupported
effects before they reach process state. It must not claim safety for hostile
tenants or bounded termination of uninterrupted CPU loops.

Today `golang-go` supplies static `-isolate-dir` builds, selected per-instance
package globals, a copied-byte `Call` boundary, and provisional revocation.
`sdk-go-poc` adapts serial workflow programs to a pinned Temporal Go SDK via
`internalbindings`; `samples-go-poc` has activity, timer, signal, and replay
examples. The serial examples have run against a local server and replayed in
fresh processes. The runtime still uses the ordinary Go heap and scheduler.
Its live-goroutine count is not an exact quiescence barrier. Child failures,
some runtime waits, process-owned state, and effects remain uncontained.

The first release keeps static linking and one selected dependency version per
module/import path. Independently loaded `.so` programs, two versions behind
the same import path, snapshots, migration, hostile-code containment, and
forceful termination of non-yielding code remain separate future work.

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
- Decide the initial native platform set from CI evidence. The macOS README
  currently describes setup, but native macOS execution has not been verified.

### 1. Exact suspension and deterministic execution

- Replace the SDK's one-command-at-a-time timeout loop with `Resume` (or an
  equivalent runtime-owned hook) that returns only when no isolate goroutine
  can make progress without a host event. Distinguish completed, awaiting
  `Call`, timer-only, and deadlocked states. Remove millisecond polling from
  the host lifecycle.
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
- Add typed workflow helpers above the byte protocol, while retaining normal
  `package main` entry points. The POC supports only Temporal's default data
  converter: move protobuf-serialized `Payloads` across the byte boundary and
  decode/encode typed values inside the isolate with that converter. TODO:
  support the worker's configured custom converter, its payload codecs and
  serialization context, and validate boundary types and conversion errors.
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
