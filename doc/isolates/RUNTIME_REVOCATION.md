# Runtime revocation audit

This is the implementation checklist for the trusted MVP's whole-isolate
`Kill(ctx)`. The provisional host method fences admission and stops `Call`
waits immediately, starts the runtime waiter scan on a process goroutine,
then waits for the runtime group's live count. Its context can expire while
the scan waits for a runtime lock. Unhandled waits can resume while Kill is
pending.
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

Synchronization objects used by an isolate, including ordinary channels,
`sync.Cond`, and its associated locker, belong to that isolate alone. No
other isolate or host goroutine may use them. A `Cond`'s ticket counters and
notification queue are embedded in the `Cond`, so they have the same owner.
The `sudog` records in
that queue still belong to runtime machinery and must be detached before the
isolate's stacks or memory can be reclaimed. The hashed semaphore roots are
process-owned and may contain waiters for different owners; teardown removes
only the target isolate's records under the root lock. `Call` uses separate
process-owned command records and must remain independently wakeable.

The exported `runtime.Gosched` path checks revocation before and after its
yield, discarding a revoked yielding G without user defers. Runtime-internal
yields use an unchecked helper in paths that cannot terminate a G. This does
not constitute the general scheduler execution fence required above.

## Wait records to handle

| Wait | Runtime record | Cleanup currently done by the resumed G |
|---|---|---|
| Channel send/receive | One `sudog` in `hchan.sendq` or `recvq`, also on `gp.waiting` | A blocking operation registers its channel with the group before taking the channel lock. Revocation removes a queued `sudog` under that lock and wakes the G; a peer that already dequeued it remains responsible for the wake. The resumed G unregisters, clears its wait state, and releases the `sudog` before hard discard without user defers. Multi-case `select` uses its own wake token path. |
| Nil channel send/receive or an empty/all-nil `select` | No channel, timer, or other external wait record | The G registers a permanent park with its group before parking. Revocation cancels an uncommitted park or wakes a committed one; the G unregisters and is discarded without user defers. |
| `select` | One `sudog` per case, linked through `gp.waiting` and several channel queues | The G registers before taking channel locks. Revocation claims `selectDone` against peer wakes, then either cancels an uncommitted park or readies the parked G. The resumed G locks all cases, removes every queue record, updates timer wait counts, and is discarded without user defers. |
| `sync.Mutex`, `RWMutex`, `WaitGroup`, and related semaphores | `sudog` in a process-owned hashed `semaRoot` queue, keyed by an isolate-owned semaphore address | A blocking sync wait registers its semaphore address with the group before taking the root lock. Revocation unlinks its `sudog` under that lock, including a non-head waitlink entry, and wakes the G; a concurrent semaphore release that already dequeued it owns the wake. The resumed G unregisters, releases its `sudog`, and is discarded without Go defers. Immediate teardown for the synctest WaitGroup path and generic process-owned semaphores remains open. |
| `sync.Cond` | Ticketed `sudog` in the isolate-owned `notifyList`, also in `gp.waiting` | A wait registers its list with the group before taking the list lock. Revocation removes a queued record under that lock and wakes the G; a concurrent `Signal` or `Broadcast` that already removed it owns the wake. The resumed G unregisters, releases its `sudog`, and is discarded without Go defers before `Cond.Wait` can reacquire its locker. A prequeue wait sees revocation and does not park. |
| `time.Sleep` and timer channels | Per-G timer or channel timer linked into runtime timer machinery | Real `time.Sleep` waits register with the group. Revocation stops pending timers and wakes their Gs; if a callback has started, it performs the wake. The resumed G unregisters before hard discard without user defers. Fake synctest timers still wait for their normal wakeup. Direct and selected timer-channel receives use channel/select cleanup. |
| Network poll | `pollDesc.rg` or `wg` and deadline timers | `poll_runtime_pollReset` checks before preparing I/O. A normal poll wait registers its descriptor with the isolate group after entering `pdWait`. Revocation clears registered poll semaphores and readies parked Gs; each G then completes normal cleanup and exits at the post-wait fence. This does not yet discard a waiter without scheduling it. `poll_runtime_pollWaitCanceled` remains separate. |
| Current `isolate.Call` | Channel send and reply receive on the provisional bridge | The trusted bridge selects each wait against a stop channel and discards the caller without user defers. Runtime `select` cleanup uses the same discard path after cleaning its queue records. `Stop` publishes the group admission fence, closes the stop channel, then scans runtime waiters; `Kill(ctx)` runs that scan on a process goroutine so its caller can observe the deadline. Call does not wait for the scan to finish before waking. A late host reply uses a buffered channel. The bridge is not yet a native owned command queue. |

The table is an initial inventory, not a complete scheduler proof. Runtime
coroutines switch Gs without `execute`/`dropg`; group accounting covers those
switches, but revocation still needs an admission check there. GC assist may
temporarily change G status while the same G continues on its M, so G status
alone is not an execution fence.

Mutex and `sync.Cond` waiters need more than a generic post-wakeup
`Goexit` check. A mutex semaphore may have already transferred lock ownership
to the waking G. Conversely, `sync.Cond.Wait` releases the caller's lock
before parking and reacquires it only after `notifyListWait` returns; exiting
inside `notifyListWait` can make a caller's deferred unlock fail. Since these
objects and their users are confined to the revoked isolate, teardown can
discard their Go-level lock state and skip user defers. The `Cond` path now
detaches its queue record, briefly resumes the G for runtime cleanup, and
discards it without running defers. A final scheduler path that destroys a
parked G without resuming it remains open. The sync semaphore path now uses
the same brief runtime cleanup before discard as the `Cond` path.

## Acceptance cases

- A revoked, never-started child executes no user instruction; the existing
  tagged first-dispatch test covers this narrow case.
- A previously parked G cannot resume isolate code after revocation, even if
  channel send, `select`, timer, netpoll, or semaphore wakeup races with it.
- Detaching a non-head semaphore waiter leaves waiters for other owners in
  the process-owned root working.
- Revoking a `sync.Cond` removes all its queued waiter records; a racing
  `Signal` or `Broadcast` cannot retain or ready a destroyed G.
- A G whose wait has been signaled but whose Go cleanup has not run does not
  leave a `sudog`, timer count, or stack pointer in a process queue.
- `Kill(ctx)` returns pending while any G remains executing or in a syscall;
  a later nil result is stable against every scheduler entry path.

The `phase0_e5a_acceptance` tagged tests contain non-head mutex and multiple
`sync.Cond` scenarios from an earlier per-goroutine kill probe. They must be
adapted to whole-isolate revocation and owner separation before use as a gate
for the current trusted POC.
