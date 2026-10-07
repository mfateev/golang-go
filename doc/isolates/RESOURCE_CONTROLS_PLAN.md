# Resource controls: productization feature 6

Status: complete for the agreed scope (2026-10-07).

## Scope and decisions

- Host-only accounting covers owned allocation slots, attached goroutine stacks,
  attributable runtime metadata, full owned span capacity, live/peak goroutines,
  scheduled execution intervals and progress. Shared process services and RSS
  remain separate measurements. Workflow code cannot inspect or configure usage.
- Zero limits mean unlimited. Memory and goroutine limits are installed before
  initialization. Both deterministic and concurrent isolates enforce them.
- The memory budget charges allocator slot sizes (including headers and large
  allocation rounding), stacks and attributable metadata. Objects remain charged
  until sweep reclaims them. `ReservedHeapBytes` reports full owned span capacity (including slots); unused
  capacity is not charged again. Shared services, SDK/transport objects and
  process-wide GC infrastructure remain outside this budget. This
  is not an RSS cap. Reserved allocation credit protects against oversized
  allocations before requesting their storage.
- Exceeding a limit permanently revokes the group, publishes copied typed host
  diagnostics and uses the existing whole-group cleanup fence. Application
  recovery and defers cannot resume it. The SDK reports a Workflow Task failure,
  never an application result or a server-side activity/timer cancellation.
- Worker configuration adds a maximum task duration and a no-progress watchdog.
  These use host monotonic time and operate only during an active task; cached
  time does not consume a task budget. Startup uses the task duration deadline.
  Scheduled interval accounting is not exact hardware CPU accounting, and
  uninterrupted loops/native execution may retain pending cleanup.
- Host observers receive copied usage and failure snapshots at lifecycle/task
  boundaries. Defaults preserve existing workers and replay behavior. Worker
  shutdown integration and frozen-heap GC remain deferred.

## Implementation sequence

1. Add accounting records with independent span/cache/host-handle lifetimes;
   collect slot, stack, span capacity, metadata and scheduling statistics.
2. Enforce goroutine admission and memory reservations before initialization and
   execution. Contain failures through immutable native fault records.
3. Expose host limits, usage and typed limit errors. Configure workers/replayers,
   add task/progress watchdogs and lifecycle observers, and preserve task-failure
   semantics under notification races.
4. Add real compiled workflow cases and density/stress tests. Verify cached
   usage, GC refunds, stack growth/shrink, shared services, late wakeups and
   retained handles without leaking accounting records.
5. Capture full Go, race/lock-ranking, compiler, SDK/sample, real-server and all
   four native platform acceptance output. Preserve the saved replay result.

## Acceptance gates

- Counters are exact at quiescence/cleanup; concurrent snapshots are documented
  as best effort. No accounting record retains application pointers or outlives
  its last host/cache/span reference. Reuse and GC cannot refund another owner.
- Limit failures affect only the offending instance, include kind/limit/usage
  and useful diagnostics, and remain failures despite recovery or late callbacks.
- Allocation, goroutine, stack and shared-service tests cover both scheduling
  modes; race/static lock ranking stress protects the runtime hot paths.
- Thousands of cached isolates are measured and reclaimed, including retained
  completed handles. Include a separate 10,000-instance density measurement.
- Recorded history still produces the same 195 observations and hash on native
  Linux/macOS arm64/amd64. All output and exact revisions are recorded.

## Implemented interfaces

- `isolate.Config.ResourceLimits` installs `MaxMemoryBytes` and `MaxGoroutines`
  before initialization. `Isolate.Resources()` reports current host usage;
  `ResourceLimitError` carries the first limit, attempted usage, snapshot and
  stack. Host `FailTaskDuration`/`FailProgress` use the same permanent native fence.
- `worker.SetIsolateResourceOptions` configures workers and replayers. Each new
  definition copies the policy. Existing cached definitions keep their policy;
  ordinary registrations retain the SDK's behavior. The low-level factory has
  `ResolveResourceOptions`, and its definition exposes `Resources()`.
- Native accounting records contain only numbers and code PCs. Separate references
  from the host handle, cache, spans and attached pooled wait records prevent
  dangling counters without making private objects GC roots. Sweep refunds slots;
  span/cache retirement refunds their metadata. Completed handles keep diagnostics
  after private heaps and stacks have been reclaimed.
