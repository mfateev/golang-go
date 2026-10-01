# Implementation Plan

Status: **Phase 0 selected the Phase 2B path for the trusted MVP; Phase 2B
implementation remains incomplete**. Results and decision in
[PHASE0_RESULTS.md](./PHASE0_RESULTS.md). Background in
[ISOLATES_DESIGN.md](./ISOLATES_DESIGN.md), [DETERMINISM.md](./DETERMINISM.md),
[ISOLATE_API.md](./ISOLATE_API.md), [ISOLATE_SUBSET.md](./ISOLATE_SUBSET.md),
[ALTERNATIVES.md](./ALTERNATIVES.md).
Where those drafts treat template copying, relative pointers, or pure Go
determinism as settled, the feasibility gates below govern implementation.

## Shape of the plan

The fork-versus-`-toolexec` decision follows feasibility proofs, not a claim
that the same deterministic implementation can be built on both paths. A pure
Go library can validate the host protocol and coordinate operations that use
its own primitives. It cannot make native `go`, channel operations, `select`,
map range, `sync.WaitGroup`, or `time.Sleep` deterministic and observable to an
isolate scheduler. Those guarantees require comprehensive rewriting or runtime
changes. The Phase 1 library therefore has a narrower, explicit contract.

| Stage | Deliverable | Decision it enables |
|---|---|---|
| Phase 0 | Requirements and feasibility proofs for the MVP; record hard-kill limits | Choose a viable execution path and release contract |
| Phase 1 | Pure Go API and host-loop prototype with instrumented primitives | Validate command, event, and quiescence semantics |
| Phase 2A | Full `-toolexec` execution and state rewriting, **conditional** | Ordinary Go for trusted workflows, if its coverage proof passes |
| Phase 2B | Compiler, linker, and runtime execution model, **conditional** | Ordinary Go with runtime-managed isolates |
| Phase 3 | Owned memory and reclamation, fork path | Measure density and establish safe teardown |
| Phase 4 | Suspended-state compaction and optional snapshots, conditional fork path | Density if Phase 3 needs it; snapshot and migration if required |
| Phase 5 | Forceful whole-isolate kill, future work; E5a must pass first | Bounded runaway execution |
| Phase 6 | Containment, fork path, required for the agreed hostile-tenant goal | Untrusted-workflow release |

Phase 1 is a prototype, not a release of the promised ordinary-Go programming
model. The Phase 2A path is also conditional: a rewriter that misses native
blocking operations or standard-library paths cannot claim that model. Under
the eventual hostile-code and forceful-kill goals, Phase 2A can only be an
intermediate trusted-code milestone, not the final release path.

---

## Phase 0 — Requirements and feasibility gates

Inputs confirmed 2026-09-27: replay must match across architectures.
`Resume(ctx)` cancellation returns while leaving the isolate resumable.
The earlier forceful-kill target was Linux arm64 with a 100 ms
request-to-stop bound; forcefully stopping a goroutine that remains in pure
computation is now **future work, outside the MVP**. If implemented, that kill
skips user defers and discards the whole isolate. See PHASE0_RESULTS.md.

MVP termination is whole-isolate revocation at runtime-controlled scheduling
and boundary points. Once revoked, queued or parked goroutines must never
execute isolate code again; they need not be scheduled for their storage to
be reclaimed later. A goroutine already executing without reaching such a
point may continue running. The MVP makes no 100 ms request-to-stop or
forceful runaway-termination claim. Until that gap is closed, the MVP is a
trusted-workflow milestone, not a hostile-tenant release.
The MVP `Kill(ctx)` commits revocation, then waits for the currently running
goroutine to stop. If `ctx` expires, it returns `KillPendingError` with a
best-effort stack sample and sampled thread ID while leaving revocation in
force. A nil return means no isolate goroutine can execute again; memory
reclamation may finish afterward. See ISOLATE_API.md.

Treat hostile tenants and forceful kill as eventual goals already recorded in
ISOLATES_DESIGN.md. The explicit MVP scope change above defers them.
Cross-architecture replay and the usefulness of an instrumented SDK prototype
have been settled for the current work. Before Phase 5, reconfirm the
forceful-kill bound, supported platforms, and behavior of defers and held
locks; those inputs belong to its acceptance test.

