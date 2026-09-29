# Task Status: Modify Go runtime to support isolates

## Task Overview

Add **isolates** to the Go runtime — a very large number of small, fully
isolated Go programs running concurrently in a single process, modelled on V8
isolates. Driving use case is Temporal workflow isolation.

**Documents:**
- [ISOLATES_DESIGN.md](./ISOLATES_DESIGN.md) — agreed definition and decisions
- [ISOLATE_SUBSET.md](./ISOLATE_SUBSET.md) — language/stdlib restriction and enforcement
- [DETERMINISM.md](./DETERMINISM.md) — worked analysis of time, map iteration, select
- [ISOLATE_API.md](./ISOLATE_API.md) — host API, isolate-side primitives, what workflow code looks like
- [IMPLEMENTATION_PLAN.md](./IMPLEMENTATION_PLAN.md) — **the plan**: phases, gating experiments, decision point
- [PACKAGE_STATE.md](./PACKAGE_STATE.md) — initial process/isolate package-state partition and dependency probe
- [DYNAMIC_LOADING.md](./DYNAMIC_LOADING.md) — feasibility of independently built programs loaded into one runtime
- [ALTERNATIVES.md](./ALTERNATIVES.md) — rationale for the no-fork path

- **Repo:** [golang/go](https://github.com/golang/go) via fork
  [mfateev/golang-go](https://github.com/mfateev/golang-go)
- **Branch:** `task/modify-go-runtime-for-isolates` (cut off `origin/master`)
- **Base commit:** `2ff5743d9f` — *cmd: update vendored x/arch*
- **PR:** none yet

## Current Status

🟡 **Phase 0 selects the Phase 2B compiler/runtime path for the trusted MVP.
Phase 2B implementation has begun with an opt-in initialized-global probe;
ordinary-Go execution remains unproved.**

- [x] Fork created (`mfateev/golang-go`) and synced with `golang/go`
- [x] Worktree on `task/modify-go-runtime-for-isolates`
- [x] `upstream` remote added with push disabled
- [x] Scope definition — revised MVP and future termination scope, see ISOLATES_DESIGN.md
- [x] Subset definition — drafted, see ISOLATE_SUBSET.md (needs review)
- [x] Determinism analysis for time / maps / select — see DETERMINISM.md
- [x] Cross-architecture replay required (confirmed 2026-09-27)
- [x] Phase 1 fixed-byte replay fixture passed on arm64 and emulated amd64
- [x] Earlier forceful-kill experiment targeted Linux arm64 and ≤100 ms;
      forceful cleanup is now future work outside the trusted MVP
- [x] Hard kill skips defers; canceled `Resume(ctx)` remains resumable
- [x] MVP `Kill(ctx)` revokes the whole isolate and waits until stopped;
      deadline returns a pending error with a best-effort running stack
- [x] Phase 0 targeted signal-stack sample captured busy Go code and
      `runtime.memmove` on native arm64; see PHASE0_RESULTS.md
- [ ] Integrate bounded targeted sampling into runtime `KillPendingError`
- [x] Measure scalar vs AES hash microbenchmarks; selected E2 prototype uses
      canonical iteration instead of relying on a hash seed
- [x] E2 restricted deterministic map-range mechanism: sorted string/integer
      key snapshot plus live lookup; mutation semantics and arm64/amd64 test
      pass; about 2.8–2.9x native cost in the current 1k-key run
- [ ] Tier 1 entry-point guard enumeration
- [x] Isolate API + boundary ABI — drafted, see ISOLATE_API.md
- [ ] Goroutine scheduling determinism (the largest remaining piece)
- [ ] **Build the runtime quiescence hook** — deadlock detection, clock advance,
      and the host suspend point are all the same mechanism
- [ ] Floor measurement + nursery prototype
- [x] Initial E3 proxy measurement at 10k parked instances; see PHASE0_RESULTS.md
- [x] E3 proxy GC scan/mark/assist/STW and allocation-throughput profiles
- [x] E3 90/10 Inbox/fan-out mixed proxy at 10k instances: 7.54–7.56 KB
      incremental RSS per instance in three fresh processes; per-instance
      median and p99 require real ownership accounting
- [x] Initial E5a safe-point and narrow kill probes on arm64; general hard kill still open
- [x] E5a 4 GiB `copy` counterexample: kill request exceeded 100 ms and left
      the target alive on native arm64; see PHASE0_RESULTS.md
- [ ] Future E5a acceptance: group teardown of multi-waiter `sync.Cond`,
      select/timer waits, all owned goroutines, safe heap teardown, and a
      confirmed worst-case bound
- [x] Phase 1 trusted-code host-loop prototype in `src/internal/isolateproto`
- [x] Phase 1 `Kill(ctx)` prototype: irreversible revocation; parked tasks
      exit; an active task that misses its deadline returns `KillPendingError`
      and stops at its next Task boundary. Its stack and OS thread ID are not
      sampled in the pure-Go prototype.
- [ ] Integrate revocation and targeted pending diagnostics into the fork runtime
- [x] Phase 2 initialized-global conformance test recorded (expected failure)
- [x] E4 tagged toy reruns compiler-generated `init.0` with a per-goroutine
      state base; two initialized map/pointer/closure graphs stay separate
      under concurrent execution, including a native child goroutine
- [x] E4 opt-in compiler toy rewrites two initialized globals' addresses through
      the per-goroutine base; 100 race runs passed with interleaved/concurrent
      instances and a native child goroutine; 100 emulated amd64 runs passed
- [x] E4 registered-entry toy reruns isolate initialization without duplicating
      host registration; two host-loop invocations read independent state
- [x] Phase 2B first-dispatch revocation hook discards an unstarted group
      child before user code; 1,000 native race runs and 100 emulated amd64
      runs passed; see PHASE2B_PROGRESS.md for its narrow scope
- [x] Phase 2B admission and revocation now have one atomic order across Ps;
      a tagged native race test covers 1,000 competing transitions per run
- [x] Opt-in compiler mode keeps static package assignments executable so a
      fresh E4 toy base can replay variable initialization before user `init`
- [x] Opt-in compiler-generated package layout and runtime GC type separate
      two toy initialized states; an MVP compiler gate now prevents exporting
      inlineable functions from the opt-in package, verified by 100 native
      arm64 race runs
- [x] Initial package-state partition recorded; a tagged two-package probe
      selects independent layouts across one dependency edge; an explicit
      manifest helper orders and replays compiler-generated initializer records.
      Selected direct imports now come from compiler metadata; 100 native
      arm64 race runs passed
- [x] One opt-in build-wide package list enables local layouts, initializer
      replay, and imported-global routing in every compiler invocation; the
      two-package tagged suite passed 100 native arm64 race runs
      and the `encoding/base64` suite passed 100 native arm64 race runs; the
      complete `src/all.bash` suite passed
- [x] Selected direct-import metadata includes a dependency identity-key
      relocation; the host validates it against the explicit manifest before
      initializer replay, with 100 native arm64 tagged race runs and a full
      `src/all.bash` pass
- [x] Imported-global lookup rejects a legacy single-package base without a
      package table; 100 native arm64 tagged race runs passed for the
      rejection path and 100 for the ordinary package-table path; the
      complete `src/all.bash` suite passed
- [x] Compiler-owned package descriptors carry the path, identity key,
      layout type slot, and initialization records together; two-package and
      `encoding/base64` tagged suites passed 100 native arm64 race runs, and
      the complete `src/all.bash` suite passed
- [x] Tagged `encoding/base64` probe reruns four initialized encoding
      pointers per instance; a directly importing caller selects the instance
      globals with a build-wide compiler flag; two importing packages and
      their test package passed 100 native arm64 race runs
- [x] Phase 0 path decision: choose Phase 2B for the trusted MVP; E0/Phase 2A
      are out of scope, E5a remains future, E5b waits for real density results
- [x] E4 first implementation direction: rerun restricted initializers per
      isolate; template copying requires a later heap-graph relocation proof
- [x] E4 workflow-shaped toy benchmark: direct global 13.38–14.75 ns/op,
      compiler-rewritten global 18.68–18.87 ns/op on Linux arm64
- [ ] E4 general global layout and rewriting, dependency init order,
      standard-library initialized-state proof, and goroutine-creation audit
- [ ] Phase 2B implementation beyond scoped compiler/runtime probes
- [x] Full Linux arm64 `src/all.bash` after the Phase 0 and E4 probe changes

## Decisions At A Glance

The intended full isolates are: same-binary, single-threaded, per-isolate globals via
base-relative indirection, isolate-owned spans off the shared `mheap_`, global
GC to start, runtime-guaranteed determinism, eventually hard-killable, relocatable-by-
design, targeting 10k+ long-lived mostly-idle instances, contained by a
build-time language subset.

**Load-bearing principle:** everything an isolate owns is addressed relative to
its isolate base. This single mechanism serves per-isolate globals, determinism,
snapshotting, and suspend-time compaction — and degrades all four at once if
violated. See ISOLATES_DESIGN.md § "The unifying mechanism".

## Deferred (Future Improvements)

- **Typed boundary interfaces** — value-type-only method signatures making
  copy-only compiler-verified rather than conventional, with memcpy instead of
  serialization for in-process calls. Sound, but adds permanent compiler
  surface. `Call([]byte)` is forward-compatible: the migration turns `Call`
  into one method on the standard boundary interface. See ISOLATE_API.md.
- **Snapshot/restore** — layout stays relocatable, implementation deferred
  (decision 9).

## Open Questions

1. **Can every effectful Tier 1 entry point be enumerated and guarded?** The
   containment claim rests on this. Guards should be generated from a list, not
   hand-written, so a Go version bump surfaces new unguarded entry points.
2. **Does `encoding/json` survive the curated `reflect`?** If not, serialization
   needs another answer and the reflect decision changes.
3. **The runtime boundary ABI** — the byte-copy model is validated by the
   Phase 1 prototype, but the runtime implementation remains open.
4. **Private fork vs. upstream ambition** — currently assumed private fork.

## Runtime Areas Affected

Starting map, not yet verified against the code:

- `src/cmd/compile` — global access via isolate base; the big change
- `src/cmd/link` — per-isolate `.data`/`.bss` layout; isolate-reachability partition
- `src/runtime/mheap.go`, `mgcsweep.go` — span ownership, bulk return
- `src/runtime/proc.go` — per-isolate deterministic scheduler, isolate↔P binding
- `src/runtime/mgc.go`, `mgcmark.go` — root set scoping, idle-isolate exclusion
- `src/runtime/preempt.go`, `signal_*.go` — hard kill at preemption-safe points
- `src/runtime/mfinal.go`, `time.go`, `sync/pool.go` — cross-isolate pointer audit

## Build & Test Notes

For a Git-only handoff, including the previous container filesystem failure
and recovery steps, see [DEVELOPMENT.md](./DEVELOPMENT.md). After container
recreation, the full Linux arm64 `src/all.bash` suite passed on 2026-09-27,
including the first-dispatch revocation change. See
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md) for the initial missing-`netbase`
environment failure and successful rerun.
The subsequent atomic-admission change has not passed `src/all.bash`: its
first run failed in cgo with a host-cache `ENFILE` error after the main
package and alternate-mode tests passed. A later rerun with the generated
layout change failed at a bootstrap staleness check before tests began; the
newer compiler probes have not had a full-suite pass. See
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md). The native `runtime` suite and
focused tagged race tests passed. A subsequent diagnostic `make.bash` failed
with `ENFILE` while reading the repo and removed the tool binaries before
rebuilding them; focused tests were blocked until the environment recovered.

