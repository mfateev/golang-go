# Native isolate determinism

Requested implementation order: `select`, map iteration, goroutine dispatch.
This extends the trusted POC and keeps ordinary host Go behavior unchanged.
`isolate.Config.Deterministic` enables the mode; the Temporal adapter opts in.
Legacy runtime probes can retain ordinary dispatch for their revocation tests.

## Contracts

- Native and reflected `select` use an isolate-local, reproducible shuffle of
  ready-case polling order. Every instance starts the same sequence. It is
  independent of process random state, OS threads, and isolate IDs. Deterministic
  readiness still requires the dispatch phase below.
- Native map range and reflection iterate an ascending snapshot of integer or
  string keys. Named types are supported. Deleted keys are skipped and values
  are read when visited; keys inserted after iteration starts are excluded.
  Pointer, uintptr, interface, boolean, floating point, complex, and composite key iteration is
  rejected inside isolates, including empty maps. Hashing stays unchanged:
  sorting removes the architecture-dependent table layout from observed order.
- Each isolate has one execution token and a FIFO runnable queue. A parent
  continues after `go`; children enter the queue in creation order. Blocking
  channel, select, and synchronization operations, explicit `runtime.Gosched`,
  and goroutine exit transfer the token. Ordinary preemption and GC suspension
  retain it, so elapsed wall time cannot choose another isolate goroutine.
  Different isolates and host goroutines can run concurrently.
- Workflow time requires Config.InitialTime and TimerOp in addition to
  Deterministic; the Temporal adapter supplies both. Clock reads and sleeps
  without a host clock fail closed in deterministic mode. Now returns UTC,
  and the default Local location resolves to UTC without reading TZ or the
  machine's timezone database. LoadLocation accepts UTC/Local names and rejects
  database lookups; explicit FixedZone and LoadLocationFromTZData values remain
  available.
- Host events are delivered in history order while an isolate is suspended.
  A runtime suspension barrier parks host control until the token and runnable
  queue are idle, then fences dispatch until the host resumes the instance.
  The SDK processes every emitted command and repeats resume/suspend until
  transport handshakes finish and no additional command is produced.
- `sync.Map.Range` snapshots and sorts keys of one concrete integer or string
  type, including named types, then loads current values as native range does.
  Mixed key types and other kinds fail before any callback runs. Empty maps
  remain valid. The host retains ordinary hash-trie iteration.
- `iter.Pull`/`iter.Pull2` use channel handshakes through the isolate dispatcher.
  Their goroutines inherit ownership, yield through FIFO dispatch, participate
  in suspension, and can be discarded during revocation. Ordinary host
  iterators retain Go's direct coroutine switches.
- Top-level `math/rand` uses an isolate-owned Go 1 generator seeded with 1,
  including byte-read remainder. Top-level Seed is a no-op independently of
  host GODEBUG. `math/rand/v2` uses its own SplitMix64 stream starting from
  sequence zero. Neither stream consumes select or runtime hashing entropy;
  suspension preserves state. Explicit seeded generators retain their Go API.
  These fixed default streams are for replay, never security or unique IDs.
- `sync.Pool` behaves as empty inside isolates: Put drops values and Get uses
  New or returns nil. GC cycles and P assignment cannot choose cached values.
- Package initialization uses the same dispatch rules. Unsupported process I/O,
  CPU-only infinite loops, mutable host sharing, and unreviewed library effects
  remain outside the trusted POC contract; this is not general containment.

## Implementation and gates

1. **Select:** add group-local random state and route native select polling
   through it; verify mixed send/receive cases, nil/default/closed channels,
   reflection, repeatability, and ordinary host behavior.
2. **Maps:** extend the shared map iterator and its compiler layout; copy and
   sort supported keys, look up current values during iteration, and reject
   unsupported key kinds. Verify growth, deletion, update, insertion, clear,
   reflection reset, and large maps.
3. **Dispatch:** intercept runnable publication, implement group FIFO/token
   handoff, retain tokens across runtime-internal waits and preemption, and
   integrate group entry/exit and revocation. Verify channel/sync fan-out,
   explicit yields, GC, different GOMAXPROCS settings, and instance independence.
4. **Suspension and SDK:** expose host suspend/resume operations and replace
   the serial one-command assumption. Validate concurrent activity/timer/signal
   delivery and fresh-process replay of SleepForDays.
5. **Record results:** update status documents, run focused runtime/compiler
   regression gates and SDK/sample acceptance tests, commit and push the repos.
   Broader productization still needs cross-architecture CI, complete effect
   enforcement, deterministic entropy APIs, and production resource limits.

