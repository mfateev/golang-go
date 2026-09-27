# Phase 0 results and open gates

Recorded 2026-09-27 against Go tree `2ff5743d9f` on Linux arm64.
These are feasibility observations, not a Phase 0 exit decision.
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

Initial source-path inventory for the sample's indirect operations:

| User operation | Current path in this tree | Phase 1 classification |
|---|---|---|
| `reflect.Select` | `reflect/value.go` `rselect` → `runtime/select.go` `reflect_rselect` | Untracked; outside contract |
| `sync.Cond.Wait` | `sync/cond.go` → `runtime_notifyListWait` | Untracked; outside contract |
| `context.WithDeadline` | `context/context.go` → `time.AfterFunc` | Untracked; outside contract |
| `time.AfterFunc` | `time/sleep.go` → runtime timer; callback starts native goroutine | Untracked; outside contract |

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

## E4: initialized globals, known failure

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
This is not compiler-generated indirection, a workflow-shaped benchmark, or
an initialized-graph isolation proof. Those remain open.

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

## Gates still open

- E0 if the `-toolexec` path remains a candidate: dependency discovery,
  cache identity, and linked package coverage.
- E1 ordinary-Go execution and standard-library path coverage.
- E2 portable hash and map-iterator semantics/cost, including cross-machine
  replay and reflected iteration.
- E3 real owned-isolate memory and GC measurements, median/p99 across
  representative workflows, and a controlled allocation-throughput result.
- E4 initialized graph isolation and global access cost.
- E5a, future: arm64 forceful-kill proof for loops, waits, defers, and lock
  holders, with whole-isolate safe teardown and a confirmed bound. E5b remains
  conditional on density/relocation.

Do not claim the final runtime isolate model or start Phase 2B from these
partial results.
