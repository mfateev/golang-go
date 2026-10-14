# Cached isolate memory and future allocator work

## Measurements before read-only library sharing

Measured on 2026-10-14 with Go revision `2a584b2896` and SDK revision
`c0a2438916`, on Linux ARM64 with `GOMAXPROCS=8` and a 16 GiB container limit.
These results describe the selected protobuf program, not a fixed cost for every
isolate or the complete Temporal worker sticky cache.

The fixture used the production SDK dispatcher and binary converter, decoded a
small generated protobuf record, and suspended each workflow on a host `Call`.
Each started workflow retained its entry and cancellation-watcher goroutines.
It did not allocate a large signal-channel buffer or retain workflow histories.

| State, measured over 1,000 retained instances | RSS increase per instance | Live heap increase per instance |
|---|---:|---:|
| Minimal runtime test with 4 KiB private state | 23 KiB | 7 KiB |
| Protobuf program initialized, entry not started | 644 KiB | 185 KiB |
| Workflow started and suspended | 708 KiB | 201 KiB |

The minimal fixture has no compiler-generated dependency state. RSS includes
allocator fragmentation, stacks and runtime metadata; it is not live heap size.
Heap profiling is sampled, so individual attribution is approximate.

The initializer-only profile attributed approximately 102 KiB per instance to
Unicode data, 50 KiB to package-global layouts and lookup structures, and 14 KiB
to regexp Unicode alias maps. Other tables and initializers accounted for the
remaining roughly 20 KiB. No workflow arguments, execution context, or business
code had run at this point.

A run targeting 100,000 simultaneously cached workflows stopped at 18,000
because container memory exceeded its 14 GiB safety threshold. Population took
41 seconds. Process RSS was 12.15 GiB, live heap increased by 3.45 GiB, and
36,000 isolate goroutines remained. A forced GC took 816 ms elapsed time; that
is not a stop-the-world pause measurement. The process exited and released its
memory. Extrapolating this workload gave roughly 67 GiB RSS for 100,000 cached
instances, before environment headroom. The 100,000-instance result was not
actually measured.

A separate startup profile covered 3,000 creations: ten saved histories, three
`GOMAXPROCS` settings (1, 2 and 8), and 100 repetitions. Average `NewContext`
elapsed time was 0.83 ms, median 0.69 ms, p95 1.41 ms and p99 1.72 ms. Average
sampled startup CPU was approximately 0.79 ms. These measurements exclude input
conversion and workflow execution and mostly describe an already running host.

## Implemented read-only optimization

The generated process entry prepares independent, frozen snapshots of the
audited GOROOT packages `unicode`, `regexp/syntax`, and `strconv`. It replays only
those table initializers, records the exact allocated extents, and publishes
their reachable graphs atomically. This is not an approval of arbitrary
process-owned data. Reads after publication require no initialization lock.

Each isolate copies its package-global layout from the snapshot rather than
rebuilding its maps and table backing arrays. Bindings remain private: an
isolate can replace `unicode.L` or `unicode.Categories` with private values.
Mutating the shared default map, range structure or backing slice is rejected.
Ordinary host globals remain separate and retain normal Go behavior. Host
mutation cannot change the frozen snapshot used by future isolates.

Read-only query/validator scratch layouts use the same immutable backing data.
Attempted mutation of that data panics in the handler without revoking the
workflow. Regexp execution buffers, pools, compiled application expressions,
other library caches, and workflow values remain private.
Wildcard rune tables are copied when exposed through writable `syntax.Prog`
instructions; ordinary host compilation retains its existing allocation path.

Package layouts themselves are still copied. Their immutable fields and literal
storage could be split out in a later compiler optimization; sharing whole
mutable package layouts would violate the ownership contract.

Repeating the same 1,000-instance retained-memory measurements after this change
gave:

| State | RSS increase per instance | Live heap increase per instance |
|---|---:|---:|
| Program initialized, entry not started | 381 KiB | 65 KiB |
| Workflow started and suspended | 446 KiB | 81 KiB |

Started-workflow RSS decreased approximately 37%, and live heap decreased
approximately 60%. The process still reserved about 552 KiB of heap spans per
started isolate, so the remaining fragmentation is substantial. The shared
snapshot cost is paid once and included in the warmed process baseline. Startup
timings during this measurement overlapped compilation/testing and are not an
uncontended CPU comparison. These results do not establish 100,000-isolate
capacity in the 16 GiB environment.

