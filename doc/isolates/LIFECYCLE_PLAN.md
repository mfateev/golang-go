# Reliable lifecycle: productization feature 5

Status: complete for the agreed scope (2026-10-07). Automatic worker-shutdown
integration is deferred at the user's request; current SDK behavior is retained.
Features 1, 2 and 4 are complete. Feature 3, frozen-heap GC, remains deferred.

## Contract

- A normal entry return terminates its children. `Done` and `Wait` publish the
  outcome only after every attached goroutine has detached and waiter cleanup
  has finished. `Kill` returning nil establishes the same permanent fence.
- Ordinary recovered panics and child `runtime.Goexit` retain Go semantics.
  Unrecovered child panics terminate the isolate, never the worker process.
  Root panic/Goexit and initializer failures produce defined host diagnostics.
- Workflow context cancellation remains cooperative. Eviction, exit, failure
  and workflow-definition close revoke execution and wake supported
  synchronization and copied-byte Call waits. Revocation does not run application
  defers.
- Runtime/process service cleanup finishes before a revoked goroutine is
  discarded. Live counts are released after queue records are detached.
- A deadline cannot undo revocation. Pending termination reports remaining
  goroutines and a bounded best-effort stack/thread sample. CPU loops without
  a supported execution fence and uninterruptible native execution may remain
  pending; this feature does not promise an in-process hard kill.
- Startup can be bounded with `NewContext`. Failed startup revokes its children.
  If cleanup remains pending, an initialization error exposes the cause,
  diagnostics and a cleanup notification without retaining the instance heap.
- SDK completion and eviction retire callbacks and copied command state.
  Late history callbacks cannot resume a closed instance or reply twice.
  Eviction must not issue server-side activity/timer cancellation commands.

## Implementation sequence

1. Add a runtime group-drain notification and remove lifecycle millisecond
   polling. Publish terminal outcomes after the whole group cleanup fence.
2. Contain unrecovered child panics and report bounded copied diagnostics.
   Preserve recovery, clean panic bookkeeping during hard discard, and keep
   host panic behavior unchanged.
3. Bound startup, establish membership before cancellation can observe an empty
   group, and expose pending cleanup without orphaning initializer children.
4. Finish wakeup/cleanup auditing for supported waits and completion-versus-kill
   races. Release private package state when terminal cleanup finishes.
5. Retire SDK callback state on close, reject commands after completion, report
   pending termination, and propagate isolate panics as Workflow Task failures.

## Acceptance gates

- Runtime lifecycle tests cover main/child/initializer failures, recovered
  panics, exit, startup cancellation, late admission, repeated concurrent kill,
  suspended eviction, timers, Calls, channels/select, locks/Cond/WaitGroup and
  iterators. Pending cleanup can subsequently finish without executing user
  defers or code beyond a revoked wait.
- Race and static lock ranking stress prove no late execution after successful
  termination, no queue records pointing to discarded stacks and no loss of the
  diagnostic under simultaneous failure/completion/kill.
- Retained host errors, closed SDK definitions and late callbacks do not retain
  private instance state after cleanup. Cached eviction reclamation remains green.
- Compiler integration exercises real marked functions and private package state,
  not only privileged runtime fixtures. Ordinary host workflows retain behavior.
- SDK/sample tests, recorded history replay and live-server cancellation/failure
  history checks pass with expected Workflow Task/Execution outcomes.
- Full `src/all.bash` and all four native Linux/macOS arm64/amd64 CI jobs pass.
  All output is captured, with exact toolchain/SDK/sample revisions recorded.


## Implementation checkpoint

The runtime now notifies a process cleanup goroutine when the revoked group
reaches zero. The notification merges member race clocks, and group membership
is announced before deterministic token acquisition so a busy initializer child
cannot prevent the host from calling Kill. Cleanup detaches private entry/runner
references and the allocator lifetime; GC can retire the cache while a completed
host handle remains retained.

