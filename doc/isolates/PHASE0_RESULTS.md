# Phase 0 results and open gates

Recorded 2026-09-27 against Go tree `2ff5743d9f` on Linux arm64.
These are feasibility observations and the scoped Phase 0 path decision.
The command blocks below start from the repository root.

## Requirements fixed for the gates

- Replay must be identical across CPU architectures. Ordinary map iteration
  therefore needs an architecture-independent hash and iterator mechanism, or
  an enforceable narrower key/iteration contract. The Phase 1 prototype
  offers `SortedKeys` only for strings and integer types.
- The earlier forceful-kill experiment targeted Linux arm64 and 100 ms from
  request to stop. The target was changed from amd64 to match this container
  on 2026-09-27. Forcefully cleaning up uninterrupted computation is now
  **future work, outside the MVP**; the 100 ms result is no longer an MVP gate.
- A future forceful kill skips user defers and discards the whole isolate. A canceled
  `Resume(ctx)` returns without killing and leaves the isolate resumable.
  These choices were confirmed on 2026-09-27.
- MVP `Kill(ctx)` revokes the whole isolate and waits for the executing
  goroutine to stop at a runtime-controlled point. If `ctx` expires first,
  it returns a pending error with a best-effort stack and sampled OS thread
  ID; the request is not undone. Go's CPU profiler unwinds running goroutines
  from signal context, and a targeted Phase 0 diagnostic probe below exercises
  that mechanism.
- Hostile tenants remain a future release goal. The MVP and Phase 1 package
  are for trusted workflows; neither claims forceful runaway termination.

## E1: execution coverage, partial

`src/internal/isolateproto` is an executable reference model. Its baton
scheduler sees only `Task.Go`, `Task.Call`, `Task.Inbox`, `Task.Yield`,
`Channel.Send`/`Receive`/`Close`, and `SelectReceive`. Native `go`, channels,
`select`, `sync`, map range, reflection, `time.Now`, and `time.Sleep` do not
pass through those methods. The ordinary-Go E1 coverage proof is still open.

The opt-in E1 sample executes native `go`, channel operations, `select`,
`sync.WaitGroup`, `sync.Mutex`, `sync.Cond`, `reflect.Select`, ordinary and
reflected map iteration, `time.Now`, `time.Sleep`, `context.WithDeadline`,
`time.AfterFunc`, and a native parked goroutine. The coordinator sees
only its explicit `Task.Call`; the native parked goroutine remains invisible
while `Resume` reports quiescence. Run:

```bash
cd src
../bin/go test -race -tags=phase0_e1 -run='^TestNativeCoverage$' -count=1 internal/isolateproto
```

The full standard-library call-path inventory and enforceable classification
of unsupported operations remain open. The sample passed ten race-detector
runs but does not establish completeness.

For the Phase 0 path decision, E1 is a negative result for Phase 2A: the
prototype cannot account for the native parked goroutine, and a source
rewriter would need whole linked-package coverage plus a proof that all
indirect calls use rewritten state. Neither proof exists. We will not build
E0's `-toolexec` integration for an unselected path. The compiler/runtime
path has concrete interception points, but it still needs the Phase 2B
source-path audit and native quiescence tests before any ordinary-Go claim.

Initial source-path inventory for the sample's indirect operations:

| User operation | Current path in this tree | Phase 1 classification |
|---|---|---|
| `reflect.Select` | `reflect/value.go` `rselect` → `runtime/select.go` `reflect_rselect` | Untracked; outside contract |
| `sync.Cond.Wait` | `sync/cond.go` → `runtime_notifyListWait` | Untracked; outside contract |
| `context.WithDeadline` | `context/context.go` → `time.AfterFunc` | Untracked; outside contract |
| `time.AfterFunc` | `time/sleep.go` → runtime timer; callback starts native goroutine | Untracked; outside contract |

The initial fork-path touch points in this tree are `runtime.newproc1` for
native `go`, `runtime.chansend`/`chanrecv` and `selectgo` for channels and
selection, `sync.Mutex`/`WaitGroup`/`Cond` through their runtime semaphore or
notify paths, `internal/runtime/maps.Iter.Init`/`Next` for map order,
`time.Now` through `time.now`/`runtimeNow`, and `runtime.timeSleep` plus the
timer heap for sleeps and deadlines. `runtime.ready` is only one of the
scheduler paths that must be audited. This list locates mechanisms; it is
not a complete wakeup or standard-library call graph.

