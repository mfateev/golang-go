# Isolate independence: retire library mediation and typed transport

Status: proposed. Nothing here is implemented. This plan removes three
mediation layers that exist for reasons that no longer hold, and it has one
measurement gate that can kill the first and largest change cheaply.

| Change | Removes |
| --- | --- |
| 1. Per-isolate protobuf | The SDK's protobuf source-adapter layer and all host-mediated descriptor access |
| 2. Binary transport encoding | JSON envelopes and their base64 expansion of already-marshalled payloads |
| 3. Byte transport as the only boundary | The typed host APIs and their dedicated compiler/runtime machinery |

The three are related but separately revertible. Change 2 is partly independent
and can land first; changes 1 and 3 are sequenced.

## Why each layer exists and why it no longer should

**Protobuf mediation.** Descriptors, the registry and `MessageInfo` are
host-owned, reached from the isolate through host calls injected by build-time
source rewriting in `sdk-go-poc/internal/toolchain/adapters.go`. That rewriting
is pinned to protobuf's internal structure — method names, and body shapes such
as "the last statement returns a call whose selector is `MessageOf`." A protobuf
upgrade that reorders a function body does not fail loudly; it produces a
differently shaped rewrite. [The minimal core plan](./MINIMAL_GO_CORE_PLAN.md)
set the goal that external library upgrades must not require compiler or runtime
edits. That goal was met for the Go core, but the coupling moved into `tgo`
rather than disappearing.

**JSON envelopes.** `Call` carries `[]byte`; the format is entirely the SDK's
choice, as [the runtime API reference](./RUNTIME_API.md) states. The SDK chose
JSON, which has no way to carry raw bytes, so `commonpb.Payloads` is marshalled
to protobuf, base64-expanded into a text document, parsed back and unmarshalled.
One real reason it was chosen: every generated `.pb.go` type used inside an
isolate currently pays adapter cost, and `encoding/json` does not. Change 1
removes that reason.

**The typed boundary as transport.** Typed host functions were introduced to
avoid serialization at the boundary. With per-isolate protobuf, generated
messages carry a cached pointer to their own `MessageInfo` and cannot cross by
reference, so durable payloads must cross as bytes regardless. What remains to
avoid serializing is identifiers, names and option structs — which a binary
envelope handles without the ownership machinery a typed boundary requires.

## Change 1: per-isolate protobuf

Reclassify the protobuf module packages as instance state. Each isolate builds
its own descriptors, registry and `MessageInfo` during initialization, and
protobuf becomes an ordinary instance-state dependency with no special handling.

Retire from `tgo`:

- `registryReads` and the injected `IsolateGlobalTypes`/`IsolateGlobalFiles`
- the `HostOnly` operation registration for `protoregistry` and
  `internal/filedesc`, and their `Copy` functions
- the `ProtoReflect` and `Reset` body trimming for generated files
- the `impl.(*MessageInfo).MessageOf` nil-branch removal
- `RegisterGenerated`, `supportedGenerated`, `CheckMessages`, `CheckResolver`

Retain:

- the `internal/detrand` pinning of `Bool` and `Intn`. Protobuf deliberately
  randomizes map ordering to discourage dependence on byte-stable output; that
  is a determinism concern, not an ownership one, and it survives unchanged.

Reverse one current rule: `protoregistry.Register*` is `Reject: true` today.
Per-isolate registration during instance initialization is legitimate and the
rejection must be removed. Writes from application code after initialization
remain subject to ordinary ownership checks.

**Memory characteristics.** Two properties of generated code bound the cost
without any special handling.

`file_X_rawDesc` is a `const string`, not a `[]byte` variable, so the serialized
descriptors live in the binary's read-only data section and
`unsafe.Slice(unsafe.StringData(...))` only produces a slice header over them.
Every isolate addresses the same bytes with nothing copied, and `filedesc` holds
descriptor names as string slices into that same region, so names are
rodata-backed too. Host accesses wholly inside the linker's pointer-free data
section already use its exact bounds, so these reads are a classified fast path.

`filedesc` splits descriptors into an eager L1 tier and a lazy L2 tier
(`internal/filedesc/desc.go:153-322`). Initialization builds only L1; `Fields()`,
`Oneofs()`, `ExtensionRanges()`, reserved names and ranges, and per-field JSON
and text names complete on first access. So per-isolate cost tracks the
descriptors an isolate actually touches, not the whole linked surface.