The following are scoped proofs, not production implementations; E5a/E5b in
particular may require substantial runtime work. Record test programs, source
changes, measurements, and the outcome of each gate. Do not assign durations
until the prototypes expose the remaining work.

### E0 — `-toolexec` build integration and provenance

The Go command discovers package imports (`src/cmd/go/internal/load/pkg.go`)
before invoking the compiler through `-toolexec`. Prove how a rewrite gets a
new scheduler or state dependency into the build graph: for example, a
pre-build source overlay or generated files visible during package loading.
A compiler wrapper alone cannot assume that adding an import to a source file
updates the graph.

The Go build cache identifies tools through the wrapper's `-V=full` response
(`src/cmd/go/internal/work/buildid.go` and `exec.go`). Make that
identity change with the rewriter version and configuration. Build a
toy program and a standard-library-dependent program from clean and warm
caches; change the rewriter between warm builds and verify that transformed
packages rebuild. Produce a linked-package manifest that proves every package
in the supported subset, including standard-library packages, was transformed
or explicitly classified as safe. Reject a binary with an unclassified package.

**Pass for Phase 2A:** dependency discovery, cache invalidation, and linked
package coverage work without manually editing the user's source or GOROOT.
This gate is unnecessary for Phase 1 and the compiler/runtime fork path.

### E1 — Deterministic execution coverage

Run one workflow using native `go`, channel send/receive, `select`,
`sync.WaitGroup`, `sync.Mutex`, map range, `reflect.Value.MapRange`, `time.Now`,
`time.Sleep`, and a deadlocked goroutine. Establish which operations the pure
Go prototype can observe and which require rewriting or runtime changes.
For the rewriter path, inventory every reachable operation that can start a
goroutine, block, wake a goroutine, read time or randomness, or expose map
order. Include indirect standard-library paths such as `reflect.Select`
(`src/reflect/value.go` to `src/runtime/select.go`), `sync.Cond`,
`context.WithDeadline`, and `time.AfterFunc`; classify each as
instrumented, rejected at build time, or outside an enforceable supported
subset. Prototype representative paths in every supported class, including
standard-library callers. A global `cur()` pointer is safe only if the
prototype proves that exactly one isolate can execute *any* code that reads
rewritten globals at a time, including after wakeups and preemption.

**Pass for Phase 2A:** the rewritten program preserves Go evaluation and
blocking semantics, runs repeatably, and the scheduler can account for every
isolate goroutine. Every uninstrumented reachable operation fails a build or
is excluded by a restriction the build can enforce; a sample workload alone
cannot establish coverage. Otherwise narrow the supported language/API
explicitly or choose the fork. This gate does not hold up Phase 1's narrower
API prototype.

### E2 — Map semantics and cost

Compare runtime-based iteration with a fixed, isolate-scoped hash state and
source-level canonical iteration on representative workloads. A portable hash
alone is insufficient: `internal/runtime/maps` has process-global `hashkey`
and `aeskeysched`, and map iterators draw random offsets. Specify the behavior
for pointer keys, structs, interfaces, NaNs, map mutation during iteration,
and `reflect.Value.MapRange`; sorting keys is not defined for every comparable
Go key type. Test replay across supported architectures and CPU feature sets.

**Exit:** one mechanism with a stated supported-key contract, cross-machine
replay result, and measured cost. If no mechanism covers ordinary Go maps,
revise that promise before implementation.

### E3 — Memory budget and GC cost

Use a representative suspended workflow to estimate goroutine stack capacity,
reachable heap, globals, timers/channels, and isolate bookkeeping. This is a
*proxy* before ownership and snapshotting exist; repeat the measurement on the
real implementation. The working target is median ≤ 8KB and p99 ≤ 32KB of
attributed retained memory per suspended isolate. Also measure the total
incremental process resident memory at 10k instances and report shared process
overhead separately. A 2KB minimum stack per blocked
goroutine (`runtime/stack.go`) makes fan-out especially important to measure.

At 10k idle instances measure GC CPU, mark and assist time, allocation
throughput, retained heap, and stop-the-world latency. Go scans stacks and
globals during concurrent marking, so stop-the-world time alone is not a
global-GC viability test. A failure changes the memory/GC architecture before
Phase 3 starts; proxy memory numbers alone cannot reject or validate it.

### E4 — Global access and initialization

