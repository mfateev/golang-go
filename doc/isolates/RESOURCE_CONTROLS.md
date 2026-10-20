# Resource accounting and enforcement

Limits are host configuration, never workflow inputs. `Config.ResourceLimits`
installs `MaxMemoryBytes` and `MaxGoroutines` before initialization; zero is
unlimited. Both scheduling modes enforce them. The SDK adds active-task duration
and no-progress watchdogs through `worker.SetIsolateResourceOptions`.

## Counters

| Counter | Meaning |
| --- | --- |
| `HeapBytes` | Allocated owned slots, including headers/rounding, until sweep refunds them |
| `ReservedHeapBytes` | Full capacity of owned spans, including occupied slots |
| `StackBytes` | Attached goroutine stack charges |
| `RuntimeBytes` | Attributable runtime metadata |
| `MemoryBytes`, `PeakMemoryBytes` | Charged heap + stacks + runtime metadata; peak charge |
| `TotalAllocatedBytes` | Cumulative accepted allocation bytes |
| `LiveGoroutines`, `PeakGoroutines` | Attached members, including initialization/entry/iterator runners |
| `RunningNanoseconds` | Completed scheduled intervals, including relevant metadata work/syscalls |
| `Progress` | Host-watchdog sequence advanced at supported waits/yields |

Unused reserved span capacity is not charged again as live memory. Shared
process services, host SDK objects, transport overhead, GC infrastructure and
RSS are separate costs. The budget is not a resident-memory ceiling. Concurrent
samples are independent best-effort counter reads. Suspension stabilizes member
and stack counts; GC can still refund slots and span metadata.

## Enforcement and lifetime

Reserve rounded allocation credit before requesting storage. A goroutine retains
unfinished credit across stack growth and GC assists; discard refunds credit
that never materialized into an allocation. Specialized small-object paths enter
the same reservation protocol. Check child admission before creating its live
membership. Stack growth checks limits at a safe boundary. Runtime preparation
holding process locks may temporarily finish charged work before honoring a
fault; never abandon a pinned/locked critical section to satisfy a byte ceiling.

Counters live in numeric native records without application pointers. Independent
references from the host handle, cache, spans and pooled wait records prevent
use-after-free. Sweep refunds a slot to its original owner account, never the
current allocating goroutine. Span/cache retirement releases their metadata
charges and references. Completed handles can retain usage/diagnostics without
rooting the private heap.

A violation publishes a preallocated immutable fault with bounded numeric PCs;
host code formats `ResourceLimitError` with resource, limit, attempted usage,
snapshot and stack. Attempted usage can exceed the accepted snapshot when a
charge was rejected. The same permanent group fence handles memory, goroutine
and host watchdog faults. Application recovery and defers cannot resume it.

## SDK task watchdogs

`ResourceOptions` contains runtime `Limits`, `MaxTaskDuration`,
`NoProgressTimeout` and an `Observer`. Each new execution snapshots configuration;
cached executions retain their policy. Startup consumes the explicit task budget.
Cached idle time consumes neither duration nor progress budget, and cached
instances have no active watchdog timer.

Use host monotonic time. `RunningNanoseconds` is not exact hardware CPU time,
and asynchronous preemption is not progress. `FailTaskDuration`/`FailProgress`
publish the host's supplied measurements through the permanent fault path.
An SDK deadlock timeout bounds an individual dispatch pass; an explicit maximum
task duration bounds the whole task, including successful transport passes.

Observers receive copied execution identity, replay state, usage and failure
text at lifecycle/task boundaries. A panic enters the SDK task-panic path after
disabling reentrant observation during cleanup. Faults fail the Workflow Task;
they do not complete the execution or send activity/timer cancellations.
Pending kill remains possible for uninterrupted code.

## Validation

Exercise reservation rejection/rollback, GC refunds, span reuse, stack growth,
iterator admission, initialization, retained handles and both dispatch modes.
Compare counters at quiescence and after cleanup. Measure process RSS separately
from charged usage. The SDK resource checker and opt-in soak test cover compiled
limits, watchdog failures, steady resource bounds and no callback resurrection.
See [validation](VALIDATION.md) and SDK `example/resources/STRESS.md`.

Primary source: `src/runtime/isolate_resources.go`, `src/isolate/resources.go`,
`src/internal/isolatebridge/resources.go`, SDK `temporalbridge/resources.go` and
`worker/resources.go`.
