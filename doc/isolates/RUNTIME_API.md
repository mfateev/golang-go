# Implemented Go isolate API reference

This document specifies the APIs added by this Go fork for native isolates. It
covers the entire exported `isolate` package, including methods inherited through
public type aliases, and the Go command's isolate build interface. It describes
the current implementation, rather than the historical API proposals elsewhere
in this directory. Checked against the source on 2026-10-17.

These are experimental POC APIs requiring `GOEXPERIMENT=isolates`. There is one
current implementation; the version markers do not promise compatibility with
older revisions. Source revisions must be pinned separately. The implementation
uses one process and the shared Go garbage collector, with allocation ownership,
private package state and execution controls. It is a trusted execution facility,
not a process security sandbox.

## Ownership and execution contexts

| Context | Package globals | New allocations | Existing values |
| --- | --- | --- | --- |
| Host | Process state | Host owner | Host access |
| Application | Selected private package state | Instance owner | Ownership checks |
| Read-only handler | Workflow state and selected scratch state | Scratch owner | Workflow reads; no workflow writes |
| Registered host function | Process state | Host owner | Explicit synchronous borrowing of caller arguments |
| `Allocate(a, build)` | Process state | Allocator `a` | No new authority to access preexisting private values |
| `HostContext.Reenter` | Original caller's state | Original caller's owner | Original ownership, effects and read-only restrictions |

An allocator selects ownership of new allocations even though physical memory
comes from the shared heap. It does not move an existing graph, copy a value,
switch application globals, or grant execution authority. A host-owned handle
can cross the typed boundary without allowing application code to read its
fields. SDK adapters must construct private results or explicitly mediate access
to shared host objects. Host caches must never retain borrowed private objects.

## Entry discovery and typed invocation

```go
type Handle struct { /* opaque */ }
type Program struct { /* opaque */ }
type Value = isolatebridge.Value // public alias; fields shown below

// Value's exported fields:
// Value   any
// Pointer any

func LookupFunction(fn any) (Handle, bool)
func (h Handle) Name() string
func (h Handle) Signature() reflect.Type
func (h Handle) MetadataVersion() uint32
func (h Handle) Invoke(decode, encode func(...Value) error) error
func (h Handle) Program(dispatch func()) Program
func (h Handle) ProgramWithHandle(dispatch func(Handle)) Program
func (h Handle) ProgramWithSupport(support Handle, dispatch func(Handle, Handle)) Program
func (h Handle) ProgramWithSupports(supports []Handle, dispatch func(Handle, []Handle)) Program
func (p Program) Name() string
func (p Program) MetadataVersion() uint32
```

`LookupFunction` is a host operation. It succeeds only for a function registered
through the explicit build manifest; assigning the original function to another
variable preserves its identity. It does not create an instance. `Name` is the
fully qualified function name; `Signature` is its original Go type.

A `Program` combines compiler-created package state with an SDK dispatcher.
`ProgramWithHandle` passes the handle through a trusted entry wrapper, permitting
a noncapturing dispatcher. The support variants combine additional marked
functions' package state; shared dependencies initialize once. The plural variant
snapshots the support list and supplies an isolate-owned copy in the original
order. These wrappers transfer compiler metadata, not arbitrary host closure
state. Other captured values still face ownership checks.

`Invoke` runs inside an instance and calls the original typed function through
the generated invoker. `decode` receives argument slots and fills their typed
`Pointer` values. `encode` receives zero or one result slot and reads its `Value`.
A function error is returned without invoking `encode`. Conversion callbacks run
inside the instance and must not retain slots in the host. SDKs own serialization
and the outer request/completion protocol.

Source: [function.go](../../src/isolate/function.go),
[function_state.go](../../src/isolate/function_state.go).

## Instance creation and configuration

```go
type Config struct {
    Program        Program
    Deterministic  bool
    RandomSeed     [32]byte
    InitialTime    *time.Time
    TimerOp        uint32
    ResourceLimits ResourceLimits
    LogHandler     func(LogRecord)
}

type Isolate struct { /* opaque */ }

func New(cfg Config) (*Isolate, error)
func NewContext(ctx context.Context, cfg Config) (*Isolate, error)
func (i *Isolate) Start() error
```