Benchmark both accessor (`cur().pkg_x`) and runtime-base indirection on
workflow-shaped code. Build a toy package whose `init` allocates a mutable map,
stores a pointer and a closure in globals, and registers an entry point.
Create two instances, mutate one, and prove that neither its globals nor its
reachable heap graph affect the other. Go's `initTask` state is process-wide;
duplicating `.data`/`.bss` alone does not establish this invariant. Decide
between per-isolate init and an explicitly relocated, validated template.

**Exit:** an isolation proof for the initialized object graph and measured
indirection overhead. A compiler toy that handles only zero-valued globals
does not pass this gate.

### E5a — Future forceful-kill feasibility, outside MVP

Before claiming forceful kill, prototype a request against a loop, channel
wait, defer, and lock holder, killing every goroutine owned by the isolate.
Measure request-to-stop latency under the specified platforms and workload;
identify safe-point limits and how waiters and runtime records are removed.
The current single-goroutine probe fails this gate on a large built-in
`copy`, and the MVP explicitly excludes forceful termination of such code.
**E5a is a gate for Phase 5 and hostile-tenant release, not for the trusted
MVP or Phase 2B.** Do not describe the MVP as hard-killable.

### E5b — Fork-only feasibility: continuation relocation, conditional

Run this gate if snapshot or migration becomes required, or E3 shows that
suspended-state compaction is needed to meet the density target. Prototype
serialize/restore/resume of a goroutine parked in `Call`, then one parked on a
channel with pointers in its stack, heap, and defers. Base-relative *global
access* does not make Go pointer values relative. A successful restore must
relocate all live references and resume with the same observable result. Also
demonstrate how a frozen isolate remains valid while omitted from global GC
roots; until then, its live objects must remain traced. Failure removes the
snapshot or compaction claim, or forces a density-scope decision; it does not
by itself reject a fork that passes E5a.

**Phase 0 exit for the MVP:** written requirements; E1–E4 results; E0 if
Phase 2A is under consideration; E5b if its trigger is met; and an
execution-path decision. E5a remains required before Phase 5 or a
hostile-tenant release. Estimate implementation durations only
after these proofs identify the work.

---

## Phase 1 — Pure Go API and host-loop prototype

**Deliverable:** a library that exercises `Register` / `New` / `Resume` /
`Call` / `Inbox`, command correlation, typed entry adapters, and the Temporal
host loop. It uses explicit scheduler-aware primitives for goroutines,
blocking, selection, iteration, and time. Its determinism and quiescence claims
apply only to work started and blocked through those primitives. Package globals
remain shared. Native `go`, native channel operations, native `select`, ordinary
map range, `sync.WaitGroup`, `time.Now`, and `time.Sleep` remain outside that
contract; the unmodified `Order` example in ISOLATE_API.md is a Phase 2 test.

This prototype tests API and replay semantics common to both paths. It is also
a reference model for Phase 2. It may be useful as an opt-in, trusted-workflow
SDK, but that is a separate release decision with its narrower contract stated
to users. It is not an isolate implementation for hostile code. In Phase 1,
`Resume(ctx)` can return on context cancellation while an arbitrary CPU loop
continues running; the instance remains resumable, but the loop has not
stopped. Phase 1 can stop isolate execution only at its scheduling points.

The Phase 1 reference model retains its original `Inbox` experiment. The
current source-level isolate API uses only `Call`; an SDK `NextRequest` call
receives each inbound message as a host reply.

### Components and acceptance

1. An instrumented baton scheduler owns every workflow goroutine it creates.
   Define FIFO rules and the observable scheduling points; state that native
   blocking outside the instrumented API is unsupported in this phase.
2. SDK-level clock, timer, map iteration, selection, and seeded random
   primitives match the written replay contract. The chosen map-key contract
   follows E2; do not assume universal sorted-key iteration.
3. `Call` copies bytes across the boundary, parks one owned goroutine, and
   emits a stable numeric correlation ID. `Inbox` receives copied events.

4. `Resume` returns `Quiescent`, `Deadlocked`, or `Completed` for workloads fully
   registered with the scheduler. A native blocked goroutine is a contract
   violation, not evidence that quiescence was detected.