What is genuinely per-isolate is the descriptor struct tree, `MessageInfo`, and
the registry maps. Measure that against the baselines below.

**Rejected: frozen shared descriptors.** Publishing one descriptor graph through
the read-only snapshot path used for `unicode`, `regexp/syntax` and `strconv`
was considered and does not work. Those tables are genuinely immutable;
protobuf descriptors are lazily completed by design. `Message.Fields()` writes
L2 under a mutex, `Field.JSONName()` mutates `stringName` even in the eager
tier, extension coder info is cached on first use, and `MessageInfo` protects
"all unexported fields" behind `initMu` rather than only what `init` builds.
Freezing would require a forced deep walk of every lazy path through exported
accessors, re-audited on every protobuf upgrade, with a missed path surfacing as
a production panic rather than a build failure. Per-isolate state makes all of
this ordinary mutable instance state that needs no audit at all.

**Consequences to accept.** Generated messages no longer cross the host boundary
by reference. `Any` resolution uses the isolate's own registry, so a host-packed
`Any` naming a type the isolate does not link will fail where it previously
resolved. Extensions registered on the host are not visible to isolates.

**Consequences to welcome.** Custom message implementations and custom resolvers
become ordinary, since nothing validates message shape any more. Dynamic
messages become possible. `timestamppb.Now` needs no special casing, because
there is no host execution to obtain workflow time before.

## Change 2: binary transport encoding

Two tiers, by operation shape.

**Scalar operations** — `OpCancelActivity`, `OpCancelLocal`, `OpCancelNexus`,
`OpAwaitActivity`, `OpAwaitLocal`, `OpSessionID`, `OpRemoveSession`. These carry
one integer or string and today cost several allocations and a text encode to
move eight bytes. Use `encoding/binary`, which is already in the selected
standard-library state set. No generated types, no dependency on change 1.

**Structured operations** — everything carrying `Payloads`, options, headers or
failures. Define transport messages in a single `.proto` and marshal once, with
`Payloads` as a submessage field rather than a base64-expanded `[]byte`:

```protobuf
message ScheduleActivityRequest {
  uint64 id = 1;
  string name = 2;
  bool by_name = 3;
  temporal.api.common.v1.Payloads input = 4;
  ActivityOptions options = 5;
  temporal.api.common.v1.Header header = 6;
}
```

Both sides generate from that definition, so the paired request and response
structs can no longer drift — a failure mode the current hand-rolled pairs
permit.

Observational `Write` payloads (`OpLogWrite`, `OpMetricWrite`) convert last.
They are off the task path and their drop semantics are unaffected by encoding.

## Change 3: byte transport as the only boundary

The SDK stops using typed host functions. Retire `sdk-go-poc/hostintegration`
after change 1 removes its protobuf users; verify whether the OpenTelemetry
adaptation needs anything from it, since its current rewriting only replaces
`nilTraceID`/`nilSpanID` sentinel comparisons and may not require host mediation
at all.

Remove the public typed host surface from the Go runtime: `NewHostFunc`,
`HostContext` (including `Allocator` and `Reenter`), `Allocator`,
`Isolate.Allocator`, `HostAllocator`, `Allocate`, and `Waiter`/`NewWaiter`
(including `Park` and `Wake`). Retire its dedicated compiler checks, runtime
entry points, allocation scopes, reference-borrowing and callback-reentry paths,
construction leases, and trusted host reference-sharing mode. Remove or update
the associated tests, fixtures and documentation. A hypothetical future zero-copy
operation is not a reason to retain this second boundary.

Audit dependencies before deleting supporting machinery. Internal scheduling,
ownership, allocation and lifetime primitives still required by byte transport
or ordinary isolate execution remain; remove the typed-host-specific paths and
public APIs. In particular, distinguish `Waiter` support from the suspension and
wakeup mechanisms used by byte calls.

The retained host communication surface is `Call`, `ControlCall`, `ReadOnlyCall`
and observational `Write`, all carrying copied bytes. Their existing scheduling,
read-only and delivery semantics remain unchanged; change 2 supplies binary
encoding for their SDK payloads.

This has an enforcement benefit worth stating. [Host integration](./HOST_INTEGRATION.md)
notes that entering a typed host operation switches an instance to trusted host
reference sharing, while "instances using only the existing transport retain
their publication checks." Removing typed host reference sharing keeps these
publication checks in force for every instance.