The final implementation also retained 10,000 workflows simultaneously: RSS
increased by 4.26 GiB (447 KiB per instance), live heap by 791 MiB (81 KiB per
instance), and 20,000 isolate goroutines remained. Population took 15.9 seconds;
mean `NewContext` elapsed time was 0.83 ms. Three forced collections took
approximately 282–303 ms elapsed each. After GC, 100 sampled workflows resumed
and preserved their protobuf state. Eviction completed in 64 ms, the goroutine
count returned to one, and subsequent collections reduced live heap to about
16 MiB. RSS remained high during those immediate collections because Go retains
pages for reuse; process exit released them. This measures retained isolate
state and the POC dispatcher, not complete worker-cache history overhead.

## Future work: denser allocation

Go spans have different sizes, each a multiple of the 8 KiB Go heap page. Small
object spans hold one size class, separately for pointer-scanned and pointer-free
objects. For example, an 8 KiB span holds 256 allocation slots of 32 bytes.
Currently, a live span has exactly one owner. An isolate using only a few slots
still prevents peers from filling the remaining slots. A suspended isolate's
partially occupied spans cannot be reclaimed merely by flushing its cache.

Possible designs to prototype, not implemented by this change:

1. **Free-span lists indexed by owner.** Reduce searching and reuse compatible
   partial spans more efficiently. This retains fragmentation across owners.
2. **Mixed-owner spans with ownership per allocation slot.** Preserve Go's size
   classes and object-scanning machinery while packing objects from different
   isolates together. Allocation, access checks, tiny packing, sweeping and
   per-isolate resource refunds need slot ownership. Partially live spans cannot
   be freed in bulk when one isolate is evicted.
3. **Isolate pages holding objects of different sizes.** Preserve isolate-local
   pages while reducing size-class fragmentation. This requires more substantial
   changes to object lookup, pointer layouts, marking, allocation and sweeping.

There is no universal unused object header. Headerless and pointer-free objects
have no spare field. Some larger pointer-containing objects have an eight-byte
type header needed by GC. Pointer tagging is architecture-dependent and does not
cover headerless objects. Size-class padding is not guaranteed and can be
application-accessible. Mark, allocation and pinning bits already have essential
uses.

A side table can retain unchanged object sizes and addresses. A 32-bit global
owner ID covers 100,000 isolates but costs four bytes per allocation slot,
including free slots. A smaller span-local owner index plus a dictionary, or an
adaptive single-owner representation that adds a table only when mixed, may
reduce that cost. Reusing existing span ownership fields to find the side table
avoids another object header, but the table still consumes memory. Measure both
the ownership-lookup CPU cost and actual density before choosing a design.

## Future work: GC and accounting from isolate roots

The global Go collector traces roots and marks individual objects. Sharing a
physical span creates no pointer edge: independent isolate graphs can share
spans while using the existing collector. Host cache handles must keep cached
state reachable. Shared immutable graphs are intentionally reachable from many
isolates and need separate accounting.

Allocation counters can charge the running isolate without an owner ID inside
each object. During GC, carrying an isolate identity through root traversal
could calculate its reachable live bytes without permanent owner IDs. This
still visits/marks objects; looking only at root pointers cannot reveal their
retained sizes. Concurrent allocations, finalizers, runtime references and host
cache handles need an explicit attribution and publication protocol.

Root attribution does not independently detect cross-isolate pointers. Current
checks guard against missed global routing, shared mutable caches, uncopied host
arguments, captured host closures and unsupported unsafe escapes. Mixed-owner
spans would need object ownership to retain those checks, unless isolation is
instead proven by the compiler and boundary implementation. Removing checks is
a separate design decision, not part of the read-only optimization.

Skipping roots of cached isolates would collect their live data. Frozen-cache GC
needs an explicit retention/marking protocol; neither span sharing nor root
attribution provides that automatically.

## Future work: copy-on-write

Software copy-on-write could share an initialized graph until an isolate first
mutates it. Replacing the package-global pointer alone cannot preserve existing
aliases into maps, structs and slice backing arrays. Correct transparent copying
needs isolate-specific address resolution through compiler accesses, map
operations and reflection while preserving alias identity. The current ownership
checks validate addresses and cannot redirect them.

OS page-level copy-on-write does not give different views to isolates sharing
one virtual address space. Audited immutable data can be shared directly;
general copy-on-write remains a larger future compiler/runtime project.