5. Adapt the `Order` example and four Temporal patterns—activity fan-out,
   signals, timers, child workflows—to the explicit primitives. Keep the
   ordinary-Go versions as Phase 2 conformance workloads.
6. Keep three test suites: record/replay command equivalence under varied
   scheduling and GC conditions; boundary and quiescence state-machine tests;
   and an isolation test expected to fail until Phase 2. Repeated runs and
   cross-machine runs are regression tests, not proof of determinism.
7. Model `Kill(ctx)` as irreversible whole-instance revocation. Parked tasks
   must exit without executing another Task continuation. If an active task
   remains in native code when `ctx` expires, return a pending error while
   keeping revocation in force; a later call can wait for the same request.
   Thread and stack sampling belong to the fork runtime, not this library.

**Exit:** the adapted workloads complete through the host loop, every owned
goroutine reaches a defined state, replayed commands match, and actual memory
is reported against E3's proxy estimate. Document each unsupported native
operation in the prototype API.

---

## Decision point

Make the branch decision from Phase 0's proofs and requirements. Phase 1 can
proceed as a shared API prototype while the branch is evaluated. Inputs are:
build integration and native-operation coverage; initialized-state isolation;
map and cross-machine replay results; measured memory and GC cost; conditional
relocation feasibility; and the recorded MVP trust model. E5a informs the
later forceful-kill milestone.

- Choose Phase 2A only if E0 and E1 support the desired enforceable subset. It
  is an optional trusted-code milestone, not a final release path. The shared
  heap may meet the density target, but E3's proxy does not establish that.
- Choose Phase 2B for the trusted MVP if its other execution, state-isolation,
  and determinism gates pass. Require E5b as well if snapshot, migration, or
  suspended-state compaction is necessary. E5a is deferred to Phase 5.
  The hostile-tenant goal requires both forceful termination and Phase 6
  before release under that claim.
- If neither path meets mandatory requirements, revisit the requirements or
  execution model before starting a long implementation phase.

---

## Phase 2A — `-toolexec` execution and state rewriter (conditional)

Proceed only if E0 and E1 prove a useful, enforceable supported subset. This phase
must rewrite more than globals: `go`, channel send/receive and close behavior,
`select` and `reflect.Select`, calls that can park on `sync` primitives, map
iteration including reflection where exposed, and clock/timer APIs including
`context`'s timer paths. Preserve argument evaluation order, panics, `defer`,
`break`/`continue`, and map mutation semantics. Reject unsupported constructs
at build time; an unrewritten operation must not silently enter a workflow.

Use E0's dependency-discovery mechanism before package loading and versioned
tool identity for cache invalidation. Verify the final linked-package manifest
on both clean and warm builds; transformed source files alone do not prove
that every archive in the binary was instrumented.

Partition package variables across user code and the allowed standard-library
subset. Handle initialized globals and their reachable heap graphs using the
E4 mechanism. Audit assembly-backed and runtime-backed standard-library calls
that the source rewriter cannot instrument. `-toolexec` is an interposition
mechanism, not evidence that each of these transformations is sound.

The process-global `cur()` design requires an execution invariant, not just a
baton convention: no goroutine may read rewritten state after a different
isolate becomes current. Prove this under wakeups, preemption, panics, and
host calls. If it cannot be proved, change the context mechanism or narrow the
supported API. Multiple active isolates in one process are outside this path's
contract; processes supply parallelism.

**Exit:** the unmodified ordinary-Go conformance workloads pass; two isolates
have independent initialized state; all supported blocking operations are
accounted for in quiescence; cross-machine replay matches the stated contract;
and unsupported operations fail the build. Phase 2A remains a trusted-code
path without hard kill, owned-heap reclamation, or hostile-code containment.

## Phase 2B — Compiler, linker, and runtime execution model (conditional)

The late x86 `rewriteToUseGot` pass (`cmd/internal/obj/x86/obj6.go`) is a useful
precedent for one form of indirection. It does not cover initialization,
pointer relocation, all architectures, or scheduler state. Scope those
separately, then implement in this order:

1. **Globals and initialization.** Identify isolate-reachable packages, lay
   out their globals, redirect accesses, and create independent post-init
   object graphs. Keep process-global runtime infrastructure explicit. Pass
   E4's map/closure isolation test and a standard-library package case.
