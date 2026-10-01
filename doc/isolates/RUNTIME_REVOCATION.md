# Runtime revocation audit

This is the implementation checklist for the trusted MVP's whole-isolate
`Kill(ctx)`. The provisional host method stops `Call` waits and waits for the
runtime group's live count; unrelated waits can resume while it is pending.
The group has a first-dispatch admission bit, a live goroutine count, a
conservative runnable count, a count of goroutines associated with an M, and
a provisional registry of network poll waits.
None is yet a safe teardown condition. See [ISOLATE_API.md](./ISOLATE_API.md)
for the requested host contract and
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md) for the implemented probes.

## Required order

1. Atomically revoke admission and publish a durable fence checked before
   every later entry into isolate code, including goroutines that previously
   ran and parked. The current `isolateFirstDispatchRevoked` check covers only
   children that have never run. It cannot exclude a previously admitted G
   from resuming after revocation.
2. Stop currently executing goroutines at runtime-controlled points and keep
   `Kill(ctx)` pending while any still executes. A syscall remains associated
   with its M in the current diagnostic counter; a delayed syscall return
   cannot be treated as stopped. The request remains revoked if `ctx` expires.
3. Detach each parked goroutine's wait registration under its owner's
   synchronization before destroying the G or releasing its stack. If a wakeup
   already won, finish or replace that wait's post-wakeup cleanup exactly once.
4. Retain the G stack and any referenced heap until all scheduler, timer, and
   I/O records that may point at it have been detached. Only then can later
   memory reclamation proceed. A nil `Kill` result requires the execution
   fence and absence of executing isolate code; it does not require heap
   reclamation to have finished.

The owner ID and group pointer in `runtime.g` identify the target. The group
must also own or enumerate every registration created on its behalf. A scan of
G status alone cannot find the queue node, lock, or timer that must be
unlinked.

The exported `runtime.Gosched` path checks revocation before and after its
yield, allowing a yielding loop to exit. Runtime-internal yields use an
unchecked helper in paths that cannot run `Goexit`; this does not constitute
the general scheduler execution fence required above.

## Wait records to handle

| Wait | Runtime record | Cleanup currently done by the resumed G |
|---|---|---|
| Channel send/receive | One `sudog` in `hchan.sendq` or `recvq`, also on `gp.waiting` | `chan.go` clears `gp.waiting`, `activeStackChans`, and `gp.param`, then releases the `sudog`. A post-cleanup revocation check now exits a woken G before user code. Kill does not yet remove or wake the queued waiter early. |
| `select` | One `sudog` per case, linked through `gp.waiting` and several channel queues | `select.go` locks the cases, removes losing entries, updates channel timer wait counts, clears stack element pointers, and releases all records. A post-cleanup revocation check now exits a woken G before it returns to user code. Kill does not yet remove or wake its queued cases early. |
| `sync.Mutex`, `WaitGroup`, and related semaphores | `sudog` in a hashed `semaRoot` queue | `sema.go` releases the record after wakeup; non-head queue removal must preserve other waiters. `WaitGroup.Wait` now checks revocation on entry and after semaphore cleanup, race-state restoration, and the reuse check. It still needs an ordinary `Done` to wake a parked waiter. Mutex and other semaphore users remain open. |
| `sync.Cond` | Ticketed `sudog` in `notifyList`, also in `gp.waiting` | `sema.go` clears the G waiting pointer and releases the record. Removing an earlier ticket must preserve later `Signal` behavior. |
| `time.Sleep` and timer channels | Per-G timer or channel timer linked into runtime timer machinery | `time.Sleep` now checks revocation after its normal wakeup and exits before user code; it is not woken early. Timer channels still need wait detachment and post-wakeup checks. |
| Network poll | `pollDesc.rg` or `wg` and deadline timers | `poll_runtime_pollReset` checks before preparing I/O. A normal poll wait registers its descriptor with the isolate group after entering `pdWait`. Revocation clears registered poll semaphores and readies parked Gs; each G then completes normal cleanup and exits at the post-wait fence. This does not yet discard a waiter without scheduling it. `poll_runtime_pollWaitCanceled` remains separate. |
| Current `isolate.Call` | Channel send and reply receive on the provisional bridge | The trusted bridge now selects each wait against a stop channel and exits the waiting G with `Goexit`. A late host reply uses a buffered channel. This handles the two bridge waits but does not detach arbitrary runtime channel waiters; the bridge is not yet a native owned command queue. |

The table is an initial inventory, not a complete scheduler proof. Runtime
coroutines switch Gs without `execute`/`dropg`; group accounting covers those
switches, but revocation still needs an admission check there. GC assist may
temporarily change G status while the same G continues on its M, so G status
alone is not an execution fence.

Mutex and `sync.Cond` waiters need more than a generic post-wakeup
`Goexit` check. A mutex semaphore may have already transferred lock ownership
to the waking G. Conversely, `sync.Cond.Wait` releases the caller's lock
before parking and reacquires it only after `notifyListWait` returns; exiting
inside `notifyListWait` can make a caller's deferred unlock fail. Their
revocation paths must preserve those lock obligations.

## Acceptance cases

- A revoked, never-started child executes no user instruction; the existing
  tagged first-dispatch test covers this narrow case.
- A previously parked G cannot resume isolate code after revocation, even if
  channel send, `select`, timer, netpoll, or semaphore wakeup races with it.
- Detaching a non-head mutex waiter leaves the other waiters working.
- Removing an earlier `sync.Cond` ticket does not consume a later waiter's
  `Signal`; repeated waits after removal still work.
- A G whose wait has been signaled but whose Go cleanup has not run does not
  leave a `sudog`, timer count, or stack pointer in a process queue.
- `Kill(ctx)` returns pending while any G remains executing or in a syscall;
  a later nil result is stable against every scheduler entry path.

The `phase0_e5a_acceptance` tagged tests already contain non-head mutex and
multiple `sync.Cond` scenarios. Some deliberately fail for the old hard-kill
probe, so they are specifications to adapt to group revocation, not a gate for
the current trusted POC.
