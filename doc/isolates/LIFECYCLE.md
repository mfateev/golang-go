# Instance lifecycle and cleanup

## States and initialization

```text
registered program -> NewContext/preparing -> prepared -> Start/running
                                                   -> suspended <-> running
any live state -> terminal cause + permanent revocation -> draining -> Done
```

`NewContext` validates the program/context, configures limits, randomness, clock
and logging, then runs its state factory in a managed initializer goroutine.
Membership is visible before waiting for a deterministic token so a busy child
cannot make the host mistake startup for an empty group. Package layouts and
initializer allocations belong to the instance. Initialization may log but
cannot wait for application host calls before `Start`.

`New` uses an unbounded initialization context. A canceled/failed initialization
revokes children and attempts cleanup for up to 100 ms. If it remains pending,
`InitializationError` contains a copied cause, `KillPendingError` diagnostics and
`Done` notification. Returning an error alone does not prove cleanup completed.
The notification and retained error must not retain the completed private heap.

`Start` is single-use. In deterministic mode, suspension waits for quiescence and
fences admission; it is not terminal. A root return or terminal fault revokes
children. The runtime keeps terminal-cause publication separate from the final
cleanup notification so no success can be reported while members remain live.

## Outcomes

| Trigger | Result |
| --- | --- |
| Normal entry return | Revoke/drain remaining children, then successful `Wait` |
| Recovered application panic | Continue with ordinary Go semantics |
| Unrecovered entry/child/initializer panic | Copied `PanicError`; terminate the instance |
| Root `runtime.Goexit` | `GoexitError`; child Goexit remains normal Go behavior |
| Exit status zero/nonzero | Successful termination / `ExitError` |
| Ownership/effect/resource violation | First permanent typed fault; no application recovery/defers |
| Host `Kill` before another outcome | `ErrRevoked` after cleanup |
| Expired kill deadline | `KillPendingError`; revocation remains in force |

`recoverablePanic` and range-function token recovery both respect permanent
faults. Panic diagnostics copy primitive messages or type names without invoking
arbitrary application `Error`/`String` callbacks. Discard balances panic/race
bookkeeping and clears print/transport/owner state before goroutine reuse.

## Revocation algorithm

1. Atomically mark the group revoked; refuse new application admission.
2. Close transport stop notifications and wake supported user parks independently
   of native synchronization-object teardown.
3. Remove parked/queued wait records from channel, select, semaphore, Cond,
   WaitGroup and iterator machinery, respecting existing runtime lock order.
4. Let trusted runtime/metadata services finish required cleanup and release
   process-owned locks before discarding their member.
5. On the last member detach, merge race clocks and notify the host cleanup
   worker. Live counts are released only after stack/waiter references detach.
6. Clear entry/state/transport roots and release both allocation-cache lifetime
   handles. Publish `Done` and the stable `Wait` outcome.

Native synchronization objects belong to their isolate and can be abandoned
with their private state. Process-owned scheduler/GC/metadata locks cannot.
Do not run application defers to unlock private objects during revocation; their
callbacks can execute arbitrary workflow code. Late wakeups and history replies
must observe revocation instead of dispatching code or replying twice.

## Host fences and diagnostics

`Done` closes after all attached cleanup, and `Wait` waits for it. A nil `Kill`
establishes the same permanent no-later-execution fence. If a context expires,
the request stays effective and another `Kill` can wait again. Samples include
remaining live/running goroutines and best-effort stack/thread identity. Running
includes syscalls; diagnostics do not forcibly suspend a busy member.

Uninterrupted computation and native code may not reach a supported discard
boundary. These cases have no guaranteed hard-kill latency. `StackTrace` captures
application stacks; `TaskDiagnostics` also captures SDK services and suspension
state. Capture task diagnostics before kill removes the state explaining a stall.

## SDK lifetime

A Temporal definition owns its instance and host callback cells. Callbacks hold
retireable cells; close clears command/payload references and marks them done.
Callbacks after close are inert. Eviction does not emit server-side activity or
timer cancellations. Workflow context cancellation is cooperative and distinct
from definition revocation. A returned application error is an execution result;
a native permanent fault follows the Workflow Task failure path.

Completed workflows with queries retain frozen application state and admit only
read-only services until eviction. Ordinary completion without retained queries
waits for cleanup before publishing the host result. Automatic worker-stop cache
teardown is not added by this POC; the supported teardown paths are definition
completion, failure, close and cache eviction.

Source: `src/isolate/host.go`, `src/runtime/isolate_{lifecycle,revocation,park,fault}.go`,
`src/runtime/proc.go`, `src/runtime/panic.go`, `src/internal/isolatebridge/bridge.go`,
and SDK `temporalbridge/bridge.go`.
