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
- [STATIC_PROGRAMS.md](./STATIC_PROGRAMS.md) — per-directory config and static multi-program build direction
- [RUNTIME_REVOCATION.md](./RUNTIME_REVOCATION.md) — scheduler waiter ownership and `Kill` acceptance cases
- [DYNAMIC_LOADING.md](./DYNAMIC_LOADING.md) — deferred feasibility of independently built programs loaded into one runtime
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
- [x] A reproducible 10,000-instance prepared-state floor measured about
      9.40 KB retained per JSON-importing instance versus 0.54 KB for an
      empty program; see experiments/layout_floor/README.md. Running-stack,
      heap ownership, and nursery measurements remain open
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
- [x] The trusted host revokes admission for unstarted children when main
      exits or preparation fails. A 100-run race test checks a parked child
      cannot start a new grandchild after revocation; started children and
      waiter cleanup remain outside this fence
- [x] The provisional `Call` bridge stops command-send and reply-receive
      waiters when main exits or preparation fails. Revoked callers now discard
      without running Go defers after their channel records are cleaned;
      two 100-run race tests and the static program script pass. Other runtime
      waiters and complete native `Kill(ctx)` revocation remain open
- [x] A provisional source-level `Kill(ctx)` fences startup, stops the Call
      bridge, and waits for zero live group members. It returns pending counts
      for unsupported runtime waits; those goroutines can still resume, so
      whole-isolate revocation and safe teardown remain open
- [x] `Kill(ctx)` now publishes the admission fence and wakes Call before
      starting the runtime waiter scan on a process goroutine. The caller can
      observe its deadline even if that scan waits for a runtime queue lock;
      the scan itself and running code have no forced completion bound
- [x] Real `time.Sleep` waits register with the runtime group. Kill stops
      pending sleep timers and wakes their Gs early; a callback already in
      progress wakes its own G. Each G unregisters before exiting without
      returning to user code. Fake synctest timers retain ordinary wakeup;
      timer-channel receives use channel or select cleanup
- [x] Nil channel send/receive and empty or all-nil `select` register a
      permanent park with the runtime group. Kill wakes parked Gs or cancels
      parks in progress; they unregister and exit before user code resumes
- [x] Ordinary channel send and receive register with the group before
      taking the channel lock. Kill detaches queued senders and receivers,
      including direct timer-channel receives, and wakes their Gs. Peer wakes
      that already removed a waiter finish normally; the G cleans its own
      wait state and exits before user code. Multi-case select uses the
      separate wake token path below
- [x] Multi-case `select` registers with the group before locking channels.
      Kill claims its wake token against peer operations, cancels an
      uncommitted park or wakes a parked G, then the G removes all channel
      records and timer wait counts before exiting
- [x] Network poll preparation checks revocation before ready I/O, and waits
      check again after the poll semaphore no longer holds the waiting G.
      The group registers normal poll waits and revocation now wakes them
      without host I/O; each resumed G finishes poll cleanup before exiting.
      Direct waiter discard and canceled-I/O waits remain open
- [x] Contended `sync.WaitGroup.Wait`, `Mutex`, and `RWMutex` waits register
      their semaphore queues with the runtime group. Kill unlinks their
      queued waiters under the process semaphore root lock, then the resumed
      G releases its runtime record and discards without Go defers. The
      synctest WaitGroup path and other process semaphore users remain open
- [x] Exported `runtime.Gosched` checks revocation before yielding and after
      resuming, so a goroutine that repeatedly yields can exit after Kill.
      Internal runtime yields remain separate; arbitrary preemption and
      uninterrupted CPU loops still need a runtime execution fence
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
- [x] Static-link feasibility probe compiled two separate `package main`
      units under distinct internal package paths and called both from one
      host executable; same-path dependency version mismatch failed link,
      while distinct versioned import paths coexisted
- [x] Initial build-side `isolate.json` reader accepts explicitly selected
      directories, rejects unknown fields and duplicate names, and returns a
      deterministic program list; focused package tests pass
