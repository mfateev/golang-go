# Temporal SDK integration

The native SDK uses `go.temporal.io/sdk v1.50.0` with the matching sibling fork
specified in `toolchain/go-source.json`. Host activities, clients, polling,
retries, command state machines and server protocols remain host SDK work.
`workflow` and `temporalbridge` replace workflow execution/concurrency with native
Go isolates. SDK dependency policy and distribution stay outside the Go core.

## Registration and program construction

`tgo` discovers `//temporal:workflow` on concrete top-level functions, including
test variants, then emits typed argument/result invokers and a generic manifest.
The Go build links a process entry that registers compiler-owned descriptors.
`worker.New`/`Wrap` identifies marked functions with `isolate.LookupFunction`,
constructs a native definition factory and preserves aliases/options. Unmarked
SDK workflows keep `workflow.Context` and their ordinary execution path.
Native workers require `context.Context` first and `BlockWorkflow` panic policy.
Host clients start native functions by registered string name.

Marked serializer/interceptor/propagator/failure-converter factories receive
copied config and compose their package state with the workflow through support
handles. Each shared package initializes once. New executions snapshot worker
configuration; cached definitions retain their snapshot. Plugin registries must
receive the wrapper, or plugin registrations bypass native detection.

## One Workflow Task

1. History callbacks queue signals, updates, cancellation and operation replies;
   they do not run normal application continuations directly.
2. On task start, create/configure an instance if absent. Set replay-stable seed,
   current history time, timer opcode, limits and logging before init. Install
   query/update/reset routers before application registration can race delivery.
3. Start the SDK dispatcher and validate both sides of the startup contract before
   decoding application values or running factories. Existing instances advance
   their history clock and pending reset identity at a fence.
4. While servicing command/write channels, suspend until admitted work is idle.
   Deliver replies and inbound signal/context/future/cancel events in history
   order. Resume inbound services first; process high-priority writable work as
   required, including accepted update handlers; then admit normal continuations.
5. Repeat resume/suspend passes until transport handshakes finish and no new
   commands remain. An instance blocked without an external pending call is a
   native deadlock under this contract. Capture diagnostics before killing it.
6. Emit host SDK commands or retain cached state. Completion without queries
   drains the native group before publishing an execution result. Completion
   with queries retains a read-only-fenced instance until eviction.

Task timeouts use host monotonic time. The SDK deadlock timeout bounds a dispatch
pass; an explicit resource maximum bounds the full task including startup.
See the SDK implementation guide for the algorithm and operation families.

## Operation coverage

| Area | Native behavior |
| --- | --- |
| Activities/local activities | Function/name aliases, host SDK options, repeatable futures, cancellation and structured failure details |
| Children/external workflows | Initiation/result separation, signals/cancellation, aliases, options, parent-close behavior and recorded retries |
| Signals | Typed native channels, proactive decoding before continuation admission, queued-before-registration delivery, corrupt-signal skip/log/metric |
| Queries | Scratch-owned read-only execution, completed-state queries and native stack query |
| Updates | Read-only validation, priority admission/handlers, identity, unfinished policies and handler draining |
| Dynamic handlers | Named precedence and native fallback workflow/query/update dispatch with local EncodedValues |
| Continuation/versioning | SDK command semantics, explicit run state, recorded version markers and previous-run metadata |
| Context | Standard context cancellation/deadlines, native propagators, per-context serializers and opaque headers |
| Payloads/failures | Private converters, Google protobuf, custom failure factories; host compression/encryption/remote codecs |
| Sessions/Nexus | Host SDK protocol, native contexts and separate execution/result futures with cancellation policies |
| Metadata/visibility | Info, memo, typed/untyped search attributes, previous completion/failure and reset-aware identity |
| Randomness | Default shared runtime stream plus independent named SDK streams |
| Observability | Replay-aware log/metric/sink Writes, native interceptor chains, tracing adapters with host exporters |
| Workflow streams | Native publish/subscribe log, update long polls, batching, truncation and continue-as-new state |
| Tests | Host test engine plus compiled isolates, native mocks/replacements, virtual time and copied test-reference IDs |

`SideEffect`/`MutableSideEffect` are excluded; external/nondeterministic work belongs
in activities. Native go/channels/select/time replace the SDK coroutine facade.

## Serialization and protobuf

The workflow owns conversion of arguments/results and error details. Host codecs
receive opaque Payloads and apply once, including failure detail/cause chains.
Protobuf transport submessages preserve payload metadata/bytes and unknown fields.
No application object or converter closure crosses the host boundary.

Each workflow and read-only service initializes its own descriptors, registry and
coder state. Generated message bodies remain ordinary code. `Any` and extensions
resolve only against linked/privately registered types. Small SDK overlays pin
protobuf ordering, private environment defaults, captured-message scratch cloning
and OTel zero-ID comparisons; module cache and checked-in generated sources stay
unchanged. The core has no library-specific allocation or metadata API.

Configure marked factories with `SetIsolateDataConverter`,
`SetIsolateContextPropagators`, `SetIsolateFailureConverter` and
`SetIsolateInterceptors` on workers/replayers. Queries and validators reconstruct
fresh scratch chains/converters. Matching host configuration is required for
client/activity interoperability. Observations and headers bypass application
serialization. Cardinality-changing batch codecs and external-storage references
are unsupported at the native boundary; legacy gogo is outside the default
native serializer contract.

## Host SDK binding contract

Binding version 2 supplies WorkflowDefinition/WorkflowEnvironment integration,
reset callbacks, previous completion/failure data, recorded memo converter policy,
canonical typed-search-attribute updates and worker/replayer plugin registry
overrides. Borrowed host protobuf metadata must be serialized before private
exposure. Reset metadata includes NewRunID/BaseRunID/EventID; initialization uses
OriginalRunID and subsequent identity updates are delivered at a task boundary.
The SDK's machine-readable source pin identifies the required fork implementation.

The byte startup protocol is 7; metadata/API/determinism are 1. Unsupported or
zero markers fail before factories/application decoding. See
[host communication](HOST_INTEGRATION.md) for exact framing.

## Failure, cleanup and limits

Native permanent faults and unrecovered root/child panics fail the Workflow Task,
never become an application result through `FailWorkflow`. Ordinary returned
application errors use configured failure conversion. Query/validator write
rejections fail their request while preserving the workflow.

History callbacks retain retireable cells, not live commands forever. Definition
close marks cells done and clears references; callbacks after close are inert.
Eviction kills local state without canceling durable server work. Cancellation
of workflow contexts remains cooperative. Automatic worker-stop cache teardown,
variadic handlers, arbitrary native code and hard kill of non-yielding loops are
outside the POC contract.

See SDK `SDK_REFERENCE.md`, `IMPLEMENTATION.md`, `TRANSPORT.md`, `INTERCEPTORS.md`
and `TESTING.md` for exact APIs, wire fields and executable checks.