Phase 2 must instrument these paths or reject them through an enforceable
subset; allowing them unmodified would make quiescence and replay unsound.

The prototype's tests exercise activity fan-out, a signal, a host-driven
timer, a child-workflow command, command correlation, copied boundary values,
FIFO scheduling, quiescence, deadlock, and an end-to-end host loop. The native
ordinary-Go conformance workload is still required for the fork path.
The prototype now also tests whole-instance `Kill(ctx)` revocation before
start, while tasks are parked in `Call` and `Inbox`, and while one task spins
outside its Task operations. The pending error records its task ID; the
prototype cannot target an OS thread or collect its stack. A later `Kill`
waits for that task to return to a Task boundary and exit. The kill tests
passed 20 normal and 20 race-detector repetitions on native arm64.
The kill and fixed-byte replay tests also passed 20 repetitions in an
amd64 binary under `qemu-x86_64` on this arm64 host.
The prototype suite passed 20 race-detector repetitions each with
`GOMAXPROCS=1` and `GOMAXPROCS=4`, both at `GOGC=20`; the replay test also
forces GC during its runs. This checks the explicit scheduler only.

A fixed command/result fixture now checks sorted integer keys, numeric call
IDs, seeded random output, and logical time against exact bytes. It passed 20
repetitions on native Linux arm64 and 20 on an emulated Linux amd64 binary;
the complete Phase 1 test suite also passed 20 amd64-emulated repetitions.
The amd64 run used `GOARCH=amd64 CGO_ENABLED=0 go test -c` followed by
`qemu-x86_64` on this arm64 host. Emulation verifies the compiled amd64 code's
observable results, but it does not establish native amd64 performance or
ordinary-Go map iteration determinism. The fixture is
`TestCrossArchitectureReplayFixture`.
An attempted Linux/386 run under this container's `qemu-i386` crashed during
Go runtime startup; a minimal binary built with the unmodified Go 1.26.7
bootstrap toolchain crashed there too. This is an emulator/environment limit,
not an observed replay mismatch. A native 32-bit run remains unverified.

## E2: source-level canonical iteration cost, partial

The selected feasible mechanism for the restricted E2 prototype is a sorted
key snapshot with a live lookup before each yield. `RangeMap` implements it
for strings and signed/unsigned integer keys, including named types. Deleted
keys are skipped, changed values are read at the visit, new keys are skipped,
and a callback can stop iteration. Each choice is allowed by Go's map range
semantics. Pointer, float, interface, array, and struct keys are outside this
prototype's contract; `reflect.Value.MapRange` and ordinary `for range` still
need compiler/runtime handling before an ordinary-Go claim. Mutation and key
order tests passed 20 times on native arm64 and 20 times under emulated amd64.
This result selects a mechanism for E2's narrow key contract, not a complete
ordinary-map implementation.

In a new same-process run on this host, a 1,000 string-key sum took
50.2–52.1 µs/op with native range and 142.9–144.1 µs/op with `RangeMap`
(three 500 ms runs). `RangeMap` allocated about 16.5 KB/op. A repeated
single-P run measured 50.8–51.6 µs/op native and 147.1–148.4 µs/op canonical.
Run:

```bash
cd src
../bin/go test -run='^$' -bench='^Benchmark(MapRange|RangeMap)1000$' \
  -benchtime=500ms -count=3 -benchmem internal/isolateproto
```

An earlier run on the same host measured a faster 15.5–15.8 µs/op native
baseline; the same-process ratios above are more useful than comparing
absolute times across runs. The precise reason for the baseline shift has
not been isolated.

On this Linux arm64 tree, summing 1,000 string-keyed map values took
15.5–15.8 µs/op with ordinary range and 114.6–116.4 µs/op with `SortedKeys`
(three 500 ms runs each). Sorting allocated about 16.5 KB/op. Run:

```bash
cd src
../bin/go test -run='^$' -bench='^Benchmark(MapRange|SortedKeys)1000$' -benchtime=500ms -count=3 -benchmem internal/isolateproto
```

This measures one source-level fallback for string keys. It does not compare
the runtime's portable and AES hash paths, resolve pointer/interface/NaN key
semantics, or establish cross-architecture ordinary-map replay. The Phase 1
`SortedKeys` fixture above did match between arm64 and amd64 under emulation.

