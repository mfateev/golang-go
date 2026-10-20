# Deterministic execution

`Config.Deterministic` enables the native scheduling/select/map/random contract
before initialization. The Temporal adapter also supplies an initial history
time and a timer operation. Deterministic mode without a configured clock rejects
clock reads and sleeps rather than observing wall time.

## Native dispatch

Each group has one execution token and FIFO readiness queues. Only the token
holder is published into normal Go run queues. A parent continues after `go`;
children enqueue in creation order. Committed user channel/select/sync waits,
`runtime.Gosched` and goroutine exit transfer the token. Preemption, stack work,
GC and runtime-internal waits retain it, so wall time and processor assignment
do not choose a different application goroutine. Different instances and host
work still run concurrently.

High-priority readiness is considered before normal FIFO readiness. Priority
survives blocking; an ordinary `go` child has normal priority. Inbound and
read-only roles have separate admission fences. Priority does not preempt a
running member or bypass a role fence. Pending joins count as work before token
acquisition, preventing a false idle report from `Start` or `Suspend`.

`Suspend` waits for the admitted queue/token to become idle and then closes
admission. Service `Commands` concurrently or a blocked transport handshake can
prevent idleness. Deliver ordered replies and events while fenced, resume the
required role, drain its commands and suspend again. Return to normal admission
only after inbound/priority work is quiescent. Do not infer idleness from a
channel poll, sleep or goroutine count. Native waits still require correct Go
synchronization; deterministic dispatch is not permission for data races.

## Select and maps

Native and reflected select shuffle polling from the instance stream. The same
seed, input readiness and dispatch trace reproduce the choices. Byte `Call`
reserves its four transport poll draws before its first park so delayed host
receipt cannot interleave those draws with application randomness. Inbound
`ControlCall` polls use fixed ordering and consume no workflow draws.

Native/reflected map iteration takes an ascending snapshot of integer/string
keys, including named types. Deleted keys are skipped, current values are read
when visited, and new keys are excluded until the next iteration. Hashing and
hash-flood protections keep ordinary Go implementations: sorted observation
removes hash-table layout from replay behavior.

Pointer, uintptr, boolean, floating-point, complex, composite and arbitrary
interface keys cannot be iterated, including on empty maps. Temporal typed
search attributes use the SDK-owned `workflow.SearchAttributeEntries` binding
and key index for canonical name/type traversal. There is no SDK key-type
exception or package knowledge in the Go map runtime.

`sync.Map.Range` snapshots keys of one concrete integer/string type, sorts them
and loads current values before callbacks. Empty maps work; mixed/unsupported
key types fail before callbacks. `iter.Pull`/`Pull2` use channel handshakes inside
the dispatcher instead of direct runtime coroutine switching. Iterator runners
inherit owner, limits and lifecycle membership.

## Clock and timers

The host installs Unix nanoseconds before init and advances nondecreasing time
at quiescent task boundaries. `time.Now` returns UTC; its wall/monotonic behavior
must agree with `Sub`, `Since`, `Until` and context deadlines. Runtime scheduling,
GC, watchdogs and profiling retain real clocks.

`time.Sleep`, `After`, `NewTimer`, `AfterFunc` and standard-context deadlines use
host timer requests. The generic request is a decimal signed nanosecond duration;
completion follows host-recorded events. Timer channel `len`/`cap` appear zero,
including reflection. Nonpositive timers are ready immediately. Timer generation
checks prevent stale delivery after Stop/Reset. Stop/Reset suppress local delivery
but do not cancel an already emitted durable host timer. Tickers are unsupported.
Initializers cannot issue durable waits before the host dispatcher starts.

Default Local is UTC and does not read `TZ` or the machine timezone database.
`LoadLocation("UTC")` and `LoadLocation("Local")` work; machine database lookups
are denied. `FixedZone` and explicitly supplied TZif data remain value operations.

## Randomness and numerical behavior

Top-level `math/rand`, `math/rand/v2`, default `crypto/rand.Reader`, `Read`, `Int`,
`Text` and select share a ChaCha8 stream installed before init. Zero seed is a
fixed default. The Temporal adapter hashes original history run identity into
the seed; replay of that run sees the same bytes. Precisely, hash the bytes
`isolate/crypto-rand/v1`, then the uint64 little-endian length of OriginalRunID
(falling back to execution RunID), then that UTF-8 run ID with SHA-256. A shared
stream read buffers leftover bytes and integer draws consume the next eight bytes
little-endian, including partial-read leftovers. Read-only stream seeding XORs
seed byte zero with 0x80; it never changes the workflow stream. Suspension preserves stream
position. Explicit local generators retain their own seeds; top-level legacy
`Seed` is a no-op independent of host GODEBUG. Query/validator scratch streams do
not advance the workflow stream.

SDK `workflow.GetRandomStream` supplies independent named streams with its
specified ChaCha8/distribution behavior. Reset identity is delivered at a task fence;
streams created afterward and generated child IDs use the new identity. See the
SDK reference for namespaces and call semantics.

Replay seeds are public/predictable. Crypto-named APIs provide no secret entropy
inside workflows; generate secrets and perform encryption in host activities or
codecs. `crypto/rand.Prime`, direct OS/DRBG entropy and key generation are outside
the supported private contract.

`math.Log`/`Exp` and normal/exponential random-distribution paths pin explicit
IEEE fused rounding needed by the retained replay corpus. Host math keeps its
architecture implementations. This is not portability for every math function
or arbitrary floating-point expression.

## Observable limits and validation

`sync.Pool` never exposes GC/P-dependent cached values inside an instance.
Finalizers, weak/unique observations, runtime machine metrics and unsupported
ambient effects are rejected. Pointer address observations are not stable replay
identities. Protobuf ordering/default overlays must not consume application random
bytes during descriptor construction.

The SDK determinism checker records the actual trace in an activity result and
completion, compares freshly computed replay observations, and rejects corrupted
outcomes. Run it at `GOMAXPROCS=1,2,8`, with optional CPU features disabled and
different host time zones. A successful Temporal replayer alone is insufficient
when it does not compare activity input payloads. Numeric contract versions do
not prove all future versions replay identically.

Primary implementation: `runtime/isolate_dispatch*.go`, `isolate_priority.go`,
`isolate_inbound.go`, `isolate_readonly.go`, `isolate_determinism.go`,
`isolate_crypto_rand.go`, `internal/runtime/maps`, `sync/map.go`, `iter`, `time`,
`math`, `math/rand` and `math/rand/v2` under `src/`.
