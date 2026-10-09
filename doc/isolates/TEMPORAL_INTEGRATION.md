# Temporal integration: feature 7

The POC uses Temporal Go SDK v1.49.0 host bindings with statically compiled
`//go:isolate` workflow functions. Native contexts, goroutines, channels,
select and deterministic time replace SDK execution constructs. Ordinary SDK
workflows and host activities retain their registration and execution behavior.

## Delivered API and behavior

- Activities: SDK options, function references/aliases, repeatable futures,
  cancellation policy, structured errors and default-converter results.
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

- Worker-configured data/failure converters, codecs, arbitrary protobuf values,
  custom headers and context propagation retain the feature 8 audit.
- Nonempty child typed search attributes are rejected explicitly. Their pinned
  SDK representation uses interface-key maps; deterministic iteration needs
  separate support. Ordinary memo/untyped search attributes are supported.
- Isolate signal channels keep their existing byte-slice input convention.
- Validator panic errors have an empty SDK panic-detail stack; query/validator
  containment of a non-yielding CPU loop remains a productization limitation.
- Runtime/library restrictions, unsupported map key kinds, trusted-code scope,
  static dependency versions and the deferred worker-stop cache policy remain.
- This checkpoint covers the feature 7 list in the productization plan; it does
  not claim implementation of every Temporal SDK operation or interceptor.