After container recreation, `src/make.bash` passed on 2026-09-28. The
opt-in cross-package inlining test passed 100 native arm64 race runs. A new
tagged compiler test confirms that exported generic functions and methods
are rejected in the layout mode, closing a caller-side global-access escape.
The complete `src/all.bash` rerun passed, including race and `../test`, after
restoring the container's missing `/etc/services` through `netbase`.
The subsequent two-package dependency probe also passed a complete native
`src/all.bash` run, alongside 100 tagged race-detector runs of its own tests.
The opt-in `encoding/base64` initialized-state probe passed 100 tagged native
race runs and another complete native `src/all.bash` run.

The active development target is the container's native **Linux arm64**
(`uname -m` reports `aarch64`; the rebuilt tree's `go version` reports
`linux/arm64`). E5a measurements and hard-kill acceptance tests run on this
target. The amd64 emulator is used only for Phase 1 replay-byte comparison;
the Linux/386 emulator cannot launch even a minimal bootstrap-Go binary in
this container.

From the repository root:

```bash
cd src
./make.bash      # rebuild toolchain after runtime changes
./all.bash       # full suite before a push
```

`all.bash` on the Go tree runs for tens of minutes. Budget for it.

## Next Steps

The Phase 0 path decision and Phase 1 reference-model results are recorded in
[PHASE0_RESULTS.md](./PHASE0_RESULTS.md). Continue Phase 2B from the scoped E4
compiler probe: generalize global layout, initialized dependency state, and
the native scheduler only as each invariant is tested.

