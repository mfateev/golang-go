# Host integration

The typed host integration API has been removed. Host communication uses the
copied-byte `Call`, `ControlCall`, `ReadOnlyCall` and observational `Write` APIs.
See [the runtime API](RUNTIME_API.md#copied-byte-communication-and-logging).

`NewHostFunc`, `HostContext`, exported allocators, construction scopes, reentry
and public waiters no longer exist. Their dedicated compiler/runtime paths,
asynchronous borrow tracking and trusted reference-sharing mode were deleted.
Ordinary scheduling, resource limits, ownership checks, publication checks and
revocation remain active.

Protobuf executes with private descriptors, registries and coder tables in each
workflow. Query services initialize their own scratch library state and snapshot
captured message data before cloning/conversion. No protobuf object is passed by
reference between the host and an isolate. The SDK uses generated protobuf
transport submessages and fixed-width binary identifiers.

The implementation and measurement gates are recorded in
[the independence plan](ISOLATE_INDEPENDENCE_PLAN.md).