`New` prepares private state and runs package initializers with an unbounded
initialization context. `NewContext` observes cancellation during preparation;
it requires a non-nil context. A zero or unregistered program is invalid.
Initializers may use configured logging; application host calls require `Start`.
`Start` runs the dispatcher on a new goroutine and may be called once.

| Field | Contract |
| --- | --- |
| `Program` | Statically linked entry and compiler-created private state factory. |
| `Deterministic` | Enables FIFO native dispatch within each priority, deterministic select/map iteration and instance random streams, including initialization. |
| `RandomSeed` | Seeds select and top-level `math/rand`, `math/rand/v2` and `crypto/rand` before initialization. Zero selects a fixed default. Nonzero requires deterministic mode. Supply a replay-stable execution seed. Bytes are predictable and unsuitable for cryptographic secrets. |
| `InitialTime` | Non-nil enables the host clock before initialization. Requires a valid `TimerOp`. |
| `TimerOp` | Nonzero SDK operation for durable timer waits; must differ from `LogOp`. |
| `ResourceLimits` | Installed before initialization; zero fields mean unlimited. |
| `LogHandler` | Enables standard logging transport and receives initialization output. After start, output arrives on `Writes()`. |

Failed startup revokes attached goroutines and attempts cleanup for at most
100 ms. If cleanup is still pending, `InitializationError` exposes its cause,
pending diagnostics and a completion notification. Do not infer completed
cleanup solely from `NewContext` returning an error.

Source: [host.go](../../src/isolate/host.go).

## Scheduling, task boundaries and time

```go
func (i *Isolate) Suspend() error
func (i *Isolate) Resume() error
func (i *Isolate) ResumeInbound() error
func (i *Isolate) ResumePriority() error
func (i *Isolate) ResumeReadOnly() error
func (i *Isolate) FenceReadOnly()
func (i *Isolate) AdvanceTime(t time.Time) error
func (i *Isolate) PendingCalls() int64

type Priority uint8
const (
    PriorityNormal Priority = iota
    PriorityHigh
)

func GoWithPriority(priority Priority, fn func())
func GoInbound(fn func())
func RunInbound(fn func())
func MarkServiceGoroutine()
func IsActive() bool
func IsReadOnly() bool
```

`Suspend` waits until deterministic instance goroutines block, then fences
admission. The host must service `Commands()` concurrently while waiting.
Deliver task input and host replies while fenced, then resume the appropriate
role. `Resume` restores normal dispatch. Narrow admission methods behave as
follows:

| Method | Admitted work |
| --- | --- |
| `ResumeInbound` | SDK input-delivery services and their children; ordinary application continuations stay queued. |
| `ResumePriority` | High-priority writable goroutines; ordinary continuations stay queued. |
| `ResumeReadOnly` | Read-only service goroutines; application continuations keep their FIFO positions. |
| `FenceReadOnly` | Restricts admission to read-only services at the next park, including during suspension; does not start services or drive the host protocol. |

For each narrow resume, service commands and suspend until that work is idle
before returning to normal dispatch. Admission controls are cooperative; priority
does not preempt a running goroutine or override a role fence.

`GoWithPriority` starts a goroutine at the requested priority. High-priority work
runs first whenever a token is released; each priority has FIFO readiness order.
Priority survives blocking, but children started with ordinary `go` have normal
priority. `GoInbound` starts an SDK delivery service. `RunInbound` temporarily
runs a hook in that role; goroutines it starts inherit the inbound role. Outside
deterministic dispatch these helpers retain ordinary goroutine execution.

`MarkServiceGoroutine` hides the current SDK helper from workflow stack traces
without changing scheduling. Children do not inherit that marker. Read-only and
inbound services are already hidden. Application code should use ordinary `go`;
service-role APIs belong to SDK integration.

`IsActive` includes initialization and read-only execution but exposes no
instance identity. `IsReadOnly` identifies query/validator execution.

`AdvanceTime` requires a configured host clock. Publish a nondecreasing history
timestamp while quiescent, before replying to task commands. Durable timer requests
use `TimerOp` and a decimal signed nanosecond duration payload (a JSON integer);
the host replies when the recorded event permits completion. Standard time APIs
use these runtime facilities without adding exported names to package `time`.

