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
  Deterministic; the Temporal adapter supplies both. Real OS timers without
  that clock are outside the deterministic workflow contract.
- Host events are delivered in history order while an isolate is suspended.
  A runtime suspension barrier parks host control until the token and runnable
  queue are idle, then fences dispatch until the host resumes the instance.
  The SDK processes every emitted command and repeats resume/suspend until
  transport handshakes finish and no additional command is produced.
- `sync.Map.Range` and `iter.Pull`/`iter.Pull2` fail closed in deterministic
  mode. Their hash-trie iteration and direct coroutine switches need separate
  implementations. Ordinary host calls remain available.
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