The tree already has `internal/runtime/maps.BenchmarkHashBakeoff`. On this
arm64 machine its 64-byte throughput results were 4.84–4.88 ns/op for the
scalar fallback and 2.897–2.912 ns/op for AES; serial latency was
8.16–8.22 ns/op for scalar and 12.51–12.60 ns/op for AES. At 16 bytes the
throughput paths were about equal (2.20 versus 2.07 ns/op); at 128 bytes
scalar throughput was 6.61–6.78 ns/op versus 5.00–5.05 ns/op for AES.
These are direct hasher microbenchmarks with process-global keys, not a
portable isolate hash or a map-operation benchmark. Run:

```bash
cd src
../bin/go test -run='^$' -bench='^BenchmarkHashBakeoff/(scalar|aes)/(latency|throughput)/(16|64|128)$' -benchtime=300ms -count=3 internal/runtime/maps
```

## E3: suspended-memory proxy, partial

Command:

```bash
cd src
../bin/go test -run='^$' -bench='^BenchmarkIdleProxy$' -benchtime=1x -count=1 -timeout=120s -v internal/isolateproto
../bin/go test -run='^$' -bench='^BenchmarkFanoutProxy$' -benchtime=1x -count=1 -timeout=120s -v internal/isolateproto
```

The toolchain was built from this tree with `src/make.bash`. At 10,000
instances, after GC, incremental process memory was:

| Parked workload | HeapAlloc / instance | StackInuse / instance | HeapInuse / instance | Incremental RSS / instance |
|---|---:|---:|---:|---:|
| One task on Inbox | 1,803 B | 4,119 B | 2,022 B | 6,281 B |
| Two child Calls, parent on channel | 4,480 B | 12,304 B | 5,217 B | 17,835 B |

A 10k-instance mixed proxy with 90% Inbox waiters and 10% fan-out waiters
used 2,248 B HeapAlloc, 4,932–4,935 B StackInuse, and 7,538–7,562 B
incremental RSS per instance in three fresh processes. Each benchmark now
revokes all instances after taking the memory snapshot. Run
`BenchmarkMixedProxy` with `-benchtime=1x -count=1`; use separate processes
for independent RSS observations. The mixed average is below the 8 KB
median target, but an average is not a median or p99, and the homogeneous
fan-out proxy already exceeds 8 KB of heap plus stacks. Neither result can
establish a per-instance percentile without ownership accounting. The proxy
therefore leaves density acceptance open for Phase 3's real implementation.

The fan-out proxy thus retains about 16.8 KB of heap plus stacks per
instance, and its RSS increment was about 17.8 KB per instance in a separate
process run. These are process-level increments, not owned memory: Go scheduler
records, goroutine stacks, prototype maps, and shared allocator slack are
mixed together. RSS was read from `/proc/self/statm` before and after creating
the 10k instances; it includes touched shared runtime pages and depends on
allocator warmup. This is neither a median/p99 across realistic workflows nor
the real owned-span implementation. Root-scan cost, mark and assist CPU,
allocation throughput, and stop-the-world latency remain to be measured. No
density gate passes from this proxy alone.

An additional forced-GC proxy used 20 collections per run, three runs in
fresh benchmark processes. Baseline GC averaged 0.217–0.237 ms; with 10k
one-task instances parked on Inbox it averaged 4.76–4.87 ms. The benchmark
completes and releases the instances after each run. This wall-time comparison
shows the prototype's idle goroutines matter to GC, but it does not separate
root scanning, marking, assists, or stop-the-world time and does not predict
the real owned-memory implementation. Run `BenchmarkBaselineGC` and
`BenchmarkIdleGC` separately with `-benchtime=20x -count=3`.

A more detailed proxy used 200 forced GCs per run, three runs per state. The
baseline scanned about 18.5 KB of stacks and 0.19 MB of heap in its last
cycle; 10k parked instances scanned about 14.9 MB of stacks and 12.2 MB of
heap. Dedicated mark CPU rose from 0.042–0.048 to 7.7–8.6 ms per GC, and
forced-GC wall time from 0.20–0.21 to 5.0–5.5 ms per GC. Assist CPU was zero
for these forced collections. Average stop-the-world time was 0.052–0.058 ms
at baseline and 0.021–0.026 ms with idle instances; this short, process-level
measurement does not show an STW regression. One idle run had a 0.418 ms
maximum pause, while the other two were below 0.077 ms. Run
`BenchmarkBaselineGCProfile` and `BenchmarkIdleGCProfile` separately with
`-benchtime=200x -count=3`.

