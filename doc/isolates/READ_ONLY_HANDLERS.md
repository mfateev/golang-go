# Query and update-validator execution

The host admits a dedicated service goroutine with `ResumeReadOnly`. Normal
workflow goroutines stay in their original FIFO queue and cannot receive a
dispatch token during that admission. `ReadOnlyCall` transfers requests and
responses through the existing copied-byte boundary.

After its first host reply, the service uses a separate scratch allocation
owner. It can read the original workflow heap, including captured closures,
but writes into that heap panic before mutation. The panic is recoverable and
does not publish an ownership fault or revoke the workflow. Other foreign
owner access still follows the ordinary fatal ownership policy. Newly allocated
scratch values remain writable and may retain read-only workflow references;
workflow values cannot retain scratch references.

Audited standard-library layouts and caches are initialized under the scratch
owner. Frozen Unicode, regexp syntax and strconv layouts instead copy private
bindings while sharing their audited immutable backing data. Mutation of those
shared maps, ranges and slices panics in the handler without revoking the
workflow. Application package layouts retain the original read-only references.
Reflection's audited metadata service remembers the actual caller owner, and
can borrow both scratch and workflow values without publishing them into
process caches. Protobuf descriptors, registries and coder tables also initialize
privately in scratch state. The SDK snapshots captured message fields, unknown fields and
extensions before clone/conversion, so scratch operations do not reuse mutable
workflow reflection caches. Scratch-owned Mutex/RWMutex operations are permitted
when they do not block; workflow-owned locks cannot be mutated.
Query select and random counters are separate from workflow counters. Reading a query does not advance workflow random streams or time.

Read-only handlers cannot create application goroutines, use blocking native
channels/select/synchronization, schedule durable operations, or terminate the
isolate. Synchronization checks precede race-detector suppression and runtime
waiter creation so a recovered rejection cannot leave either subsystem altered.
The host still owns deadline and resource enforcement; non-yielding CPU loops
are not fully contained by this POC.

Completed workflows with queries remain frozen until SDK cache eviction.
Their ordinary continuations stay parked; only the query service can resume.
Both allocation owners share the instance resource account. Eviction drains
the group and detaches both cache lifetime handles. The runtime tests exercise
read-only borrowing, workflow continuation after rejected writes, and retirement
of 64 cyclic instances with two caches each. The compiled SDK probe additionally
checks reflection, atomics, callbacks, blocking APIs, multiple isolates, race
detection, completed-state queries and saved-history replay.

See [memory ownership](MEMORY_OWNERSHIP.md), [task integration](TEMPORAL_INTEGRATION.md)
and [lifecycle](LIFECYCLE.md) for publication rules and the host cleanup fence.
