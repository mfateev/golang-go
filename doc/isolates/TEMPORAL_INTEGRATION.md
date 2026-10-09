# Temporal integration: features 7 and 8

The POC uses Temporal Go SDK v1.49.0 host bindings with statically compiled
`//go:isolate` workflow functions. Native contexts, goroutines, channels,
select and deterministic time replace SDK execution constructs. Ordinary SDK
workflows and host activities retain their registration and execution behavior.

## Delivered API and behavior

- Activities: SDK options, function references/aliases, repeatable futures,
  cancellation policy, structured errors and isolate-owned serialized results.
- Continue-as-new and workflow versioning: SDK error/options and version
  markers, recorded histories and computed-result replay checks.
- Queries: read-only allocation owner while workflow goroutines are fenced;
  mutation panics before the write and becomes a query error. Completed query
  state remains cached until eviction.
- Updates: SDK registration/options, admission before handler execution,
  read-only validators, native handler goroutines, update identity and unfinished
  handler policies. Replay decodes recorded inputs but skips validators.
- Child workflows: SDK child options/futures, initiation/result separation,
  function alias resolution, cancellation after initiation, child-only signals,
  ordinary SDK/isolate interoperability, retries and continue-as-new.
- `Future.ToChannel()` remains the explicitly requested native select adapter;
  results expose `converter.EncodedValue` alongside the error. `Future.Get`
  remains available and repeatable.
- Additional API coverage: external workflow signals/cancellation, host local
  activities with markers and durable retry backoff, SDK session-worker protocol
  with native session contexts, and Nexus clients/execution/result futures with
  all SDK cancellation policies. SideEffect/MutableSideEffect are intentionally
  excluded; nondeterministic work belongs in activities.

Host SDK protocol callbacks only queue copied bytes. Normal workflow execution
resumes at the runtime's task suspension fence. Queries and validators use a
separate read-only service while normal workflow goroutines remain suspended.
Accepted updates execute normally; validation rejects writes and durable work.
Handler registration yields to updates that arrived before registration.

Errors and payloads are decoded under the receiving owner's allocator. Child
memo and untyped visibility values remain encoded through the host boundary,
avoiding precision loss from JSON decoding into `interface{}`. Child initiation
sentinels retain public `errors.As` identity through explicit transport tags.
Closing or evicting a definition retires callback cells without generating
server cancellation. Parent completion uses the SDK's parent-close policy.

## Validation

Local coverage includes SDK/sample package suites, workflow/bridge/worker race
tests, ownership-checked native drivers at GOMAXPROCS 1/2/8 and GOGC=1, live
Temporal recording, and fresh-process replay of checked-in histories.

The update history test audits computed completion payloads/failures against
recorded outcomes and rejects a deliberately corrupted outcome. Child history
tests verify computed workflow results, cancellation, structured failure details,
duplicate starts, retry metadata and continued runs. Live retry tests inspect
the child's final attempt. Close tests reject late callback resurrection and
check that eviction does not cancel server-side work.

Native Linux/macOS arm64/amd64 CI includes read-only runtime/compiler conformance,
SDK/sample suites, and compiled ownership-checked query/update/child race drivers.
Results must be checked for the specific pushed revisions; local validation
does not establish native platform completion.

## Explicit POC exclusions

- Arbitrary protobuf values, custom headers/context propagation, custom failure
  converter implementations and per-operation value-serializer contexts remain
  deferred. Batch codecs that change payload count are not supported.
- Nonempty child typed search attributes are rejected explicitly. Their pinned
  SDK representation uses interface-key maps; deterministic iteration needs
  separate support. Ordinary memo/untyped search attributes are supported.
- Isolate signal channels keep their existing byte-slice input convention.
- Validator panic errors have an empty SDK panic-detail stack; query/validator
  containment of a non-yielding CPU loop remains a productization limitation.
- Runtime/library restrictions, unsupported map key kinds, trusted-code scope,
  static dependency versions and the deferred worker-stop cache policy remain.
- This checkpoint covers the delivered integration features below; it does
  not claim implementation of every Temporal SDK operation or interceptor.
- Local activity implementations must be registered on the host worker. Offline
  replay needs no implementations. Session IDs are deterministic original-run
  IDs plus sequence; legacy SDK UUID SideEffect session histories are not migrated.

## Additional API coverage and observability

The SDK's `example/apicoverage/check` exercises the added APIs with ownership
checks at GOMAXPROCS 1/2/8, live execution and saved-history replay with computed
results. Local activity failures preserve details and retries across durable
timers. Session failure is tested by stopping a dedicated session worker while
the workflow worker stays active. External commands interoperate with ordinary
SDK workflows. Nexus tests cover synchronous/asynchronous operations, structured
failures, separate execution tokens, and all four cancellation policies.

Queries install their host router at execution setup, so an early query returns
an error before its application handler has registered instead of panicking the
SDK's task processor.

Printing/standard logging already use worker-configured replay-aware host calls.
SDK logger/metrics interfaces and tracing remain feature 10 work. The SDK's
`API_COVERAGE_PLAN.md` proposes copied one-way sink messages with replay suppression
and bounded best-effort delivery, following TypeScript's sinks model. Host sink
objects never enter an isolate and sink outcomes must not affect decisions.

## Worker-configured serialization (feature 8)

`worker.SetIsolateDataConverter` configures a marked
`func([]byte) (converter.DataConverter, error)` factory and copied configuration
on workers and replayers. Registration can precede configuration; each execution
snapshots it. The factory runs under the workflow owner before argument decoding.
`isolate.Handle.ProgramWithSupport` composes compiler-created state descriptors
for the workflow and factory, with one initializer per shared dependency. No
host-owned converter object or capturing factory closure crosses the boundary.

Query/validator conversions use freshly constructed scratch-owned serializers.
Their caches can mutate locally while retained workflow state remains read-only.
Factories and user marshal/error callbacks have the same compulsory ownership
and effect restrictions as workflow code. Default behavior and ordinary SDK
workflows are preserved.

The client's/replayer's ordinary host DataConverter applies codecs to RawValue
payloads, skipping application-value serialization. Plain protobuf Payloads cross
Call. This supports host compression, encryption/randomness and remote codec I/O
without sharing keys, clients or their Go objects with an isolate. Activity and
child results, signals, queries, updates, continuation and failure cause-chain
payloads use that boundary. SDK execution handles use an isolate-owned built-in
JSON converter; search attributes use SDK default serialization.

The adapter supplies SDK serialization contexts on the host and determines child
IDs before encoding. The pinned SDK's private current-run-ID string is read on
the host to retain its ID convention; a public bindings hook remains a TODO.
Codecs must decode self-describing historical payloads: SDK failure conversion
may omit context and standalone replay uses synthetic namespace/execution IDs.
The initial bridge uses the SDK's single-payload contract; cardinality-changing
batch codecs are deferred and codec errors fail Workflow Tasks.

Validation includes separate-package state composition (normal/race compiler
scripts), all SDK packages, remote HTTP codec transport and encrypted common
failure attributes, ownership-checked custom serializer/cache/query checks, live
AES-GCM encrypted activities/signals/queries/updates/children/continuation,
ordinary SDK interoperability, and encrypted history replay with exact results.
See the SDK README for the configuration API, restrictions and commands.