A separate 1 KiB allocation loop retained the latest 1 MiB of allocations.
In three 2-second runs, baseline throughput was 3.48–3.54 GB/s and 10k idle
instances yielded 4.17–4.23 GB/s. Dedicated mark CPU per allocated MiB rose
from 0.045–0.046 to 0.184–0.189 ms; assist CPU rose from 0.015–0.018 to
0.021–0.023 ms/MiB. Throughput was higher in the idle condition in this
microbenchmark, so it does not establish a throughput penalty. Scheduler,
cache, and GC timing need closer control for a causal throughput claim. Run
`BenchmarkBaselineAllocationThroughput` and
`BenchmarkIdleAllocationThroughput` separately with `-benchtime=2s -count=3`.
These metrics are still for the explicit Phase 1 scheduler, not owned spans
or suspended-state compaction.

## E4: initialized globals, scoped feasibility result

The Phase 2 conformance test creates a map, pointer, and closure during init.
Two prototype instances produce `1/1/1` and `2/2/2`; the second result must
be `1/1/1` once initialized state is isolated. Run it with:

```bash
cd src
../bin/go test -tags=phase2_isolation -run='^TestInitializedGlobalsAreIsolated$' -count=1 internal/isolateproto
```

The test is deliberately excluded from the default suite until Phase 2.
An opt-in toy E4 benchmark measured one hot global increment on arm64:
direct 2.088–2.096 ns/op, accessor 2.179–2.183 ns/op, and base-plus-offset
2.177–2.184 ns/op (three 500 ms runs). Run:

```bash
cd src
../bin/go test -tags=phase0_e4 -run='^$' -bench='^BenchmarkE4(Direct|Accessor|BaseOffset)$' -benchtime=500ms -count=3 internal/isolateproto
```

The accessor and base shapes were about 4% slower in this microbenchmark.
This benchmark does not include compiler-generated indirection, a workflow
operation, or initialized-graph isolation. The later probes below cover
narrow examples of those separately.

A tagged toy package now calls its compiler-generated `init.0` again after
selecting a fresh state base. Its `init` allocates a map, pointer, and closure;
two instances run interleaved and each produces `1/1/1`, then `2/2/2`. A
tagged runtime getter stores the selected base on the current `g`, copies it
to a newly created user goroutine, and clears it when the goroutine exits.
Two instances also run concurrently without sharing their initialized graphs;
a child created by `go` inherits its parent's base. The tests passed 100
race-detector repetitions on Linux arm64:

```bash
cd src
../bin/go test -race -tags=phase0_e4 -count=100 internal/isolateproto/testdata/e4toy
```

This demonstrates that the generated init function can execute again while
the test redirects its global accesses, and that a per-goroutine base can
follow native `go` creation in this narrow case. The toy explicitly calls a
runtime getter at every global access. It does not prove automatic compiler
rewriting, package dependency init order, standard-library initialized-state
isolation, or that all paths that create goroutines preserve this base. The
process-global `initTask.state` is still unchanged.

A second tagged toy removes the source-level getter from `init` and its entry
function. The compiler's opt-in `-d=isolatee4=1` flag rewrites address
generation for that package's `global` graph and `epoch` integer symbols to
use the per-`g` base and a second field offset, falling
back to the ordinary global before a base is selected during process startup.
The toy again reruns compiler-generated `init.0` for two instances. Its
map/pointer/closure graphs remain separate under interleaved calls, parallel
goroutines, and a native child goroutine. It passed 100 race-detector runs at
`GOMAXPROCS=4`, `GOGC=20` on Linux arm64:

```bash
cd src
GOMAXPROCS=4 GOGC=20 ../bin/go test -race \
  -tags=phase0_e4,phase0_e4_compile \
  -gcflags='internal/isolateproto/testdata/e4compiletoy=-d=isolatee4=1,isolateinit=1' \
  -count=100 internal/isolateproto/testdata/e4compiletoy
```