`PendingCalls` counts outstanding host requests. Inspect it after suspension;
a live, blocked deterministic instance without pending host calls has no external
host event to make progress under this contract.

Source: [host.go](../../src/isolate/host.go),
[isolate.go](../../src/isolate/isolate.go),
[clock transport](../../src/internal/isolatebridge/bridge.go).

## Copied-byte communication and logging

```go
func Call(op uint32, payload []byte) ([]byte, error)
func ControlCall(op uint32, payload []byte) ([]byte, error)
func ReadOnlyCall(op uint32, payload []byte) ([]byte, error)
func Write(op uint32, payload []byte)

type Command = isolatebridge.Command
// Exported fields: ID uint64; Op uint32; Payload []byte
func (c *Command) Reply(payload []byte, err error)

type Message = isolatebridge.Message
// Exported fields: Op uint32; Payload []byte

func (i *Isolate) Commands() <-chan *Command
func (i *Isolate) Writes() <-chan *Message
func (i *Isolate) DroppedWrites() uint64

const LogOp = 0xffff0001 // uint32

type LogRecord struct { Source, Message string }
func DecodeLog(payload []byte) (LogRecord, error)
```

`Call` copies the request into host memory, blocks for a reply, and copies the
response into the caller. `Command.Reply` must be called exactly once. Errors
cross as copied diagnostic text, not the original error object or concrete type.
Operation numbers and application payload formats belong to the SDK.

`ControlCall` is for deterministic SDK inbound services (`GoInbound` or
`RunInbound`); fixed transport select ordering avoids consuming the application
random stream. It is forbidden from ordinary workflow goroutines and read-only
handlers. `Call` is also forbidden from read-only handlers.

`ReadOnlyCall` establishes a dedicated read-only service. After its first reply,
allocations use scratch ownership. Existing workflow state is readable, never
writable, including through callbacks. The service cannot start children or use
blocking native channel operations. The host admits it with `ResumeReadOnly`.

`Write` sends copied observational output without waiting for delivery. It is
allowed in read-only handlers. Its order is independent of `Call`; queue delivery,
backend errors and drop counts never become application inputs. The current
queue holds 64 messages, drops messages larger than 64 KiB or arriving when full,
and reports losses to the host through `DroppedWrites`. Use `Call` for durable
operations and external decisions.

`Writes` remains readable after `Done`; it is not closed. Drain buffered messages
on completion when final output matters. Do not rely on channel closure to detect
instance completion. `LogOp` is reserved for standard printing/logging. `DecodeLog`
validates its payload and returns a source (`fmt`, `log`, `slog` or `builtin`)
and formatted message. Formatting occurs inside the instance; sink configuration
and sink errors remain host concerns.

Source: [isolate.go](../../src/isolate/isolate.go),
[log.go](../../src/isolate/log.go),
[bridge.go](../../src/internal/isolatebridge/bridge.go),
[write.go](../../src/internal/isolatebridge/write.go).

## Typed host operations and scoped allocation

```go
type HostContext struct { /* opaque */ }
type Allocator struct { /* opaque */ }

func NewHostFunc[In, Out any](fn func(HostContext, In) (Out, error)) func(In) (Out, error)
func (c HostContext) Allocator() Allocator
func (i *Isolate) Allocator() Allocator
func HostAllocator() Allocator
func Allocate[T any](a Allocator, build func() T) (result T, err error)
func (c HostContext) Reenter(fn func())
```

Register `NewHostFunc` during host setup. Its returned typed function requires
an isolate caller. The host operation executes synchronously on that caller's
goroutine using host globals and host allocation; it may borrow caller arguments
until it returns. Arbitrary ordinary host functions do not gain this permission.
Input, output and error values are ordinary typed Go values: this boundary does
not automatically serialize or deep-copy them.

`HostContext` is valid only on that goroutine until the operation returns. Its
zero value is invalid. Use `c.Allocator()` to construct results owned by the
original caller, including a read-only caller's scratch domain. The independent
`i.Allocator()` is a host operation selecting the initialized instance's main
domain, usable before `Start` and while execution is fenced. `HostAllocator()`
selects host ownership for shared lazy initialization inside a private constructor.