- Application allocation reserves rounded credit before obtaining storage. A G
  retains its unfinished reservation across stack growth and GC assists; discard
  refunds it if no slot was materialized. Compiler-specialized small allocations
  enter this common path too. Heap profiles preserve their previous frame format.
- Child admission includes iterator runners. Stack growth checks the budget at a
  safe runtime boundary; pinned waits and runtime preparation finish necessary
  cleanup before discard. Such preparation may temporarily exceed a budget while
  holding a lock. This remains a charged-memory control, not a strict RSS ceiling.
- Fault publication allocates no diagnostic string on the native critical path:
  it saves bounded numeric PCs and publishes a preallocated immutable record.
  The host formats the diagnostic. Notification after scheduler detachment uses
  explicit race synchronization with the publishing G.
- Active tasks use host monotonic deadlines and progress sampling. Supported
  parks and explicit yields advance progress; asynchronous preemption does not.
  Startup consumes the task-duration budget. Cached instances have no watchdog
  timers, and cached idle time does not consume either budget.
- Lifecycle observers receive copied identity, replay status, counters and failure
  text. An observer panic disables reentrant observation during cleanup and takes
  the existing Workflow Task panic path. Late SDK callbacks remain retired, and
  quota failures never send activity/timer cancellation commands.

## Local validation checkpoint (2026-10-07)

Initial implementation revisions:

- Go: `1fa11bb07e78aab40aa25323542a8ed32da22ae8`.
- SDK: `4a4d198b41fec36e0d81e1a2f6392ccc9a48b662`.
- Samples: unchanged `a2f364746f75cb42dd0381c46597b5e8c782b94d`.

Local Linux arm64 checks passed:

- Resource accounting, limits, initialization, iterator admission, pinned select
  preparation, interrupted reservations, shared services, immutable watchdog
  faults and retained handles; race and static lock ranking each repeated five
  times alongside lifecycle/allocator coverage.
- Compiled entry-point tests, including selected package-global accounting and
  initializer admission, in both dispatch modes and with race instrumentation.
- The SDK suite, compiled controls with compulsory ownership probes and race,
  all five tracked sample trees, and the existing driver.
- A real Temporal development server: unlimited execution completed; memory,
  goroutine, task-duration and no-progress violations emitted Workflow Task
  failures while leaving executions open. Test executions were then terminated.