The same toy registers an entry once during process initialization. Repeated
isolate initialization leaves this host-owned registration alone while its
registered function reads the selected isolate's initialized graph. Two
independent instances completed through the Phase 1 `Resume` host loop with
`1/1/1` results. The updated suite passed 100 race-detector runs on arm64
and 100 runs under emulated amd64.

The rewrite targets two statically named globals in one toy package. The
second offset is a toy-specific three-pointer constant checked by a test;
there is no generated package layout yet. The rewrite does not lay out
globals across packages, rewrite all access modes or assembly,
replay dependency init tasks, or isolate a standard-library package. It is a
compiler feasibility result, not a Phase 2B implementation.
The compiler toy also passed 100 runs under emulated Linux amd64 after
cross-compiling from this arm64 host. Another test creates 64 instances in
parallel and checks that each initialized pointer is distinct. The opt-in
compiler flag and build tags are required; ordinary builds do not acquire
global isolation.

On this machine, a five-run workflow-shaped benchmark of a map update,
pointer mutation, and closure read measured 13.38–14.75 ns/op for a direct
package global and 18.68–18.87 ns/op for the rewritten global, with zero
allocations in either case. The small operation pays for several runtime
getter calls; this toy does not hoist the base or use a direct `g`-offset
load. After the two-global layout change, three more runs measured
13.39–13.45 ns/op direct and 18.52–18.73 ns/op rewritten, again with zero
allocations. Run:

```bash
cd src
GOMAXPROCS=1 ../bin/go test -tags=phase0_e4,phase0_e4_compile \
  -gcflags='internal/isolateproto/testdata/e4compiletoy=-d=isolatee4=1,isolateinit=1' \
  -run='^$' -bench='^Benchmark(ProcessGlobal|CompilerGlobalBase)$' \
  -benchtime=500ms -count=5 -benchmem internal/isolateproto/testdata/e4compiletoy
```

The E4 exit criterion is met for the scoped toy: compiler-directed global
access isolates its initialized map, pointer, closure, and integer state, including
through the registered entry, and its overhead is measured. The process-wide
registration must be classified separately from isolate-owned globals.
For the first implementation, rerunning deterministic, restricted package
initializers is the supported E4 direction. Copying `.data` and `.bss` alone
would leave the map, pointer, and closure-reachable graph shared. A template
would additionally need a validated relocation of that heap graph; the E5b
relocation proof is still open. This chooses an approach for the prototype,
not a claim that the current compiler flag handles arbitrary packages.

A second E4 benchmark updates a small result map, a pointer allocated during
initialization, and a closure while one state base is already selected. With
three 300 ms runs on arm64, direct access was 5.39–6.59 ns/op, an accessor was
5.58–6.77 ns/op, and base-plus-offset access was 5.52–6.84 ns/op; all were
zero-allocation. The ranges overlap, so this does not establish a reliable
overhead difference. It excludes scheduler switching and compiler-generated
indirection. Run:

```bash
cd src
../bin/go test -tags=phase0_e4 -run='^$' -bench='^BenchmarkE4Workflow' -benchtime=300ms -count=3 -benchmem internal/isolateproto
```

## E5a: future forceful-kill probe; 100 ms stress gate failed

On this tree `runtime.preemptone` (`src/runtime/proc.go`) explicitly describes
its request as best-effort: it can miss or target the wrong goroutine. The
async-preemption continuation (`runtime/preempt.go`) parks or deschedules a G;
it does not terminate it. `goexit0`/`gdestroy` expect a running G, while a
channel wait also owns queue and `sudog` records. Therefore neither existing
preemption nor `Goexit` alone establishes the 100 ms bound or safe teardown.
`isAsyncSafePoint` excludes runtime/internal-runtime/reflect code, assembly
without pointer maps, unsafe-point regions, and `runtime/secret` regions; its
comment guarantees safe suspension, not bounded reachability. These exclusions
need an enforceable duration or subset policy for a 100 ms worst-case claim.

A tagged runtime probe now calls `suspendG` on an external user-code CPU loop
and a blocked channel waiter. In ten runs of 100 running-loop suspensions,
the median per run was 7.5–12.3 µs; one outlier reached 15.47 ms. A blocked
channel waiter took 83–167 ns to suspend. The probe uses the current runtime's
safe-point mechanism and resumes each G; the blocked channel's wait record
remains linked. An initial loop defined inside package `runtime` hung the
probe because that code is excluded from async safe points, confirming the
source-level exclusion. Run:

