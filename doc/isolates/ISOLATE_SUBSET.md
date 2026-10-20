# Supported Go operations and effects

The POC supports reviewed Go workflow code built through the isolate manifest.
It enforces ownership and a defined effect policy; it does not establish hostile
native-code containment. Host execution and activities retain ordinary behavior.
A dependency import is not a guarantee that every operation it exposes is usable.

## Language and library behavior

| Area | Current private behavior |
| --- | --- |
| Functions, interfaces, closures, generics | Ordinary Go values with ownership/provenance checks |
| Native `go`, channels, select, mutexes, RWMutex, Cond, WaitGroup, atomics | Supported within an owner; deterministic token handoff in deterministic mode |
| Panic, recover, defer | Normal Go semantics for application panics; permanent faults bypass application recovery/defers |
| Maps | Owned access; canonical integer/string iteration; SDK typed-attribute traversal uses its own binding |
| Reflection | Owned value operations and audited immutable type construction; unsafe address APIs require audited implementation provenance |
| Strings, bytes, formatting, JSON, regexp, common encodings | Selected private library state and explicit immutable tables |
| Embedded data | Strings use immutable literals; byte slices get private storage before init |
| Pooling | `sync.Pool` behaves empty; local `New` still works |
| Time/context | History clock, durable native timers and context cancellation; no tickers or machine timezone database |
| Randomness | Replay-seeded supported random APIs; never cryptographic entropy |
| Standard printing, `log`, default `slog` | Format privately and send best-effort copied observations |
| `os.Exit`, `syscall.Exit` | Terminate only the current isolate |

## Effect policy

The compiler and runtime share `src/internal/isolatepolicy/effects.go`. Effect
checks are compulsory in a manifest build, including process-initialized
libraries. Recognizable entry violations can be compile diagnostics; helpers,
indirect/reflected calls and native wrappers are checked when executed privately.
Unreachable host-only diagnostic paths in a dependency need not reject the entire
library. A permanent violation is reported before the external operation occurs.

Denied operations include filesystem/environment access, networking, subprocesses,
process signals/plugins, process-wide runtime configuration, machine/GC-dependent
observations, profiling, application finalizers/cleanups, weak/unique observations,
direct syscalls and unaudited unsafe/native escape paths. Pure value parsers and
formatters remain usable when they do not touch unselected process state.

Workflow configuration comes from inputs or copied factory configuration. Perform
external work in host activities or explicit SDK durable operations. `Call` is
available only after startup; it does not grant access to host memory. Query and
validator handlers additionally reject durable work, child goroutines and waits.

## Implementation provenance

Unsafe pointer operations, unauthorized linkname declarations and external native
bodies conservatively guard their function before private execution. Ordinary
host invocation remains usable. GOROOT implementation provenance and explicit
package/file audits permit necessary library internals; they do not disable
ownership, forbidden effects or application callback checks. Constant-only unsafe
size/alignment operations do not fabricate pointers.

Audits use physical source paths and survive exported generic declarations,
instantiation and inlining. `//line`, package-name impersonation, generated-looking
function names and forged entry adapters cannot manufacture authority. Generated
entry aliases are restricted to manifest wiring. `reflect` dynamic calls and
registered defer/go targets validate the actual target before invocation.

The SDK validates exact protobuf and OTel source versions before issuing audits
and applies small deterministic/default overlays. The Go core knows only generic
audit inputs and GOROOT policy. An application can import a generated message
package without granting its other source files a generated-code audit.

## Failures and limits

Ownership/effect/resource faults revoke the group permanently and produce copied
host diagnostics. Application `recover` and application defers cannot turn them
into results. Read-only writes to workflow data are a separate recoverable
handler rejection. The SDK uses `BlockWorkflow` to report permanent faults as
Workflow Task failures; `FailWorkflow` is rejected for native registrations.

CPU loops without a supported execution fence and uninterruptible native code can
leave kill pending. Quotas are charged-memory/goroutine limits and host watchdogs,
not RSS or exact CPU quotas. External native memory, dynamically loaded programs,
snapshot restoration and complete standard-library support are not implemented.
See [lifecycle](LIFECYCLE.md), [resources](RESOURCE_CONTROLS.md) and
[validation](VALIDATION.md) for the exact fences and checks.
