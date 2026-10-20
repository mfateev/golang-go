# Reimplementing the isolate core

This guide describes a construction order from a clean Go source tree. Each
step states the required data/algorithm and the observable gate. The companion
contracts specify the public API and edge cases. The SDK's implementation guide
specifies discovery, library policy and Temporal transport separately.

## 1. Coordinate the experiment and ABI

Add `GOEXPERIMENT=isolates` to Go's experiment definitions, disabled by default.
The compiler, runtime, standard library, Go command and package cache must agree
on that experiment. Build ordinary Go without it as a regression configuration.
Keep conventional `bin/go` and `bin/gofmt`; the SDK supplies `bin/tgo`.

Define metadata/API/determinism markers, all currently 1, in a small internal ABI
package. Export them through public `isolate.CurrentContract`. Prefix compiler
package descriptors with a uint64 metadata version. Check versions before reading
version-dependent strings/pointers or registering functions. Markers check a
matching implementation; they are not Git revisions or compatibility selectors.

Gate: bootstrap both configurations; reject a descriptor containing only an
incorrect version prefix without reading beyond it.

## 2. Emit typed package state

Introduce a manifest selecting entries, generated adapter sources, state/scratch
packages, support roots and generic unsafe audits. [Build contract](BUILD_CONTRACT.md)
defines its format. Only the generated process entry can import privileged
registration/state packages; external adapters use public `isolate.Value` slots.

For each state package, create a typed struct of all mutable globals sorted by
symbol name. Emit unique key, layout type cell, variable offsets, selected
import dependencies, immutable initializer list and descriptor. Preserve original
host storage and process init. Move static initializer assignments into replayable
functions. Reconstruct embedded strings/slices before source init; allocate byte
backings privately. Avoid process init-completion flags during replay.

Lower local/imported/global references to current-table lookup plus offset,
including inlined and generic bodies. Export imported offsets and key symbols.
Check an active table's missing key. Select host vs instance address before pointer
arithmetic; invalid interim pointers cannot survive a GC safepoint.

A state factory validates versions/paths/keys, sorts and topologically orders
dependencies, allocates all typed layouts, attaches the table, runs init once per
package and restores prior state. A group runner restores state around dispatch.
Goroutine creation inherits table/owner/group; reuse clears every such reference.
Compose support factories by unioning descriptors and deduplicating dependencies.

Gate: host and two instances have distinct initialized pointer/map/closure graphs;
children inherit; imports/inlining/generics route consistently; missing/mismatched
keys and cycles reject. [Package state](PACKAGE_STATE.md) gives the exact records.

## 3. Add group membership and lifecycle roots

Extend `g` with typed group/package-table references and scalar owner/role state.
Use typed GC roots for heap references; off-heap runtime structures keep only
numeric owners/counters or established runtime pointer representations. Maintain
live/running membership before dispatch admission. Entry and initializer roots
are normal managed goroutines; native children and iterator runners attach too.

A revocation group holds lifecycle state, fault publication, clock/random state,
allocation handles, resource account and dispatch queues. Keep a host terminal
cause separate from final drain. Notify drain only after all wait/stack records
detach. Arrange cleanup on a host worker so it does not require running revoked
application code.

Gate: normal return drains children; initializer cancellation sees pending joins;
retained completed handles do not retain private state.

## 4. Route allocations and retain the existing GC

Add numeric span owner metadata and an instance `mcache`, separate from per-P host
caches. Route all private malloc paths through it, including compiler-specialized
small allocations and large objects. Keep tiny packing within one owner. A second
cache serves scratch execution. Lock cache access; release before assists,
allocation slow-path work that can park, or other incompatible runtime locks.

Register non-GC caches for STW flushing, sweep generations, ReadMemStats/profiles
and retirement. An acyclic GC-visible handle owns each cache; group membership
keeps it alive. The cache registry must not root the handle/group. Finalization
retires the cache without freeing live objects. Sweep and span reuse refund the
original owner's counters and clear owner metadata.

Do not replace ordinary object addresses, type pointer maps, root scanning or
mark/sweep. Suspended layouts/stacks remain GC roots. Exposed heap stats and
owned span capacity are distinct. See [memory ownership](MEMORY_OWNERSHIP.md).

Gate: allocations retain their owner across P migration; small/large/tiny paths
separate instances; forced collections/reuse preserve values; cyclic evictions
return cache/account counts to baseline with handles retained.

## 5. Instrument access and publication