## Progress

Implemented on 2026-10-05. The mode is opt-in for runtime probes and enabled
by the Temporal adapter.

- Toolchain bootstrap passed with the extended map iterator ABI.
- Native/reflected select and canonical map tests passed; map/reflect/sync and
  isolate regression suites passed.
- Dispatcher tests compare full traces across GOMAXPROCS 1, 2, and 8. The main
  stress case runs 48 goroutines for 64 rounds per instance with concurrent
  isolates, background host GC, allocations, select, maps, Cond barriers, and
  explicit yields. A separate case forces contended Mutex/RWMutex parks and
  unbuffered channel handoffs. Repeated runs and the race detector passed.
- Suspend/reply batching and revocation while paused or awaiting suspension
  passed. The SDK driver checks two concurrent activities and a native timer,
  ordered history replies, stable results, and deadlock reporting.
- Live concurrent activity/timer execution and SleepForDays completion via a
  signal passed against Temporal CLI 1.9.1 on Linux arm64. Both histories
  replayed in fresh processes at GOMAXPROCS 1, 2, and 8. Ordinary PlainEcho
  completed on the same worker. The new GreetAll sample also completed and
  replayed at all three settings, comparing greetings against history. The sample's 30-day timer repetition and
  pending/failed email independence are covered by its direct-host tests.
- Stress exposed GC worker startup's internal channel wait; it now retains the
  token. Broader runtime tests exposed a stale tail link in scheduler batches;
  individual filtering now preserves Go's original tail-clearing invariant.
- Final full toolchain rebuild, marked-function/legacy build scripts,
  focused runtime regressions (including goroutine leak profiles), and static
  lock-ranking checks passed. SDK tests and all three sample behavior drivers
  passed. The dispatcher suite passed five repeated race runs.
- Native cross-architecture replay and the remaining productization gates have
  not been completed. Deterministic code must still follow Go synchronization
  rules for shared values; the race detector's memory model is unchanged.

## Productization item 1

The follow-up adds the random streams, UTC policy, supported sync.Map range,
dispatcher-aware iterators, and synchronous timer behavior described above.
Non-positive channel timers are immediately ready; Stop and Reset undo pending
delivery, and native/reflected channel length and capacity remain zero.
Native Timer.Stop/Reset still do not cancel an already issued host timer; that
Temporal integration work is tracked separately.

The SDK's `example/determinism` records one workflow's full map, select,
goroutine, iterator, random, floating-point distribution, cancellation, and
clock observations in its activity input and result. On replay the workflow
compares its freshly computed trace with the recorded activity result, and the
checker compares completion with history. This avoids relying on the Temporal
SDK to compare activity input payloads. A negative test changes both recorded
results and verifies rejection. The original saved history is retained.
`.github/workflows/isolate-determinism.yml` runs the same corpus on native
Linux and macOS arm64/amd64, with normal and disabled optional CPU features,
plus runtime/compiler, SDK/sample, and repeated race gates. Logs are preserved
for every run. A platform is verified only after its native job passes.

Item 2 (memory ownership) must not begin until item 1's local and native gates
pass and all changes are checked in. The CI and history corpus establish replay
compatibility for the covered operations; future runtime algorithm changes
must pass old histories or introduce an explicit compatibility/version policy.

### Local validation, 2026-10-05

- `src/all.bash` completed with `ALL TESTS PASSED`, including standard-library
  and compiler tests, tagged/experimental configurations, cgo, runtime processor
  configurations, the race detector, and the language regression corpus.
- After the final named-zone lookup guard, `go test -short time isolate go/build`
  passed, including UTC/Local lookup, rejection of machine database lookups,
  and explicit TZif data decoding.
- `go test -race isolate -run TestDeterministic -count=5`, SDK `go test ./...`,
  and the serial/concurrent SDK driver passed. The SDK suite includes the
  positive saved-history replay and corrupted-result rejection test.
- The saved 195-observation history replayed in fresh processes at GOMAXPROCS
  1, 2, and 8, with optional CPU features enabled/disabled and host TZ set to
  America/Los_Angeles. The race-instrumented checker also passed. All returned
  SHA-256 `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.
- Every tracked sample package passed. The local wildcard samples command
  still reports a vet error in an unrelated, untracked root `main.go`; that
  user file is excluded from commits. Native CI runs the full samples command
  from clean branch checkouts.
- Native Linux/macOS arm64/amd64 jobs remain pending. Item 2 has not started.