2. **Scheduler and quiescence.** Define deterministic run-queue and wakeup
   order, then audit every path that makes a goroutine runnable, including
   timers, channels, `sync`, global queues, and work stealing. Track the
   isolate on each `g`. Classify completion, host waits, timers, and native
   deadlock without mistaking an untracked runnable goroutine for quiescence.
3. **Randomness, maps, and time.** Isolate the PRNG *and* map hash state,
   including process-global `hashkey`/`aeskeysched` or an alternative selected
   in E2. Cover reflected iteration and the agreed architecture set. Separate
   real runtime time from isolate-visible injected wall and monotonic time;
   route timers through the host policy.
4. **Boundary and failure behavior.** Implement `Call`, copied
   payloads, stable correlation IDs, and per-isolate failure reporting. Keep
   runtime errors that still abort the process on an explicit audit list for
   Phase 6; they cannot be described as isolate-fatal yet.

Add a durable whole-isolate termination state. Every path that dispatches a
queued or parked isolate goroutine must check it before user code resumes;
reclamation can follow after execution stops. This does not stop a goroutine
already running without reaching a runtime-controlled point. A source-level
scheduling proof must cover paths outside `newproc`, `ready`, and `schedule`;
routing those three alone is insufficient. `Resume(ctx)` cancellation returns
with the isolate resumable, as specified above. The MVP must not expose
`LimitExceeded` as if it could forcefully stop a CPU-bound loop.
The `Kill(ctx)` wait must not depend on scheduling revoked goroutines, and
its diagnostic stack collection must not itself wait for a Go safe point.
Signal-based sampling, as used by Go's CPU profiler, is a candidate; allow
an empty or truncated stack if sampling misses its deadline. A tagged Phase 0
arm64 probe captured the running goroutine's Go stack during a large
`memmove`; production integration and deadline behavior remain open.

**Exit:** the same ordinary-Go conformance workloads as Phase 2A, plus
initialized-state isolation and native quiescence tests, pass under varied
`GOMAXPROCS`, forced GC, preemption, and supported architectures. Record all
remaining process-fatal paths and cross-isolate reference risks before claiming
memory isolation or hostile-code containment.

---

## Phase 3 — Owned memory and safe reclamation (fork path)

Build the ownership verifier before bulk reclamation. It must inspect other
isolates, host roots and heap objects, and runtime off-heap structures that
can hold user pointers. A teardown-only scan of other isolates misses host
references, timer records, finalizer queues, and runtime caches. The verifier
must catch deliberately injected cross-owner references as well as pass normal
workloads; include references through globals, stacks, channels, and closures.

Implement owned allocation and an initial small-object nursery if E3 confirms
the size-class floor is material. A nursery needs GC pointer metadata, write
barrier behavior, and promotion rules; bump allocation alone is not sufficient.
Coordinate bulk free with GC phase, stack scanning, and allocator caches.
Choose global or per-isolate GC from E3's measured CPU, latency, and retention
results. Repeat E3 on the real implementation at 10k idle isolates and a
mixed active workload. The 8KB median / 32KB p99 targets are acceptance
criteria for the suspended workload, not proven consequences of the design.

**Exit:** the verifier catches injected escapes; teardown leaves no dangling
references; GC and allocator invariants hold under stress and the race
detector; and incremental resident memory and GC costs are measured against
the recorded budgets. If suspended stacks or roots keep density above target,
run E5b and Phase 4 before claiming that target, or make an explicit scope
decision.

## Phase 4 — Suspended-state compaction and optional snapshots (conditional fork path)

Run this phase when E3/Phase 3 measurements require compaction for density, or
when snapshot or migration is included in the release contract. Build from
E5b's restore proof. A suspended goroutine can contain absolute stack and
heap pointers, channel wait records, defers, and references into runtime
metadata. Serialize or relocate the entire live graph and prove that
restore resumes the same observable execution. Global-base indirection by
itself does not provide this. A frozen isolate can leave the global GC root
set only after its representation is independently owned and protected from
collection; otherwise it remains a root.

Measure the actual stack contribution before treating serialization as
mandatory for the 8KB median target. It is required for sub-2KB storage per
blocked goroutine and may be needed for fan-out-heavy p99 cases. First support
restore with the same build. If durable snapshots or migration are required,
define snapshot versioning and an explicit fallback to replay when code or
data layouts change across builds. Worker migration is an exit claim only for
supported build/version combinations.

