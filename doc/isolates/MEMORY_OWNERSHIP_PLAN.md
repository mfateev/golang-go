# Memory ownership: productization item 2

Prerequisite: determinism item 1 passed local gates and all four native jobs;
the results and source revisions are checked in at `cf8d69e7b9`. This feature
does not include freezing cached heaps or implementing a separate collector.

## Allocation and collection

- Give each instance an allocator cache independent of the processor executing
  its goroutines. Moving between processors must not move its allocation owner.
- Small and large object spans have exactly one owner. The process remains
  owner zero. The shared page allocator and GC remain process machinery; object
  placement and central-list reuse must never mix owners within a span.
- Tiny allocation blocks must not combine host and instance objects. Initially
  disable tiny packing for instance allocations rather than introduce another
  hidden GC root in each cache. Preserve the ordinary process fast paths.
- Keep owner caches in a runtime registry that GC can flush at the correct
  sweep boundaries and include in allocation/scan accounting. The registry must
  not keep instances alive. A separate GC-visible cache handle can retire the
  non-GC cache after the group becomes unreachable, including groups with
  cyclic transport callbacks.
- Reuse compatible partial spans through the existing central lists. A bounded
  owner search may allocate a fresh span; measure fragmentation before tuning
  it or adding owner-specific central queues. Never retag a span containing live
  objects. Fully swept empty spans may change owner.
- Keep ordinary concurrent tracing and sweeping. Owner tags prepare for later
  cached-heap GC work, but this feature must not skip tracing or free a heap
  while runtime queues can still reference it.

## Compiler and boundary checks

- Extend the build manifest beyond its POC package allowlist. Every reachable
  mutable global needs instance state or an explicit process-service policy;
  an unclassified state access must fail closed.
- Instrument pointer reads/writes, pointer publication, slice/closure/interface
  access, maps, channels, reflection, and bulk copies. Enforce the same owner
  across all supported access paths, including small objects and interior
  pointers. Host execution keeps its ordinary behavior for process objects.
- Retain the copied-byte `Call` boundary and typed, compiler-generated invokers.
  Workflow arguments and results are converted within their instance. Host
  callbacks and allocator/runtime metadata use explicit trusted paths.
- Share immutable code, static type metadata, and proven read-only data. Do not
  treat arbitrary process-owned heap objects as immutable merely because their
  owner is zero.

## Process metadata services: design decision

Reflection and protobuf use process-wide type registries and lazy caches. Some
hold locks shared with the host, and some allocate metadata on their first use.
The POC currently classifies the default converter dependency graph as a process
service; that broad exemption is insufficient for enforced heap ownership.

The proposed policy is a small audited set of trusted metadata operations:
their shared caches and immutable metadata remain process-owned, decoded user
values remain instance-owned, and revocation waits until a service has released
its process locks before discarding the caller. Ordinary application locks and
mutable objects remain private to an instance. This needs a decision because
the earlier no-shared-locks rule must distinguish application locks from
process metadata services. Alternatives are isolating the caches and rejecting
APIs that require global type identity, or routing all shared service access
through host operations.

Allocator separation can proceed independently of that policy. Do not enable
unchecked shared mutable access while the service decision is pending.

## Initial audit

- `malloc.go` and generated `malloc_generated.go` both fetch the processor's
  cache directly. Update `_mkmalloc` and regenerate its output; changing only
  the generic allocator leaves the normal fast paths mixing owners.
- `mcentral.cacheSpan` currently returns any compatible size-class span.
  Owner matching must cover swept and unswept partial/full paths, not merely
  newly grown spans. Preserved empty spans can be retagged after sweeping.
- GC resets scan accounting at mark termination and prepares caches for the
  new sweep generation. It also flushes idle caches before the next cycle.
  Owner caches must participate without keeping their groups alive.
- `profilealloc` updates per-cache sample counters, and `ReadMemStats` flushes
  allocation counts. Selecting an owner cache for allocation must also select
  the correct profiling counters and include it in those flushes.
- The existing selected package layouts already include reflection and time;
  the converter/activity dependency graphs remain broad process exemptions.
  Narrowing those exemptions depends on the process-service policy above.

## Gates

1. Allocation tests cover tiny/small/large, scan/noscan, interior pointers,
   processor migration, concurrent instances, repeated GC, cache retirement,
   and allocation statistics. Test both generated fast paths and race paths.
2. Negative tests reject host/instance and instance/instance crossings through
   every supported language and reflection operation. Positive tests retain
   immutable sharing and copied-byte transport.
3. Package-state tests use the same package on the host and in two instances,
   including initialization, caches, callbacks, and default converter use.
4. Run compiler/runtime regression and lock-ranking gates, SDK/sample tests,
   and the retained determinism history on the native platform matrix. Ownership
   work must preserve the item 1 trace and ordinary Temporal workflows.
5. Check in results and all three repositories; publish the supported service
   manifest and any remaining exclusions before claiming feature 2 complete.
