# Host communication

The boundary accepts copied bytes. Application values, channels, closures,
protobuf messages, allocators and arbitrary host callbacks stay in their owner.
Typed entry invocation is local to the instance and is not a typed host API.

| Operation | Caller and behavior |
| --- | --- |
| `Call(op, payload)` | Writable application goroutine; copy request to host, wait, copy reply to caller |
| `ControlCall(op, payload)` | Deterministic inbound service; same copying, fixed transport polls without workflow random draws |
| `ReadOnlyCall(op, payload)` | Dedicated read-only service; scratch ownership after first reply, admitted by host read-only resume |
| `Write(op, payload)` | Observational output; copy to bounded host queue, no acknowledgment |

The host consumes `Commands()` and calls each `Command.Reply` exactly once.
Errors cross as copied text, never an error object retaining foreign state.
Request/reply infrastructure is host-owned; copies returned to the instance use
its active owner. Halt wakes Calls independently of native waiter teardown.
Service commands concurrently while waiting for `Suspend`.

`Write` preserves no ordering relative to Call. The queue holds 64 messages of
at most 64 KiB each; full queues and oversized messages drop output. The host
can inspect `DroppedWrites()`. `Writes()` is not closed on completion: drain
buffered messages after `Done` when final logs matter. Delivery, sink errors and
drops cannot become workflow decisions. Standard logging reserves `LogOp`.

## SDK binary protocol

The Go core does not prescribe application payload formats. The Temporal SDK
uses `internal/transportpb/transport.proto`, generated protobuf bindings and
static Go conversions. Envelopes contain Temporal `Payloads`, `Header` and
`Failure` as submessages; application payload encodings remain converter-owned.
Scalar identifier messages use fixed-width binary integers or raw strings.
Runtime timer waits use decimal signed nanoseconds; durable session tokens keep
their specified token format. No JSON/base64 transport envelope surrounds the
already serialized payloads.

The startup request and response validate a 20-byte header before invoking
factories or decoding application input:

```text
0..3   ASCII ISOL
4..7   uint32 little-endian protocol = 7
8..11  uint32 little-endian metadata = 1
12..15 uint32 little-endian API = 1
16..19 uint32 little-endian determinism = 1
20..   startup protobuf body
```

This header is startup metadata, not a blanket framing requirement for every
operation or a Temporal history event. Both directions reject missing, truncated
or mismatched markers. Host and workflow implementations are built together.
The SDK host binding contract is separately `IntegrationVersion = 2`.

## Serialization and library state

Data converters run inside the workflow owner. Host codecs operate on opaque
payloads through RawValue handling and may do I/O, compression or encryption.
Headers and observations bypass application converters. Structured failures
preserve the Temporal failure tree and detail payloads; generic runtime Call
errors alone do not preserve concrete error types.

Each workflow and scratch service owns its protobuf registry/descriptors/coder
caches. Host registrations/extensions are invisible. `Any` needs its type linked
and registered privately. Binary protobuf preserves unknown fields; JSON follows
the configured SDK converter. Captured messages are snapshotted before read-only
clone/conversion so mutable reflection caches remain scratch-owned.

Source: `src/internal/isolatebridge/{bridge,write,log}.go`, public
`src/isolate/isolate.go`, SDK `workflow/wire.go`, `temporalbridge/wire.go`,
`internal/transportpb`, `internal/integrationcontract`, `internal/payloadcodec`
and `internal/failurecodec`. See the SDK implementation guide for the operation
inventory and schema-generation commands.
