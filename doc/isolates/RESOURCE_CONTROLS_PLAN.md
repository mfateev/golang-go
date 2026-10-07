# Resource controls: productization feature 6

Status: implementation in progress (2026-10-07).

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