```bash
cd src
../bin/go test -tags=phase0_e5a -run='^TestIsolate(.*)SuspendLatencyPhase0$' -count=10 -timeout=30s -v runtime
```

An opt-in scheduler hook now discards a registered G before returning from a
preemption point to user code. It does this in `execute`, which permits write
barriers; an earlier direct `goexit0` call from `preemptPark` was rejected by
the compiler's `nowritebarrierrec` check. An indirect call there compiled but
evaded the safety check, so that version was removed. The current hook runs
trace/race end bookkeeping and calls `gdestroy`; a test-tag hook releases
arm64's saved extended-register block and detached wait records after the G
becomes dead but before it enters the reuse pool. The register cleanup was
found by a repeated-run
crash (`gp.xRegState.p != nil on async preempt`) and tested after the fix.

The probe covers a busy user-code loop, one parked channel receiver whose
channel is closed to dequeue it, a loop holding a `sync.Mutex` with no waiters,
one blocked mutex waiter, a waiter behind another on the same semaphore
address, and a `sync.Cond` waiter with the newest ticket. User defers do not
run; the held mutex stays locked and must be discarded with isolate-owned
state. The channel path
releases the receiver's `sudog` after `closechan` wakes it. The semaphore path
detaches head or later waiters while holding the root lock and releases their
`sudog` after teardown. The Cond path unlinks the newest ticket and atomically
rolls back the ticket counter, preserving the next `Signal` for an earlier
live waiter. If another waiter obtained a newer ticket, this narrow path
refuses the kill because removing an earlier ticket leaves a hole.

The first four shapes passed 100 repetitions under normal and race builds.
Loop and channel cases additionally passed 1,000 normal repetitions and 200
with `GOGC=1`; the non-head semaphore and sole Cond cases passed 100 normal,
race, and `GOGC=1` repetitions. The newest-ticket Cond case with two waiters
also passed 100 normal, race, and `GOGC=1` repetitions; one `Signal` woke the
surviving earlier waiter. Traces for the earlier tested shapes parsed. Observed
request-to-stop times were below 100 ms, but finite samples are not a bound.
The running-loop kill also passed 100 repetitions at `GOMAXPROCS=1` and 20
race-detector repetitions, checking the single-P scheduling case.
The narrow kill probes also passed 1,000 runs with `GODEBUG=clobberfree=1`.
An 8 MiB heap object reachable only from a killed G's stack was reclaimed
after forced GC in 10 normal, race, and clobberfree runs. This checks one
stack-root case; it is not owned-span reclamation or cross-owner verification.
Run these probes only on Linux arm64:

```bash
cd src
../bin/go test -tags=phase0_e5a -run='^TestIsolate.*KillPhase0$' -count=100 -timeout=30s runtime
../bin/go test -race -tags=phase0_e5a -run='^TestIsolate.*KillPhase0$' -count=100 -timeout=30s runtime
../bin/go test -tags=phase0_e5a,phase0_e5a_acceptance -run='^TestIsolate(NonHeadMutex|Cond|MultiCond)WaitHardKillAcceptancePhase0$' -count=100 -timeout=30s runtime
```

This is **not** a general kill implementation. `suspendG` still spins without
a deadline if a G does not reach a safe point. The channel path handles one
receiver and requires closing its channel; senders, `select`, timer channels,
earlier-ticket Cond waiters, and races with normal wakeups have no safe teardown
proof. The probe does not identify all goroutines owned by an isolate or prove
that their reachable heap is detached from GC and other runtime tables. It
cannot support a 100 ms worst-case, safe teardown, or hostile-code release
claim. The opt-in `phase0_e5a_acceptance` suite includes a two-waiter Cond
case whose first (non-tail) ticket currently fails after 100 ms; removing it
while keeping the later waiter alive could consume a future `Signal` meant
for that waiter. Run `TestIsolateNonTailCondWaitHardKillAcceptancePhase0` to
see that explicit red case. A real isolate should own the entire Cond and all
its waiters, so group teardown could discard both tickets together; this
probe has no owner model and does not prove that path. After moving
register/waiter cleanup to the point between `_Gdead` and
`gFree`, the native Linux arm64 `src/make.bash` rebuild and full
`go test runtime` suite passed. E5a remains open.