Panic reporting runs after ordinary recovery has had its opportunity, copies a
bounded primitive message or dynamic type name, and releases outstanding panic
bookkeeping during discard. Runtime tests exercise ordinary, nested and metadata
failure unwind. Isolate tests include retained private panic payloads, retained
handles/cache retirement, concurrent Kill/late replies, pending initialization,
and unstarted/parked iterators in both scheduling modes.

The SDK retires command cells instead of retaining commands directly through
history callbacks. Completion waits for native cleanup before publishing an
execution result; lifecycle failures and task deadlines follow BlockWorkflow.
The compiled lifecycle checker covers root/child panic, exit, Goexit, recovery,
leftover children, cooperative context cancellation and ordinary returned errors,
with saved-history replay and optional real-server event assertions.

Call reserves the poll draws for both transport selects before parking. A
delayed host receipt therefore cannot perturb another workflow goroutine's
select stream. A regression compares immediate and delayed receipt; the original
195-observation history retains its recorded hash. Dispatcher admission also
counts pending joins so Start never publishes a false idle interval.

The implemented runtime and definition paths passed their acceptance gates.
Feature 5 is complete for the agreed scope. Automatic worker-shutdown integration
is deferred as described below.

Local acceptance passed on 2026-10-07: full `src/all.bash`, repeated lifecycle
race/static-lock-ranking stress, all eight compiler integration scripts, the
complete SDK suite, tracked samples, the standalone bridge driver, and real
Temporal server event assertions. Six fresh-process replay variants (GOMAXPROCS
1/2/8, default/disabled CPU features) match the saved 195-observation result:
`12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`.

Native validation exposed an outdated `runtime.g` size fixture and inconsistent
SDK treatment of ownership violations depending on which terminal notification
arrived first. The size fixture now covers the added lifecycle fields on 32-bit
and 64-bit targets. Both SDK paths now report ownership violations as Workflow
Task failures and retain their diagnostic stack; the driver and lifecycle
regression test require this outcome.

## Accepted implementation evidence (2026-10-07)

The [native acceptance run](https://github.com/mfateev/golang-go/actions/runs/37586840637)
passed all four jobs: Linux/macOS arm64/amd64. Every artifact contains these exact
source revisions:

- Toolchain: `fa0808fdca6df6a52c3c59018b72de97814f423b`
- SDK POC: `aa3bc479fabdd11a9ecd96061d2d831e129f3760`
- Samples: `a2f364746f75cb42dd0381c46597b5e8c782b94d`

Each platform passed full short runtime/library conformance, lifecycle and
ownership/determinism race stress repeated five times, static lock ranking repeated
five times, all eight isolate compiler scripts, the complete SDK and sample suites,
the standalone bridge driver, its race/ownership build, and its ownership build at
GOMAXPROCS 1/2/8 with GOGC=1. All 24 native replay variants match the unchanged
195-observation hash above. Complete logs are attached to the run and were also
inspected locally under `/tmp/feature5-native-accepted`.

The full local `src/all.bash` passed (`/tmp/feature5-all-bash-final.log`). Final SDK,
sample and standalone driver output is captured in `/tmp/feature5-sdk-final.log`,
`/tmp/feature5-samples-final.log`, and `/tmp/feature5-driver-final.log`. Live Temporal
checks against the local development server verified execution completion,
cooperative cancellation, ordinary returned application errors, and root/child
panic, Goexit and exit as task failures (`/tmp/feature5-lifecycle-final-live.log`).
The checker terminated its own negative-test executions; the development server
was stopped after verification.

## Deferred worker-shutdown integration

The pinned Temporal Go SDK shares its sticky workflow cache across workers.
`Worker.Stop` stops pollers and task processors but does not evict cached workflow
definitions. Its public process-wide purge is valid only after all workers stop.
Definition `Close` is implemented and tested. Automatic cleanup on worker stop
remains future work and must reach it safely under the SDK's workflow-context lock.

On 2026-10-07 the user chose to retain the current behavior and defer this work.
A future integration could add a per-worker cache-eviction hook to the Go SDK or
provide explicit process-wide cleanup after all workers stop. Before advertising
automatic worker-shutdown cleanup, test cached workflows, late events and another
active worker. This deferred work does not block the accepted feature 5 scope.