`Allocate` applies the selected owner to `build` and synchronous helpers. Scopes
nest and restore on return or panic. Existing objects retain ownership; goroutines
started by the constructor inherit neither its scope nor borrowed access.
Construction consumes the target's resource budget and holds a lifetime lease.
A retained allocator keeps ownership metadata alive without reviving the instance.
Its zero value is invalid. Closing/revoking its domain prevents new construction
and suppresses delivery of an in-progress result with `ErrRevoked`.

`Reenter` invokes application callbacks synchronously with the original caller's
private globals, allocation owner, effect checks and read-only role. It does not
inherit an enclosing constructor's allocation override. Host state is restored
on panic. Calling arbitrary application callbacks directly while in a host
operation does not provide this context switch.

A panic escaping a registered operation runs host cleanup, produces copied
`PanicError` diagnostics, and revokes the instance. It never transfers an
arbitrary panic object. SDK adapters may handle their own expected errors inside
the host operation before returning an appropriately owned result/error.

Example host registration and private result construction:

```go
type Request struct { Text string }
type Result struct { Bytes []byte }

var convert = isolate.NewHostFunc(func(c isolate.HostContext, in Request) (*Result, error) {
    return isolate.Allocate(c.Allocator(), func() *Result {
        return &Result{Bytes: []byte(in.Text)}
    })
})
```

This pattern lets an SDK keep its converter, descriptor cache or other service
host-owned while allocating the returned object graph for the caller. All
returned private data must be created or copied in that allocation scope; merely
returning an existing host slice does not change its owner. Shared initialization
inside the constructor needs a nested host allocation scope.

Source: [host_integration.go](../../src/isolate/host_integration.go).
See [Host integration](HOST_INTEGRATION.md) for the design and constraints.

## External completion and parking

```go
type Waiter struct { /* opaque */ }
func NewWaiter() *Waiter
func (w *Waiter) Park()
func (w *Waiter) Wake()
```

`NewWaiter` associates one completion with the current isolate goroutine or the
original caller of a host operation. Its zero value is invalid; do not copy it
after first use. Only that goroutine may `Park`, once. Parking releases
deterministic dispatch so an SDK can wait for external host work without holding
the execution token.

`Wake` may run from any host goroutine, is idempotent, and may precede `Park`.
Writes before `Wake` happen before `Park` returns. Wakeup respects suspension and
role admission and cannot revive a revoked instance. A host worker must release
every borrow before waking, even after revocation; pending borrowing remains part
of the cleanup fence and can appear in `KillPendingError.PendingHostWork`.

Source: [host_integration.go](../../src/isolate/host_integration.go).

## Completion, revocation and diagnostics

```go
var ErrRevoked error

func (i *Isolate) Done() <-chan struct{}
func (i *Isolate) Wait() error
func (i *Isolate) Kill(ctx context.Context) error
func (i *Isolate) StackTrace() string
func (i *Isolate) TaskDiagnostics() string

type PanicError struct { Phase, Message, Stack string }
type GoexitError struct { Phase, Stack string }
type ExitError struct { Code int }
type OwnershipError struct { Reason, Stack string }
type EffectError struct { Operation, Stack string }
type KillPendingError struct {
    GoroutineID       uint64
    ThreadID          int64
    Stack             string
    LiveGoroutines    int32
    RunningGoroutines int32
    PendingHostWork   int32
}
type InitializationError struct {
    Cause   error
    Pending *KillPendingError
    Done    <-chan struct{}
}

func (e *PanicError) Error() string
func (e *GoexitError) Error() string
func (e *ExitError) Error() string
func (e *OwnershipError) Error() string
func (e *EffectError) Error() string
func (e *KillPendingError) Error() string
func (e *InitializationError) Error() string
func (e *InitializationError) Unwrap() error
```

Every error type above implements `Error() string` on its pointer receiver.
`(*InitializationError).Unwrap() error` returns `Cause`. `ResourceLimitError`,
described below, also implements `Error() string` on its pointer receiver.

