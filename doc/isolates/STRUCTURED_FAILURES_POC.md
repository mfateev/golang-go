# Structured Temporal failures checkpoint

Feature 7 remains in progress. Activities and workflow completion now preserve
the pinned Temporal SDK's actual error types, cause chains, details, heartbeat
details, retry states, and next retry delays across the copied-byte boundary.
Continue-as-new retains its separate command path.

## Ownership

The compiler intercepts `proto.Clone` only in an audited isolate build using
protobuf v1.36.11. Ordinary host calls execute protobuf's original code. The
isolate path clones generated message values under the caller's allocator using
reflection, without reading the shared lazy coder tables or invoking message
callbacks. The default process builder records generated message and oneof
wrapper type provenance. This registration grants no access to message storage.
Source reads and destination stores remain subject to ownership checks.

Mutable message contents, maps, repeated values, and unknown bytes are copied.
Typed nils and protobuf empty-container semantics are retained. Extensions and
custom message implementations are outside this path. Existing private registry,
private MessageInfo, custom descriptor, registry mutation, and callback guards
remain active.

Temporal SDK v1.49.0 is now pinned by the metadata manifest. Its startup-derived
`goErrType` cell has a bounded read permission. Its `ErrNoData` sentinel is
published at process initialization as one precisely validated immutable
`errors.errorString`, preserving SDK identity without approving other errors or
arbitrary shared objects. Public and internal sentinel cells permit reads only.

## Transport

The SDK adapter transports protobuf-encoded Temporal `Failure` messages. A small
codec handles the pinned schema without invoking shared protobuf coder caches
inside an isolate. The actual SDK default failure converter reconstructs errors
under the receiving owner's allocator. The existing default data converter
decodes ordinary JSON, byte, and null details. Native context cancellation also
supports `errors.Is`, while `errors.As` can still reach the original SDK error.

Unknown wire fields and external payload references are rejected. General
protobuf workflow arguments/results and custom data/failure converters remain
feature 8 work. Malformed wire errors are created under the caller's owner;
protobuf's process-owned parse sentinels are not returned to an isolate.

## Validation

- All pinned Failure variants cross-checked against standard protobuf encoding
  and decoding, including integers above JSON's exact floating-point range,
  nested causes, encoded attributes, oneof merging, and malformed input.
- Native ownership-checked race driver validates ActivityError → TimeoutError →
  ApplicationError through both activity futures and workflow completion, with
  two active isolates and host conversion at GOMAXPROCS 1/2/8.
- Native tests cover clone independence, nil/empty values, missing-detail
  sentinel identity, custom clone rejection, and malformed nested payloads.
  The driver also passes with GOGC=1.
- Targeted runtime/isolate/reflection race tests and compiler function,
  ownership, heap, and metadata build scripts pass.
- Full SDK and tracked sample suites pass. Failure/payload codecs, workflow,
  bridge, and worker race suites pass.

The four-platform native CI matrix and live-server failure-history replay are
not included in this local checkpoint's validation. Children, queries, and
updates still block completion of feature 7.