**Exit if undertaken:** compacted state restores for the supported `Call`,
channel, timer, and deferred continuations; address relocation and ownership
checks pass; and E3's density and GC measurements are repeated. Claim durable
snapshot or migration only for the build/version combinations actually tested.

## Phase 5 — Forceful whole-isolate kill (future fork path)

Implement a termination mechanism that passes E5a. A request must reach a
well-defined safe point, stop every goroutine in the isolate, remove its
waiters and scheduler records, and hand memory to Phase 3's safe teardown.
Test loops, channel waits, locks, defers, concurrent GC, and shutdown races.
Run stress tests with `GODEBUG=clobberfree=1` and the race detector, but do not
treat a finite number of clean iterations as a proof of safe termination.

**Exit:** a runaway isolate stops within the future agreed bound without corrupting
the host or another isolate, and its memory returns to the measured baseline.
If this cannot be achieved, revisit the execution model. The trusted MVP may
ship without this phase, but it must not be released as hard-killable or as
safe for hostile tenants.

## Phase 6 — Containment (fork path, required for the agreed hostile-tenant goal)

Implement and audit the Tier 2 static restrictions in ISOLATE_SUBSET.md, the
Tier 1 capability boundary, and per-isolate resource limits. Maintain an
explicit manifest of allowed Tier 1 entry points and block a Go-version update
until additions are reviewed; a denylist and traps alone fail open when a new
effectful entry point appears. Include compiler,
assembly, `linkname`, `unsafe`, cgo, syscalls, and ambient process state in the
coverage test. Audit process-fatal runtime paths—such as `throw`, fatal map
errors, stack overflow, and OOM—so a tenant cannot terminate the worker.
Forbidden operations must terminate only the offending isolate and must not
be recoverable with `recover`.

**Exit:** adversarial build and runtime tests demonstrate memory containment,
capability enforcement, resource limits, and worker survival. Earlier phases
are trusted-code milestones even if the final trust model is hostile. If that
requirement is dropped, omit Phase 6 and state the trusted-code boundary in
the release contract.

---

## Scheduling and release gates

The earlier 7–49-week totals are withdrawn. They assumed Phase 1 could provide
ordinary-Go determinism without code transformation and priced globals without
initialized-state cloning. After Phase 0, estimate each selected phase from
its passing prototype and the measured work remaining. One engineer is assumed;
Phase 5 implementation can proceed independently of optional Phase 4 after
E5a passes and Phase 3's teardown interface is stable. Phase 6's audit can
begin earlier, but its untrusted-workflow release gate follows both
containment and forceful-kill work.

| Milestone | Claim allowed at that milestone |
|---|---|
| Phase 1 | API/host-loop prototype with explicit deterministic primitives; shared globals and trusted code |
| Phase 2A | Ordinary Go only for the proven rewritten subset; trusted code; no hard kill or owned memory |
| Phase 2B | Runtime-managed ordinary Go and independent initialized state for the proven subset; no memory-reclamation or containment claim yet |
| Phase 3 | Owned-memory reclamation and measured costs; density only if the target is met |
| Phase 4, if needed | Suspended-state compaction; snapshot/restore only for documented build and continuation cases |
| Phase 5, future | Forceful whole-isolate kill under a tested safe-point contract |
| Phase 6, future | Hostile-code containment, only after forceful kill and adversarial acceptance tests pass |

## Decisions still required

1. Cross-architecture replay and `Resume(ctx)` cancellation are fixed above.
   The 100 ms forceful-kill target is deferred beyond the trusted MVP; its
   final bound, defer behavior, and supported platforms must be confirmed
   again before Phase 5 acceptance. Hostile-code release remains a separate
   future scope decision.
   MVP `Kill(ctx)` return behavior is specified above. A targeted stack
   diagnostic has a Phase 0 probe; production integration remains open.
2. Resolve activity options and cancellation in the context-free API before
   calling the Phase 1 `Order` adaptation representative.
3. Define the supported ordinary-Go subset from E1/E2 results, including
   blocking operations, map keys, reflection, and standard-library behavior.
4. Decide whether snapshot/migration is required or density measurements
   trigger compaction. If snapshot is implemented, define compatibility across
   builds; until then, use replay for incompatible versions.