The Phase 1 scheduler has a `Kill(ctx)` reference path. It does not stop
native computation without a `Task` operation and cannot sample an individual
OS thread. Phase 2B needs the runtime-owned revocation and dispatch gate
after global state and native scheduling are integrated.

The Phase 0 decision selects the fork path from its execution coverage, map,
memory, and initialized-state results. Phase 1 is a
shared API and host-loop **prototype**; it guarantees determinism only for its
explicit primitives and does not provide package-global isolation or hostile
code containment. It does not release the promised ordinary-Go model.

The eventual hostile-code and forceful-kill goals also require the
compiler/runtime fork. The trusted MVP proceeds through Phase 2B without
E5a; it cannot claim forceful shutdown
of an uninterrupted CPU loop or hostile-tenant safety. The current forceful
probe fails the earlier 100 ms target on an allowed large `copy`. Phase 2A
was not selected; revisiting it would first require E0 and a complete E1
rewriter-coverage proof.
The revised plan deliberately assigns no implementation durations before the
gates expose the work.

## Setup Gotcha (for future sessions)

The fork is **`mfateev/golang-go`**, not `mfateev/go` — the name `go` was already
taken by a fork of `encoredev/go`, an unrelated fork network. The clone directory
and worktree are therefore named `golang-go`.