`Done` closes only after a terminal outcome and all attached runtime cleanup.
After closure no later wakeup can execute instance code. `Wait` waits for that
fence and returns the terminal outcome, including an unrecovered child panic.
It may remain blocked on pending cleanup.

`Kill` permanently revokes execution, wakes supported native waits and host
calls, and waits for the full cleanup fence. A nil result is stable. A deadline
returns `KillPendingError`; revocation remains effective, and a later `Kill` can
wait again without resuming execution. `ErrRevoked` represents host-requested
termination before another terminal outcome is published.

`PanicError` contains copied diagnostics rather than the original panic value.
`GoexitError` applies to an initializer/entry root that exits without completing;
child `runtime.Goexit` retains ordinary Go behavior. Exit status zero completes
successfully; nonzero `os.Exit`/`syscall.Exit` produces `ExitError`.

`OwnershipError` and `EffectError` report permanent faults. Forbidden effects
are stopped before occurring. Application recovery and application defers cannot
resume a permanently faulted instance; host cleanup still runs through the
runtime fence. A recoverable SDK validation/query error is a separate adapter
policy, not permission to recover from permanent runtime faults.

`KillPendingError` reports remaining attached work. Running counts include
syscalls; pending host work includes construction leases and asynchronous borrows.
Samples are best effort; a busy stack is not suspended solely for diagnostics.
Retaining a completed handle or initialization notification preserves diagnostics,
not the completed private heap.

`StackTrace` is host-only and briefly stops process goroutines to capture live
application stacks, excluding SDK services and other instances. Suspend first for
a stable task boundary; a drained instance returns an empty string.
`TaskDiagnostics` additionally includes SDK services and the host task/suspension
state, excluding unrelated host goroutines and instances. Capture it before
`Kill` when investigating a stall.

Source: [host.go](../../src/isolate/host.go).

## Resource limits, usage and watchdog faults

```go
type ResourceLimits struct {
    MaxMemoryBytes uint64
    MaxGoroutines  uint32
}
type ResourceStats = isolatebridge.ResourceStats
// Exported fields:
// MemoryBytes, PeakMemoryBytes                           uint64
// HeapBytes, ReservedHeapBytes, StackBytes, RuntimeBytes uint64
// TotalAllocatedBytes                                    uint64
// LiveGoroutines, PeakGoroutines                         uint32
// RunningNanoseconds, Progress                          uint64

type ResourceLimitError struct {
    Resource     string
    Limit, Usage uint64
    Stats        ResourceStats
    Stack        string
}

func (e *ResourceLimitError) Error() string
func (i *Isolate) Resources() ResourceStats
func (i *Isolate) FailTaskDuration(limit, usage uint64) error
func (i *Isolate) FailProgress(limit, usage uint64) error
```

Zero limits are unlimited. Goroutine limits include initializer and entry roots.
Memory charges allocation slots until sweep, stacks and attributable runtime
metadata. Unused span capacity is reported as reserved heap, not charged memory;
this is not an instance RSS cap.

`Resources` independently samples counters without stopping the world. After
suspension, goroutine and stack counters are stable, but GC can still refund heap
and span charges. `TotalAllocatedBytes` is cumulative. `RunningNanoseconds`
counts completed scheduled intervals including metadata work and syscalls; it is
not hardware CPU time or a replay input. `Progress` is a host watchdog counter.

Native budget failures and host watchdog notifications produce the same permanent
fault/fence. `FailTaskDuration` and `FailProgress` publish supplied measurements;
the host implements measurement and deadline policy. `ResourceLimitError.Usage`
can represent a rejected attempted charge exceeding the accepted stats snapshot.
Use `Kill` to wait for cleanup or collect pending diagnostics after a fault.

Source: [resources.go](../../src/isolate/resources.go),
[resource counters](../../src/internal/isolatebridge/resources.go).

## Contract markers

```go
const (
    MetadataVersion    = 1
    APIVersion         = 1
    DeterminismVersion = 1
)
type Contract struct {
    Metadata    uint32 `json:"metadata"`
    API         uint32 `json:"api"`
    Determinism uint32 `json:"determinism"`
}
func CurrentContract() Contract
```