- [x] Source-level `isolate` package implements `Call` for a
      normal per-program `func main()` through a trusted per-goroutine
      boundary probe. It copies request and response bytes; correlates
      concurrent calls; and is inherited by native child goroutines. The
      native owned command queue and deterministic scheduler remain pending
- [x] Incoming work now uses an SDK `Call` operation whose host reply carries
      one request. Removed the source-level `Inbox`, `Config.Input`, and host
      `Send` path; the static two-program build and focused race tests pass
- [x] The static package-state probe now selects `time` globals. The build
      script checks `time.Local` starts fresh in two instances and leaves the
      host value intact; runtime timers and clocks remain outside this result
- [x] A trusted instance now attaches the runtime goroutine group during
      initialization and entry. Native children inherit membership, and an
      internal live count includes parked children; this is not quiescence or
      `Kill` support
- [x] The group now conservatively counts goroutines transitioning into and
      out of the runnable scheduler state. A 100-run race test covers a
      parked child, wakeup, and exit; this is still a diagnostic, not a
      quiescence decision
- [x] The runtime group also tracks goroutines associated with an execution
      thread across dispatch, park, yield, exit, and coroutine switches. It
      counts syscalls conservatively and is a diagnostic, not a revocation or
      safe-teardown proof
- [x] `Wait` reports a normal return, panic, or `Goexit` from the isolate
      program's main goroutine after `Done`; `New` returns an error for an
      initializer panic or `Goexit` without terminating its host caller.
      Child goroutine panics remain a process-wide failure until native
      lifecycle handling exists
- [x] Direct-toolchain static probe linked two separately compiled configured
      `package main` directories into one host and ran both through the
      trusted `Call` boundary (the original probe also had `Inbox`)
- [x] Experimental `go build -isolate-dir` loads configured `package main`
      directories under distinct paths, generates the name-to-entry table,
      and links one executable with the host. The trusted host API can look up
      each program, start its `main`, and exchange copied `Call` bytes;
      a `cmd/go` script test builds and runs two programs by name
- [x] The static build selects configured `package main` units and their
      reachable non-standard dependencies for the opt-in package-state mode.
      It registers the generated descriptors, and `isolate.New` allocates and
      initializes the selected layouts in dependency order. A build script
      runs two instances of one program and one of another, both importing
      the same initialized package, and verifies fresh state in each;
      standard-library state remains process-wide
- [x] Generated-entry compiler mode retains selected application code while
      omitting process-startup init tasks only for packages the host cannot
      reach. A package imported by both host and isolate initializes once for
      the process and once per instance; the build script checks separate
      host and instance state and exact init counts
- [x] The static builder selects audited `encoding/base32` and
      `encoding/base64` when an isolate program reaches them. A script
      verifies independent `StdEncoding` state across two instances and the
      host, including per-instance initialization
- [x] Exported generic functions instantiated in importers use selected
      unexported globals. Generated offsets cover those globals, while generic
      dictionaries remain shared metadata; the static script checks reads,
      writes, second-instance reset, and separate host state
- [x] Optional `go build -isolate-report=FILE` emits a sorted JSON inventory of
      each program's reachable packages, instance-state selection, and host
      reachability. Unselected standard packages are labeled unclassified;
      the build script verifies selected, shared, and unclassified examples
- [x] A static build script first exposed that `encoding/json` v2 writes
      callback globals in `encoding/json/internal`, making JSON-only selection
      unsafe. The builder now selects JSON's mutable v2 dependency family and
      `reflect` together. The script round-trips JSON and checks distinct v2
      default-options objects across two instances and the host; heap and
      effect ownership remain unproved
- [x] The compiler rejects direct writes from selected package code to
      unselected imported globals, including writes in initializers, ordinary
      functions, closures, and indexed assignments. A build script checks
      `os` globals and the `encoding/json/internal` callback assignments.
      It also rejects direct `copy`, `clear`, `delete`, `append`, channel send,
      and channel close targeting an unselected imported global. Writes
      through aliases, ordinary calls, and unsafe pointers remain outside
      this gate