Provide runtime probes for full-range read/write, typed reference publication,
copy/move/string/slice/map/channel/reflection/atomic paths. The [ownership contract](MEMORY_OWNERSHIP.md)
defines the matrix. Distinguish static read-only memory, mutable process data,
private spans, current stack and unclassified/manual spans. Owner zero is host,
not shared read-only permission.

SSA checks execute before effects and before references become GC-visible.
Recursively walk pointer-bearing fields when publishing values. Validate map keys
and values against the map owner, not an invented contiguous slot range. Probe
all select channels before locks/waiter creation. Preserve nil/bounds semantics.
Compiler-created stack maps and map headers need owner tagging too.

Probes are safepoints. Copy result ABI areas before calling probes; validate
sources before destination liveness; derive interface method addresses from the
checked itab so scheduling cannot expose nil-plus-offset pointers to stack scans.
Keep ordinary uninstrumented SSA behavior and devirtualization unchanged.

Force checks across every manifest build action, including unselected packages.
Caller debug flags cannot turn them off. Preserve normal host behavior when the
same code runs outside an instance.

Gate: alias/interface/closure/bulk/reflection/atomic reads and writes fail before
foreign access; publication cannot hide pointers; forced stack movement and nil
interface dispatch preserve correct GC and panic ordering.

## 6. Define service provenance and effects

Use a shared compiler/runtime policy for forbidden effects and GOROOT exceptions.
Record verified source provenance on declarations, export/import it and retain it
through generics and inlining. Before inlining, guard unsafe/native/linkname
helpers and forbidden operations; revisit newly read bodies. After inlining,
validate direct writes to unselected imported globals. Keep address lowering in
SSA and initializer emission in pkginit.

Audit standard reflection type constructors as canonical immutable metadata
services. Borrow caller data for bounded reads without retaining it. Reject
application callbacks inheriting metadata privilege, including reflection
assembly and registered defer/go targets. Exact CPU-feature/backend cells get
bounded read permission, never a general package/global exemption.

`sync.Pool` must bypass process lists privately. Protect `atomic.Value` before
its pinned sentinel path. Copy runtime metadata values into the current owner
when exposed. Freeze only explicitly audited library graphs and publish complete
extents atomically. Ordinary host globals remain separate.

Support generic package/file unsafe audits using physical source identity; they
retain ownership/effect checks. External dependency names confer no authority.
Keep protobuf/OTel version checks and overlays in the SDK.

Gate: application unsafe/linkname/native escapes and namespace impersonation
fail; imported generic provenance survives; metadata callbacks and later defer
execution cannot gain authority; host calls remain usable.

## 7. Add deterministic native dispatch

Intercept ready publication for group members. Under a leaf dispatch lock, either
reserve an idle token for an eligible goroutine or append it to the appropriate
FIFO. Only a token holder reaches ordinary process run queues. Never allocate,
run write barriers or acquire another lock under the dispatch lock.

On committed user park/yield/exit, reserve the next eligible token before
publishing it. Retain the token for preemption, GC and runtime-internal waits.
Failed parks retain the current token. Clear batch tail links when filtering
ordinary scheduler lists. Count joins before their park to close false-idle races.

Implement normal/high-priority queues and role admission: normal, inbound,
high-priority writable and read-only. A host suspend waiter observes no token,
no eligible ready members and no counted joins, then fences admission. Replying
while fenced only makes members queued; an explicit resume admits them. Add
explicit race acquire/release for runtime locks/token transfer without changing
Go's application synchronization requirements.

Gate: identical traces with GOMAXPROCS 1/2/8, concurrent host GC and peers;
channel/select/Mutex/RWMutex/Cond/WaitGroup/iterator handoff; correct role fences;
no suspension false-idle interval or dependence on asynchronous preemption.

## 8. Make observable primitives deterministic

Install a group ChaCha8 stream before init. Route select and top-level supported
random APIs through it, including legacy random buffer/distribution state. Reserve
Call transport polls before parking; inbound transport uses fixed polls. Query
scratch randomness is independent. Explicit local generators retain their seeds.

Map iterators snapshot/sort supported keys, check current values and skip deleted
keys. Keep ordinary hashing. Update both compiler iterator layouts and reflection
paths; `sync.Map.Range` needs the corresponding sorted snapshot behavior.
Implement `iter.Pull` through owned channel runners rather than direct coroutine
switches. Preserve host iterator behavior.