The allowed built-in `copy` supplies a stronger counterexample to the
current kill mechanism. An opt-in Linux arm64 test repeatedly copies a 4 GiB
slice into another 4 GiB slice (about 8 GiB resident memory), requests kill
while the target is active, and stops the target cooperatively if the request
fails. With default GC the request returned after **307 ms with the target
still alive**. With `GOGC=off` it returned after **3.44 s with the target
still alive**. A standalone 4 GiB `copy` on this host took 128–204 ms. The
test demonstrates that the current `suspendG` plus `execute` hook does not
acknowledge termination within the recorded 100 ms bound for an allowed
operation; it does not by itself isolate the exact share of delay due to
`memmove`, suspension, or scheduler handoff. The probe acknowledges only after
`gdestroy`, so this result does not timestamp the target's last instruction.
When it times out, the probe clears its temporary kill target; a production
execution fence must remain in force so queued or parked goroutines cannot
run later. Those goroutines need not be scheduled merely to be revoked, and
their storage can be reclaimed after execution has stopped.
Run the deliberately failing test only on a machine with at least 8 GiB free:

```bash
cd src
../bin/go test -tags=phase0_e5a,phase0_e5a_stress -run='^TestIsolateLargeCopyHardKillAcceptancePhase0$' -count=1 -timeout=30s runtime
```

This failed probe blocks a forceful-kill claim, not the trusted MVP's Phase
2B work. A future forceful-kill milestone needs a bounded preemption policy
covering large Tier 1 operations as well as user loops, plus whole-isolate
waiter and heap teardown. The MVP explicitly excludes that claim.

For the MVP pending-error diagnostic, a second tagged probe sends a signal
to the registered running goroutine's OS thread and unwinds from its signal
context into a fixed, allocation-free buffer. It sampled the intended busy
Go function in 100 normal runs; 20 race-detector runs returned a symbolized
top PC, sometimes without the caller frames. One run against the large-copy
stress case returned the OS thread ID and this stack while the kill probe
remained pending:

```text
runtime.memmove
runtime_test.TestIsolateLargeCopyHardKillAcceptancePhase0.func1
runtime.goexit
```

This proves a useful best-effort diagnostic on native arm64, including inside
assembly excluded from Go's async safe points. It does not guarantee a full
stack or a sample by any fixed deadline. An error must still be returned if
sampling fails. The tagged hook is not the production `Kill(ctx)` path.

```bash
cd src
../bin/go test -tags=phase0_e5a,phase0_e5a_diag -run='^TestIsolateRunningStackSamplePhase0$' -count=100 runtime
../bin/go test -race -tags=phase0_e5a,phase0_e5a_diag -run='^TestIsolateRunningStackSamplePhase0$' -count=20 runtime
```

After adding this signal hook, the native Linux arm64 `src/make.bash` rebuild
and full `go test runtime` package suite passed.

## Phase 0 path decision for the trusted MVP

The written requirements and E1–E4 scoped results are sufficient to choose
the **Phase 2B compiler/runtime path**. E1 rules out a claim that the pure-Go
prototype observes ordinary Go, and the missing whole-build coverage proof
rules out Phase 2A for this work. E0 is therefore inapplicable. E2 selects
canonical sorted iteration for the restricted string/integer key set; its
native and emulated cross-architecture tests pass, with about 2.8–2.9x
native cost in the current 1k-key run. E3 measures the proxy's 10k-instance
RSS and GC costs but leaves owned-memory percentiles for Phase 3. E4 proves
an initialized map/pointer/closure graph can be independently recreated by
rerunning a restricted initializer through a compiler-selected global base.

No snapshot or migration is in the trusted MVP contract. The mixed proxy's
average RSS does not prove that compaction is required, so E5b is not
triggered yet. Phase 3 must repeat E3 on real owned isolates and decide
whether density then triggers E5b and Phase 4. E5a remains a Phase 5 gate.

The selected path still has major implementation gates: Phase 2B must replace
the one-symbol E4 probe with general global layout and access, handle package
dependency initialization and a standard-library initialized-state case,
enforce the E2 key contract or implement a broader portable map mechanism,
and prove native scheduling/quiescence under channels, sync, timers, and GC.
Until those pass, the fork has no ordinary-Go isolate claim.

The fork's full Linux arm64 `src/all.bash` suite passed after these Phase 0
and compiler/runtime probe changes.