- [ ] Classify ownership and effects across the broad standard library;
      implement required library and runtime hooks and reject unclassified
      paths before claiming general standard-library support
- [ ] Replace the temporary standard-library selection list with default
      isolate ownership for mutable reachable Go state, an explicit
      process-service exception set, allocation ownership, and cross-owner
      pointer checks. Share immutable tables or materialize state lazily so
      broad selection does not eagerly copy standard-library data per instance
- [ ] Make runtime cleanup callbacks owner-aware before supporting `unique`
      and transitive users such as `net/netip`; `uniqueMaps` can hold pointers
      to isolate data, and `runtime.AddCleanup` runs outside the isolate
- [x] Provisional runtime guards reject `SetFinalizer`, `AddCleanup`, and
      `unique.Make` inside an active isolate; `unique.Make` rejects before
      touching its process map. Tests cover direct calls, `net/netip.WithZone`,
      and a child goroutine. Owner-aware callbacks remain pending
- [x] Provisional runtime guards reject `LockOSThread`, `UnlockOSThread`,
      `NumCPU`, `NumCgoCall`, `NumGoroutine`, `GOMAXPROCS`,
      `SetDefaultGOMAXPROCS`, and `ReadMemStats` before accessing process
      state. Other process-state APIs and isolate-fatal handling remain open
- [x] `os.Exit` and direct `syscall.Exit` now request whole-isolate
      termination, hard discard their caller, and report nonzero status
      through `ExitError` without ending the host process. Initializer and
      child exits are covered; other process-control entry points remain open
- [x] Provisional guards cover `runtime/debug` process diagnostics and
      settings, including heap dump and traceback runtime entries. Its pure
      `ParseBuildInfo` parser remains usable; other effectful packages and
      isolate-fatal enforcement remain open
- [x] Provisional guards reject `os/exec.Cmd.Start`, `os.StartProcess`, and
      direct `syscall.ForkExec`, `StartProcess`, and `Exec` before launch.
      Raw syscalls and other process effects remain open pending the static
      subset gate and Tier 1 audit
- [x] Provisional guards reject `os` and `syscall` process environment reads
      and mutations before host state is accessed. Per-isolate environment
      values and `os.Args` routing remain open
- [x] Provisional `os/signal` guards reject process-wide signal
      registration, handler changes, and signal-state reads before accessing
      the handler table
- [x] Provisional guards reject direct runtime GC requests and process
      profile rates/snapshots, plus `runtime/pprof` process-wide registry,
      mutation, output, and CPU profiling. Goroutine context labels remain
      available; other profiling entry points still need audit
- [x] Provisional `runtime/trace` guards reject process trace and flight
      recorder controls. Isolate annotations are inert while preserving
      `WithRegion`'s function call; the host trace stays active
- [x] Provisional entropy guards reject public `crypto/rand.Read`, its
      default `Reader`, internal DRBG reads, and OS entropy reads. A direct
      DRBG-backed ML-KEM path is covered; other cryptographic setup effects
      still need audit. `runtime/metrics.Read` rejects process metrics
- [x] `runtime/metrics.All` copies static descriptions for an active isolate,
      preventing writes through its returned slice from changing host data
- [x] Runtime `g` carries a monotonic numeric instance owner ID through
      initializer replay, the generated state runner, `main`, and native
      children. A race test checks distinct instance IDs and restored host
      context, with `Call` unavailable during init;
      heap routing and cross-owner checks remain pending
- [x] Large-object spans record the active numeric allocation ID and clear it
      on reuse. Focused and race tests check zero and nonzero origins.
      This is diagnostic metadata: small allocations still share per-P spans,
      while incoming requests now use owner-copied `Call` replies
- [x] Map headers carry the creator's numeric owner, including optimized
      stack maps. Assignment, deletion, and clearing across owners fail closed
      even through aliases or reflection; map value pointers and other data
      structures still need cross-owner write checks
