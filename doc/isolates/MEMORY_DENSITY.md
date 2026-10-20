# Retained memory and startup cost

The current allocator gives each live span one owner. Small-object spans use
Go size classes and scanned/noscan divisions; unused slots cannot be filled by
other isolates. A mostly idle workflow can therefore reserve much more span
capacity than its live objects occupy. Package layouts and initializer-created
graphs are eager. Each blocked goroutine also retains its stack and group state.

Frozen Unicode, regexp syntax and strconv backing tables reduce duplicated
library data, while bindings stay private. Protobuf registries, descriptor trees
and coder caches are private and lazily completed within each instance. Host-only
service descriptor graphs are excluded from private initialization. These choices
trade substantial retained memory/startup work for independent library execution.

## Reproduce the current fixture

From `sdk-go-poc`, using its matching toolchain, on Linux:

```sh
tgo build -o /tmp/isolatebench ./internal/cmd/isolatebench
/tmp/isolatebench -instances=1000
/tmp/isolatebench -instances=1000 -started
```

Run built binaries serially outside compilation/server activity. The fixture sets
`GOMAXPROCS=8`, warms five constructors, collects before/after population and
reports RSS, live heap, constructor percentiles and post-GC resumption checks.
RSS sampling reads Linux `/proc/self/statm`. Each started workflow receives a
`Record{Sequence:42}`, suspends on a copied-byte Call and retains its cancellation
watcher. Sampled workflows resume after GC and must return the original value.
Initialized-only instances never start the entry. All instances are killed after
measurement. Repeat fresh processes to distinguish steady cost from noise.

For a representative 1,000-instance run of this graph, budget roughly 0.8 MiB
RSS / 0.34 MiB live heap per initialized instance and 1.1 MiB RSS / 0.58 MiB live
heap per started/suspended instance, with constructor mean around 1.5–1.7 ms.
These are sizing examples, not contractual limits; rerun the fixture for the
actual toolchain, dependency graph and machine. Constructor timing excludes
input conversion and workflow startup. Full cached Temporal histories, host SDK
cache entries and business state add further cost.

## Interpret the measurements

- `HeapAlloc` after GC measures live Go objects, not RSS or owned span capacity.
- Runtime `Resources()` measures charged owned slots/stacks/metadata; reserved
  span capacity is reported separately and is not an RSS quota.
- RSS includes committed allocator pages, stacks, runtime/process state and
  fragmentation. Go may retain freed pages for reuse after eviction.
- A forced collection's elapsed time is not its stop-the-world pause duration.
- Whole-program graph selection and lazy protobuf access change costs. Avoid
  forcing unused descriptor caches merely to inspect them during initialization.
- Sticky-cache size bounds retained instances, while cache misses pay constructor
  CPU repeatedly on the Workflow Task critical path.

The POC does not implement mixed-owner spans, a compact nursery, object-slot
ownership, general copy-on-write, heap snapshots, stack serialization or skipping
cached roots during GC. Suspended workflow roots must remain traced or their live
objects would be collected. There is no 100,000-workflow capacity guarantee.
Use the fixture plus realistic worker-cache/history load before setting capacity.