Supply history Unix time independently of real runtime clocks. Route native
sleep/timers/context deadlines through a generic timer Call, using generation
checks to suppress stopped/reset deliveries. Match synchronous timer channel
length/capacity behavior. Resolve default Local to UTC; explicit zone data works.
Reject machine-dependent clock/entropy/GC observations and unsupported tickers.
Pin the numerical fused-rounding paths required by random distribution replay.

Gate: full primitive edge cases plus a recorded trace compared against freshly
computed observations on both architectures/CPU-feature settings. See
[determinism](DETERMINISM.md) for the exact supported operations and limits.

## 9. Implement copied transport

Create host-owned command/reply queues and a separate halt signal. Copy outgoing
request bytes in host allocation context; store only copied error text. Copy a
reply back under the active workflow/scratch owner. Enforce one reply per command.
Maintain pending-call count for quiescent progress decisions. Halt must wake a
caller regardless of user synchronization teardown.

`Call` blocks writable code; `ControlCall` is restricted to deterministic inbound
services. `ReadOnlyCall` identifies a dedicated query/validator service and changes
its owner after the first request reply. `Write` copies observations into a
64-message queue with a 64 KiB/message bound and no acknowledgments. Queue/drop
behavior must not feed workflow decisions. Format standard logs privately and
reserve their opcode independently of durable timer policy.

Gate: large request/reply allocation origins, no retained host objects, shutdown
at each handshake, delayed host receipt preserving random draws, bounded writes
and final-log draining. [Host integration](HOST_INTEGRATION.md) gives the SDK framing.

## 10. Add read-only scratch execution

While normal continuations remain queued, admit only read-only services. After
startup handshake allocate under scratch ownership, copy the package-table
bindings and rebuild selected library layouts. Keep application layouts for reads.
Use the same audited immutable backings where appropriate. Permit scratch values
to retain workflow references, never the reverse.

Reject workflow writes before mutation with a recoverable handler panic. Forbid
new children, durable operations and waits; check before race suppression and
waiter queue creation. Scratch-owned locks may operate without blocking. Recreate
serializer/interceptor/propagator chains locally. Snapshot captured protobuf data
before cloning/conversion to avoid workflow reflection-cache mutation.

Gate: query/validator cannot change workflow state, caches, random streams or
queued continuation order; scratch can mutate its own temporary values; completed
queries and eviction retire both caches.

## 11. Complete faults, resources and diagnostics

First permanent fault wins. Ownership/effect/quota faults mark revocation and
suppress application panic recovery/defers, including range-function recovery
tokens. Let trusted locks/metadata unwind before discard. Ordinary recovered
panics and child Goexit keep Go semantics; contain unrecovered child panics.

Wake native parks and stop transport, detach records, merge race clocks, notify
drain, clear private roots and release allocation handles before `Done`/successful
kill. Bound initialization/kill with host contexts; pending results retain copied
diagnostics and revocation. Never promise forceful termination of arbitrary loops.

Reserve resource credit before allocation/admission and refund correctly on
interruption/sweep. Use numeric records with independent references; publish
faults without allocating on native critical paths. Host task/progress watchdogs
are separate from scheduler CPU accounting. Capture stacks before kill; allocate
diagnostic buffers before saving stack pointers because allocation can move them.

Heap dump runs on g0: with race instrumentation, lend the caller's race context
while dumping reflected metadata and restore g0 afterward. Reflection's dynamic
name resolution uses race-annotated locks/maps even during STW.

Gate: completed fences forbid late code, callbacks and double replies; retained
handles/errors release private graphs; race/lock-ranking tests cover fault/kill
races, trusted waits, failed startup and heap dumps of dynamic reflected types.

## 12. Integrate the SDK and preserve build identity

Generate process registration from explicit entries and compiler descriptors.
Hash adapter contents plus normalized graph-local state/audit/effect flags; do
not hash only manifest paths or invalidate every dependency for unrelated entries.
Support internal/external test variants with distinct source identities, registration
before TestMain and ordinary test cache behavior. Marked coverage mode remains
unsupported. SDK `tgo run` builds a marked temporary executable before running it;
`tgo install` does not generate entries.

Build the SDK adapter described in its implementation guide: startup header,
private converters/protobuf, binary operations, history-ordered task phases,
callback retirement and correct Task-vs-Execution failure distinction. Test native
workflow registration through worker/replayer plugins, not only direct calls.

Finish with [validation](VALIDATION.md), including ordinary disabled-experiment Go,
compiled external-manifest fixtures, race/static lock ranking, SDK/samples and
fresh-process replay. Source pins keep independently built repositories aligned.