- [x] Foreign isolate map lookups and iteration reject across owners,
      including reflection and iterator advancement. Process-owned maps stay
      readable. Isolate builds route `len(map)` through the same owner check;
      ordinary builds keep the original direct header access
- [x] The trusted `Call` bridge allocates the host command and request copy in
      process context, then copies the reply under the isolate ID. A 100-run
      race test checks large request, host copy, and reply origins
- [x] `sync.Pool` discards `Put` and uses fresh `New` values while an isolate
      boundary or selected package-state table is active, preventing a
      process-wide per-P pool from retaining isolate objects or passing them
      between instances. Focused entry, child, and initializer tests pass
- [x] `fmt` rejects its six implicit process stdin/stdout functions inside an
      isolate; string formatting and explicit reader/writer calls still work.
      Explicit stream effects remain unclassified
- [x] The static probe initializes `regexp/syntax`'s Unicode alias cache at
      process startup, and the multi-program script exercises a Unicode regex
      inside an isolate. Generic immutable sharing and reader effects remain
      unclassified
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
- **Independent same-path dependency versions and plugin loading** — the
  static MVP links one compatible definition per import path. Per-program
  package namespaces and runtime loading require separate proofs; see
  STATIC_PROGRAMS.md and DYNAMIC_LOADING.md.

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
opt-in cross-package inlining test passed 100 native arm64 race runs. An
earlier tagged compiler test required exported generic functions and methods
to be rejected in layout mode; the 2026-09-30 generic layout change replaced
that guard with imported access to their unexported globals.
The complete `src/all.bash` rerun passed, including race and `../test`, after
restoring the container's missing `/etc/services` through `netbase`.
The subsequent two-package dependency probe also passed a complete native
`src/all.bash` run, alongside 100 tagged race-detector runs of its own tests.
The opt-in `encoding/base64` initialized-state probe passed 100 tagged native
race runs and another complete native `src/all.bash` run.

The 2026-09-29 `Call`/`Inbox` boundary probe passed `src/make.bash`,
`go test -race -count=100 isolate`, and `go test runtime -count=1`. The
earlier runtime run found a `runtime.g` size assertion that was corrected;
it also failed in an execution environment that denied `ptrace` and local
listening sockets. After those permissions were available, the focused
`TestUsingVDSO` and `TestNetpollWaiters` checks and the complete runtime
suite passed.

The 2026-09-30 static build integration passed `src/make.bash`, a focused
`cmd/go` script test, and a separate two-program build/run from an external
module. The next slice also passed `src/make.bash`, the script's initialized
state check for two instances, and `go test -race isolate`. A later script
check passed for a dependency imported by two programs and for initializer
dependency order. A further focused check passed with process-startup
initialization suppressed for selected packages unused by the host. The
complete Linux arm64 `src/all.bash` suite passed after
updating the new packages' dependency policy and generated package/help lists.
The rebuilt `bin/go` also built and ran a standalone two-program example from
an external module. The startup-initialization change passed `make.bash`, its
focused `cmd/go` script test, and a subsequent complete Linux arm64
`src/all.bash` run.

The provisional `sync.Pool` boundary passed focused `sync`, `isolate` race,
and static-build script tests, followed by a complete Linux arm64
`src/all.bash` run. Other standard-library caches still require an ownership
audit.

The shared-package startup rule and selected `encoding/base64` state passed
`src/make.bash`, the focused static-build script, and a complete Linux arm64
`src/all.bash` run. The script checks distinct host and isolate globals for
both an application package and `encoding/base64`.

The exported-generic layout change passed `src/make.bash`, the focused
static-build script, the tagged generic compiler test, a standalone `-race`
build/run, and a complete Linux arm64 `src/all.bash` run.

The optional package ownership report passed the focused static-build script
and `TestDocsUpToDate`. The provisional cleanup guards passed `src/make.bash`,
`go test -race isolate`, ordinary `unique` and `net/netip` tests, and a
complete Linux arm64 `src/all.bash` run.

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