## Implementation sequence

1. **Scalar binary encoding.** Independent of everything else. Lands first and
   covers the highest-frequency operations.
2. **Protobuf reclassification behind a build flag.** Measure before
   proceeding — see the gate below.
3. **Retire the adapter layer** in `tgo` once the measurement passes.
4. **Proto transport envelope** for structured operations, generated for both
   sides. Cheap only after step 3.
5. **Remove the typed host boundary.** Retire `hostintegration`, verify no
   remaining consumers, then remove the public typed host APIs and their dedicated
   compiler/runtime machinery. Update runtime API documentation and coverage.
6. **Observational `Write` payloads.**

## Acceptance gates

**The measurement gate on step 2.** Reclassify and rerun
the retained-instance and startup measurements from
[memory density](./MEMORY_DENSITY.md) against its baselines: 381 KiB RSS and
65 KiB live per initialized instance, 446 KiB and 81 KiB per started and
suspended instance, and `NewContext` at 0.83 ms mean with 1.72 ms p99.

Watch the `NewContext` distribution harder than the memory delta. Memory is paid
by retained instances and is bounded by the sticky cache size; initialization CPU
is paid by created instances, so it scales with the cache-miss rate and rises
under exactly the load where the cache is least effective. It also sits on the
critical path of a Workflow Task, so it is latency the server observes.

Startup is CPU-bound in the baseline — roughly 0.79 ms sampled CPU against
0.83 ms elapsed — so added work appears directly in the number rather than
hiding behind contention. Descriptor construction is a fixed per-instance cost,
so expect the whole distribution to shift. If only the tail moves, that is
contention or GC rather than the descriptor cost, and should not be read as
this change's price.

**If the cost is too high.** There is no alternative design to fall back to —
frozen shared descriptors are rejected above, and host-mediated descriptors are
what this plan removes. The response is to reduce the cost, and there are two
levers before the design is in question:

1. **Prune the linked proto surface.** The isolate needs `commonpb.Payloads`,
   `commonpb.Header`, `failurepb.Failure` and whatever messages the workflow
   itself uses. If the SDK's import graph drags in more of
   `go.temporal.io/api` than that, descriptor construction shrinks roughly in
   proportion to what is trimmed. Measure the linked file count first; this is
   likely the largest lever.
2. **Keep eager work in L1.** Initialization should not touch accessors that
   force L2 completion. Any SDK or adapter code that calls `Fields()`,
   `JSONName()` or reserved-range accessors during instance startup converts
   lazy work into eager work for every isolate. Audit the initialization path
   for such calls before concluding that construction is inherently expensive.

Only if both are exhausted does the operating point need revisiting, and that is
a question about worker configuration — cache size against miss rate — rather
than about this design.

**Everything else.**

- Saved-history replay is unchanged. The transport envelope is not a durable
  artifact, so no recorded history, command ordering or random-stream
  consumption may change. Existing corpora must replay bit-identically.
- Per-isolate descriptor construction must be deterministic and must not consume
  the application random stream.
- No generated message crosses the host boundary by reference. Audit the bridge
  for any such path before step 3.
- Two independent registries coexist in one process — the host's `GlobalTypes`
  and `GlobalFiles`, and each isolate's private bindings — with no cross-talk.
- No public typed host APIs, SDK consumers or dedicated compiler/runtime paths
  remain. Publication checks stay enabled; byte-call suspension, completion,
  read-only restrictions, revocation and observational delivery still pass their
  existing coverage after removal.
- The full SDK, sample and compiled-fixture suites, race checks, and the
  two-revision upgrade/rollback replay all remain green at every step.

## Open questions

1. How many `.proto` files does the isolate's import graph actually link, and
   how much of that is reachable from workflow code rather than from SDK
   plumbing? This sets both the initialization cost and the first lever for
   reducing it.
2. Does `filetype.Builder.Build` tolerate running under instance state without
   touching unselected process-owned packages? Its dependency graph needs the
   same classification pass any instance-state library gets.
3. Does anything in the current bridge rely on host and isolate sharing one
   `MessageInfo`? The transport is byte-oriented today, so probably not, but it
   must be verified rather than assumed.
4. Should the transport `.proto` live in the SDK or be generated from the
   existing operation constants in `sdk-go-poc/workflow/workflow.go`? The second
   keeps one source of truth for operation numbers.
