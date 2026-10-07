# Reliable lifecycle: productization feature 5

Status: implementation and acceptance validation in progress (2026-10-07).
Features 1, 2 and 4 are complete. Feature 3, frozen-heap GC, remains deferred.

## Contract

- A normal entry return terminates its children. `Done` and `Wait` publish the
  outcome only after every attached goroutine has detached and waiter cleanup
  has finished. `Kill` returning nil establishes the same permanent fence.
- Ordinary recovered panics and child `runtime.Goexit` retain Go semantics.
  Unrecovered child panics terminate the isolate, never the worker process.
  Root panic/Goexit and initializer failures produce defined host diagnostics.
- Workflow context cancellation remains cooperative. Eviction, exit, failure
  and worker shutdown revoke execution and wake supported synchronization and
  copied-byte Call waits. Revocation does not run application defers.
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

Validation remains in progress. No completion claim is made until the full Go,
SDK/sample, stress, service-history and four-platform gates above are green.
