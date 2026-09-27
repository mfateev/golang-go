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
- [ALTERNATIVES.md](./ALTERNATIVES.md) — rationale for the no-fork path

- **Repo:** [golang/go](https://github.com/golang/go) via fork
  [mfateev/golang-go](https://github.com/mfateev/golang-go)
- **Branch:** `task/modify-go-runtime-for-isolates` (cut off `origin/master`)
- **Base commit:** `2ff5743d9f` — *cmd: update vendored x/arch*
- **PR:** none yet

## Current Status

🟡 **Phase 1 reference model exists; trusted MVP scope excludes forceful
cleanup of uninterrupted computation. Phase 2B still depends on its other
feasibility gates.**

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
- [ ] Measure portable-hash vs aeshash cost
- [ ] Tier 1 entry-point guard enumeration
- [x] Isolate API + boundary ABI — drafted, see ISOLATE_API.md
- [ ] Goroutine scheduling determinism (the largest remaining piece)
- [ ] **Build the runtime quiescence hook** — deadlock detection, clock advance,
      and the host suspend point are all the same mechanism
- [ ] Floor measurement + nursery prototype
- [x] Initial E3 proxy measurement at 10k parked instances; see PHASE0_RESULTS.md
- [x] E3 proxy GC scan/mark/assist/STW and allocation-throughput profiles
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
- [ ] Implementation
- [ ] Tests (`all.bash`)

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

The Phase 1 reference model and partial Phase 0 observations are recorded in
[PHASE0_RESULTS.md](./PHASE0_RESULTS.md). Continue the remaining feasibility
gates in [IMPLEMENTATION_PLAN.md](./IMPLEMENTATION_PLAN.md) before choosing a
runtime path.

The Phase 1 scheduler now has a `Kill(ctx)` reference path. It does not stop
native computation without a `Task` operation and cannot sample an individual
OS thread. The next implementation step is the runtime-owned revocation and
dispatch gate, after the execution and state-isolation gates that choose the
fork path are resolved.

The fork-versus-`-toolexec` decision follows Phase 0's execution coverage,
map, memory, initialized-state, and hard-kill feasibility proofs. Phase 1 is a
shared API and host-loop **prototype**; it guarantees determinism only for its
explicit primitives and does not provide package-global isolation or hostile
code containment. It does not release the promised ordinary-Go model.

The eventual hostile-code and forceful-kill goals make the compiler/runtime
fork path the likely final route. The trusted MVP may proceed through Phase
2B without E5a after its other gates pass; it cannot claim forceful shutdown
of an uninterrupted CPU loop or hostile-tenant safety. The current forceful
probe fails the earlier 100 ms target on an allowed large `copy`. Phase 2A
remains an optional trusted-code milestone if E0/E1 pass.
The revised plan deliberately assigns no implementation durations before the
gates expose the work.

## Setup Gotcha (for future sessions)

The fork is **`mfateev/golang-go`**, not `mfateev/go` — the name `go` was already
taken by a fork of `encoredev/go`, an unrelated fork network. The clone directory
and worktree are therefore named `golang-go`.