- All six local saved-history variants retained 195 observations and SHA-256
  `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.

The density fixture cached 10,000 native instances with 4,096 bytes of application
state each. It charged 123,280,000 bytes total: 40,960,000 heap, 40,960,000 stacks,
and 41,360,000 runtime metadata. Full owned span capacity was 81,920,000 bytes.
Process HeapAlloc grew 64,344,304 bytes and StackInuse grew 41,025,536 bytes. Linux
RSS was 9,297,920 bytes before caching and 150,106,112 bytes while cached. This is
one native fixture measurement, not a Temporal workflow footprint or a production
capacity target. Every private heap, stack, span and live-G charge was reclaimed
while completed handles remained retained; releasing those handles returned
account/cache counts to their baselines.

Captured output is under `/tmp/feature6-*`, including
`density-10000-final.log`, `runtime-race-final.log`,
`runtime-lockranking-final.log`, `compiler-resources-second.log`,
`sdk-all-final.log`, `resources-race.log`, `resources-live.log`,
`samples-final.log`, `samples-helloworld.log`, and `replay-final.log`.
The first full Go run exposed the heap-profile frame regression; its fix passed
three repeated memory-profiler tests. The final full `src/all.bash` passed on
the implementation revision above; its output is
`/tmp/feature6-all-bash-final.log`. Final source and native acceptance are recorded
below.

The first native run passed Linux arm64/amd64 and macOS arm64. macOS Intel
exposed a readiness race in the existing metadata `Cond` revocation test: its
host could observe a GC/stack-growth park and signal before `Cond.Wait`
registered. Test-only revision `e9ec76650cee44290b3d4e3c35c1f397f1f8b3a7`
publishes readiness while holding the notification mutex. It passed 100 race
repetitions with `GOGC=1` and 200 static-lock-ranking repetitions locally. The
That test-only revision preserved the runtime/compiler implementation tested by
full `all.bash`.
Captured outputs: `/tmp/feature6-cond-race-stress.log` and
`/tmp/feature6-cond-lockranking-stress.log`.

The second native run found a compiler ordering bug on Linux arm64 during the
SDK's forbidden protobuf-registry mutation check. Method-table address computation
could be scheduled before an earlier diagnostic call and its own nil check,
leaving `nil+24` live as a pointer while the diagnostic allocated and GC shrank
the stack. The original SDK binary reproduced the failure under GC stress; a
small standalone fixture then reproduced it reliably with explicit stack growth
and GC. The fix derives the address from the checked itab SSA value. A dedicated
compiler script covers nil panic ordering, nonnil dispatch, both ownership-check
levels, and race instrumentation; it is included in every native gate.

Compiler fix revision: `3248d2d05fed8a80d8b671a77a29d8170a9e3c46`.
All ten isolate compiler scripts passed locally, and the corrected real SDK
effects binary passed 100 fresh-process runs with `GOGC=1` and `GOMAXPROCS=8`.
The original binary failed on run 34. Captured outputs are
`/tmp/feature6-niliface-before.log`, `/tmp/feature6-nilinterface-regression.log`,
`/tmp/feature6-compiler-nilinterface-final.log` and
`/tmp/feature6-effects-gc-fixed-stress.log`. This revision was then checked by a
fresh full Go run and native acceptance.

That full Go run passed every gate except two existing SSA devirtualization
expectations in `test/devirt.go`. The checked-itab dependency is now selected only
for functions with isolate heap instrumentation; ordinary builds retain their
existing SSA call shape. Native CI also runs the uninstrumented devirtualization
regression. Both that regression and the GC-ordering script are required before
accepting the final compiler correction and repeated full Go run.

Final compiler correction: `ad5c34b5ff267a5dc87eb2dcc8a640fd5b48fe16`.
Both targeted regressions passed locally; output is
`/tmp/feature6-devirt-final.log` and
`/tmp/feature6-nilinterface-conditional-regression.log`. The repeated full
`src/all.bash` passed at this revision, including all ordinary Go compiler
regressions; output is `/tmp/feature6-all-bash-conditional-final.log`. The build
cache was cleaned after all local Go commands finished.

## Final native acceptance (2026-10-07)

[Native run 37645964456](https://github.com/mfateev/golang-go/actions/runs/37645964456)
passed all four platform jobs:

| Native platform | Result |
|---|---|
| Linux arm64 (`ubuntu-24.04-arm`) | Passed |
| Linux amd64 (`ubuntu-24.04`) | Passed |
| macOS arm64 (`macos-15`) | Passed |
| macOS amd64 (`macos-15-intel`) | Passed |

Every artifact's `revisions.txt` was downloaded and verified against:

- Go: `ad5c34b5ff267a5dc87eb2dcc8a640fd5b48fe16`.
- SDK: `4a4d198b41fec36e0d81e1a2f6392ccc9a48b662`.
- Samples: `a2f364746f75cb42dd0381c46597b5e8c782b94d`.

Each job passed broad runtime/library conformance, CPU-feature-disabled checks,
repeated race and static-lock-ranking stress, ordinary compiler devirtualization,
all ten isolate compiler scripts, the SDK/sample suites, regular/race/compulsory
heap driver variants, and compiled resource controls under race with compulsory
ownership checks. Its six fresh-process replay variants (GOMAXPROCS 1/2/8,
default/disabled CPU features) retained 195 observations and SHA-256
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
All 24 native replay results were verified from the artifacts.

Complete output and exact revisions remain in the run's four artifacts. Local
copies are `/tmp/feature6-native-final-{arm64,amd64,macos-arm64,macos-amd64}/`.
The final source revision also passed full local `src/all.bash`; the SDK/sample,
real-server, 10,000-instance density and GC stress evidence above completes the
agreed resource-control gate. Frozen-heap GC, exact CPU quotas, forceful stopping
of uninterrupted code and automatic worker-shutdown integration remain outside
this feature's scope.