These numeric markers check the current compiler metadata, public API and
observable deterministic behavior. Metadata registration rejects unsupported
producers. They are not source revisions, compatibility implementations or proof
of replay equivalence. The POC may change them freely. Determinism 1 currently
uses one shared random/select stream. Pin the toolchain and SDK sources separately.

Source: [version.go](../../src/isolate/version.go),
[isolateabi/version.go](../../src/internal/isolateabi/version.go).

## Build manifest and report interfaces

```sh
GOEXPERIMENT=isolates go build -isolate-config=entries.json -isolate-report=report.json ./cmd/host
GOEXPERIMENT=isolates go test -isolate-config=entries.json ./...
```

`-isolate-config` supplies explicit entry selection, generated typed invokers and
package-state selection. The Go command does not discover SDK directives or
select external libraries on its own. The JSON format is version 1:

| Field | Shape and meaning |
| --- | --- |
| `format_version` | Integer `1`. |
| `entries` | Array of `{package, function, value, invoke}` objects; optional `for_test` selects the test variant. |
| `sources` | Map from package identity to generated adapter source text. Test identity is `package [for_test.test]`. |
| `state_packages` | Import paths requiring private package layouts. |
| `readonly_packages` | Subset of state packages recreated under read-only scratch ownership. |
| `support_packages` | Additional host-reachable roots included in entry state graphs. |

The generated accessor has signature `func() any` and returns the original entry.
The invoker has signature `func(decode, encode func(...isolate.Value) error) error`.
Both compile in the entry package with ordinary application instrumentation.
The compiler validates their signatures and constrained linkname declarations.
The manifest grants no metadata, allocation or callback privilege.

`-isolate-report` writes JSON with `format_version`, `metadata_version`,
`api_version`, `determinism_version`, `programs` and `packages`. Each program has
`name` and its `packages` list. Each package has `path`, `standard`,
`instance_state`, `host_reachable` and `classification`. Current classifications
are `instance-state`, `trusted-runtime` and `process-state-denied`. The report
explains selection; it does not change enforcement.

See [BUILD_CONTRACT.md](BUILD_CONTRACT.md) for source/import constraints, test
variants and cache behavior. SDK-owned directives, the `tgo` CLI and module
source adapters are outside the Go runtime API.

## Private implementation interfaces

There are no additional public exported functions in package `runtime` for
creating or driving isolates. Existing standard-library APIs retain their names;
the fork integrates their behavior with ownership, execution and effect checks.
The supported entry points are the public package and build contracts above.

| Private implementation | Responsibility |
| --- | --- |
| `internal/isolateabi` | Numeric metadata/API/determinism markers shared with build tools. |
| `internal/isolatestate` | Compiler-produced package layouts, initialization and private-state factories. |
| `internal/isolatebridge` | Entry registration, copied transport, runtime attachment and host control; selected data types are exposed through `isolate` aliases. |
| `runtime.isolate*` hooks | Ownership/allocation, dispatch/admission, host scopes, callbacks, parking, termination, resource accounting, clock/random and diagnostics. |
| Compiler instrumentation and metadata | Global relocation, memory publication/access checks, effect provenance and generated entry descriptors. |
| Standard-library metadata/frozen-library scopes | Audited sharing and canonical reflection caches within GOROOT. |

These hooks, descriptor layouts, compiler debug flags and test-only exports are
private implementation details, not APIs for external adapters. External code
must not import the internal packages or use linkname to claim their authority.
There is no public raw-owner setter, descriptor registration API, allocation
retagging operation or independent heap-release API. Lifetime is controlled by
instance cleanup and GC.

Temporal, protobuf and OpenTelemetry integration belongs to SDK source adapters
using `NewHostFunc`, `Allocate` and `Reenter`. The Go core has no generated-protobuf
classifier or module-specific allocation/metadata API. In particular,
`isolate.IsGeneratedProtobuf` was removed; it is not part of this reference.

To inspect the live public declarations after changes, run:

```sh
GOEXPERIMENT=isolates ./bin/go doc -all isolate
GOEXPERIMENT=isolates ./bin/go doc -all isolate.Command
GOEXPERIMENT=isolates ./bin/go doc -all isolate.ResourceStats
GOEXPERIMENT=isolates ./bin/go doc -all isolate.Value
```
