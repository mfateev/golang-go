# Host integration with ordinary Go functions

Status: implemented in the Go fork (allocation scopes, typed host functions,
synchronous reentry, and one-shot waiters). The APIs below are additions to the
public `isolate` package. Existing `Call`, `ControlCall`, `ReadOnlyCall`,
and `Write` behavior and their SDK users remain unchanged. Migrating that
transport to this interface is separate work.

Host integration should use ordinary Go functions, concrete values, constructors,
and callbacks. A library can keep shared metadata in the host, allocate a result
for its calling isolate, and explicitly park the caller when work must wait.
The compiler and runtime enforce execution and allocation domains without
knowing the library's module path, concrete types, or private layouts.

## Ownership contract

An allocation belongs to the host or to one isolate execution domain. The domain
is a runtime property of the allocation, independent of its Go type. Shared host
objects may be mutable. The programmer is responsible for synchronization,
consistent results, and preventing shared objects from retaining references into
an isolate. This includes references hidden in slices, maps, interfaces, function
values, and their reachable objects.

The integration runs without reference-publication validation of shared graphs.
Exporting an instance allocator, entering a typed host operation, or creating a
waiter enables trusted host reference sharing for that instance: it may retain opaque host references,
and host control records may retain its allocations. Host operations and
constructors also borrow references under this contract. Memory access checks
still reject unrelated private domains and ordinary access to host objects.
Instances using only the existing transport retain their publication checks.
This requires neither immutable types nor freezing, copying,
or a library allowlist. Existing ownership checks continue to protect unrelated
memory; crossing the explicit host boundary grants access to the host operation,
not ordinary isolate access to every host allocation. Optional diagnostic
instrumentation may detect violations without becoming a prerequisite for
execution.

Borrowing isolate data during a call and returning an isolate-owned result are
permitted. Retaining either in a shared cache after the operation is not. Runtime
records used to manage a live call may root borrowed arguments, callbacks, and
results until their use ends. Those records are lifecycle machinery, not
application shared caches.

Both domains currently use Go's collector. A valid, GC-visible pointer retained
by the host keeps its target reachable and can retain an isolate's graph. It also
provides a potential path to private data, subject to remaining access checks.
Retention is therefore a programmer error with consequences beyond memory use.
Independent isolate collection, heap freezing, or bulk reclamation must preserve
these borrowing lifetimes before enabling those features.

## API

The following declarations summarize the implemented surface. Runtime control
state remains opaque to application code.

```go
package isolate

// HostContext describes one active call from an isolate into a host operation.
// It is valid until that operation returns and cannot be retained or transferred
// to another goroutine. Its zero value is invalid.
type HostContext struct { /* runtime state */
}

// Allocator identifies an execution domain and keeps its allocation machinery
// alive. It does not grant permission to run application code in that domain.
type Allocator struct { /* runtime state */
}

// NewHostFunc registers a host operation and returns an ordinary typed function.
// Registration occurs in host code. Calling the returned function in an isolate
// enters the host operation and supplies its active HostContext.
func NewHostFunc[In, Out any](
	fn func(HostContext, In) (Out, error),
) func(In) (Out, error)

// Allocator selects the calling execution domain, including a read-only
// service's scratch domain when applicable.
func (c HostContext) Allocator() Allocator

// Allocator returns the instance's main allocation domain to its host controller.
// It is available after successful instance initialization.
func (i *Isolate) Allocator() Allocator

// HostAllocator selects ordinary host allocation inside a host construction
// scope. It cannot be obtained directly by isolate application code.
func HostAllocator() Allocator

// Allocate constructs a value using a selected allocation domain. It is called
// from host code or a registered host operation.
func Allocate[T any](a Allocator, build func() T) (T, error)

// Reenter calls an application callback synchronously in the original caller's
// execution context, then restores the enclosing host context.
func (c HostContext) Reenter(fn func())

// Waiter connects asynchronous host completion to one isolate goroutine.
// It must not be copied after first use.
type Waiter struct { /* runtime state */
}

// NewWaiter creates a host-shareable control handle for the current goroutine.
func NewWaiter() *Waiter

func (w *Waiter) Park()
func (w *Waiter) Wake()
```

`NewHostFunc` is explicit authority granted by the host at registration. It does
not infer trust from a package name or promote an arbitrary isolate closure to
host execution. The implementation must validate registration on the host and
construct its own entry trampoline. The function returned to an isolate is a
supported callable handle; it does not expose the registered closure's fields or
its host captures. Shared caches are accessed inside these registered operations.
A returned host pointer may be retained as a metadata handle; returning it does
not by itself authorize arbitrary field access from isolate code. Direct shared
field access would need its own explicit access contract. Registration belongs to application setup, and the SDK build
driver supplies bindings where private package state would otherwise hide the
host registration. No external adapter receives a raw compiler descriptor.

The compiler marks the entry closure emitted by the verified standard-library
`NewHostFunc` implementation, including its imported generic instantiations.
Only that callable entry is shared; ordinary closure reads never acquire access
to its captures. A compiler-typed function load may import a registered callable
from a host binding cell. This supports SDK bindings without exposing adjacent
host state or granting direct shared-field access. Applications cannot publish
an arbitrary host closure through the same mechanism.

Function values materialized in application execution retain their allocation
domain even when their code has no captures; their code identity stays unchanged.
A host operation or constructor cannot invoke a private callback directly or
through reflection. It must use `Reenter`. Registration also rejects an
isolate-owned closure supplied by a host controller.

The initial interface requires an isolate caller. Host callers use their ordinary
implementation directly; calling a registered wrapper without an isolate caller
panics. `HostContext` does not expose an owner ID, goroutine pointer, or descriptor.
An allocator obtained from it may outlive the call for asynchronous construction,
but cannot resurrect a stopped instance. It keeps ownership metadata alive and
rejects new construction after revocation. A scratch allocator also expires when
its read-only execution domain closes; it cannot fall back to the main heap.

## Constructing a result for an isolate

A shared catalog can build an application value with ordinary Go constructors:

```go
// During host setup. Catalog and Schema are library types, not runtime types.
lookup := isolate.NewHostFunc(
	func(call isolate.HostContext, name string) (*Record, error) {
		schema, err := catalog.Lookup(name)
		if err != nil {
			return nil, err
		}
		return isolate.Allocate(call.Allocator(), func() *Record {
			return schema.NewRecord()
		})
	},
)

// The SDK binds lookup into the isolate's private application state.
// Application code calls it like an ordinary Go function.
record, err := lookup("customer")
```

The string argument is borrowed for the duration of the call. `catalog.Lookup`
must copy it if the catalog retains it. The schema and its lazy indexes stay
host-owned; the record and its newly allocated fields belong to the caller. A
record may reference explicitly shared host metadata under the programmer's
sharing contract. The runtime neither clones nor walks the returned graph.

`Allocate` selects the owner for Go allocations made synchronously by `build`
and its helpers: objects, slice backing arrays, maps, interface boxes, and
closure environments. Escape analysis must not leave escaping objects on a
host stack; stack temporaries remain ordinary stack storage. Existing objects
keep their owner. A constructor that retrieves a cached host object does not
turn it into an isolate-owned value. A constructor must not populate a shared
cache with its isolate-owned allocations. Shared lazy initialization runs before
the constructor or in a nested `Allocate(HostAllocator(), ...)` scope. Library
adapters own that separation; it is not inferred by the compiler.

The constructor receives access needed to initialize its new objects and may
read permitted shared metadata. This is a construction context, not application
reentry. It does not restore the isolate's private package globals, run its
continuations, or grant access to another isolate. Constructor callbacks needing
application state use `Reenter` explicitly. Access to preexisting isolate objects
requires a valid borrow from the enclosing host call; an allocator obtained from
`Isolate.Allocator` alone grants no such borrow.

Allocation selection is nested and restored on normal return and panic. A scope
that cannot begin because its target is revoked returns a lifecycle error without
calling `build`. A panic from `build` unwinds the constructor before the boundary
handles it. New goroutines do not inherit an allocator scope or borrowed access;
asynchronous work obtains its own scoped use of an explicit allocator.

A running `Allocate` scope holds a construction lease. Revocation can prevent
new scopes and isolate execution, but cannot invalidate memory while host
construction still uses it. Completion checks revocation before delivering its
result. Resource limits apply to the selected domain; host allocation cannot
bypass the isolate's memory budget. Read-only calls select scratch ownership,
never writable workflow ownership.

Host controllers may also call `Allocate(instance.Allocator(), constructor)`
before starting an instance or while it is suspended. The returned value remains
isolate-owned. It may be held temporarily for explicit delivery through a typed
host operation or a future typed input API. Existing byte `Call` replies still
copy bytes; allocating a typed value does not change that protocol or inject the
value into a running isolate.

## Execution transitions

A registered host operation starts with ordinary host allocation and host
package globals. Runtime state records the original isolate, its execution role,
and its borrowed arguments until the operation ends. Host execution remains
visible to lifecycle accounting. Allocation selection, access permission, private
package state, and scheduling membership are distinct state; changing one must
not implicitly change the others.

A synchronous operation keeps the caller's execution position until it returns
or explicitly parks. Go code continues to run as normal Go code with a scannable
stack and normal GC participation. It must not execute on a system stack or be
marked as executing a foreign syscall while it can allocate or call Go helpers.

`Reenter` restores the original caller's package state, allocation owner, effect
restrictions, and read-only role for the callback. Callback allocations belong
to the caller. It cannot inherit host access or an enclosing constructor's
allocation override. It returns to the host context after normal completion and
unwinds correctly on panic. Nested calls maintain a stack of contexts. Initial
support is synchronous; an arbitrary host goroutine cannot use an expired
context to start an application callback.

Revocation during a host operation defers abandonment until its required host
cleanup runs. The operation cannot return control to a revoked isolate.
Borrowed workflow data remains read-only during a read-only service, including
inside host operations. Construction and host locks must not be abandoned by
discarding a goroutine in the middle of host cleanup. Panics crossing the boundary must restore all context
first; a host panic value cannot implicitly grant access to arbitrary host memory.
The first implementation reports copied panic diagnostics through the existing
isolate failure path rather than transferring an arbitrary panic object.

Returned errors are Go values with ownership too. An operation constructs a
caller-owned error, returns a deliberately shared host error, or returns nil.
The interface does not silently copy arbitrary error graphs. Normal result and
error pointers follow the trusted sharing contract; the special panic rule
prevents accidental publication during unwinding.

## Blocking and completion

A fast host operation returns without allocating a command envelope or copying
its arguments. An operation that waits for external completion uses a waiter:

```go
func lookupAsync(call isolate.HostContext, name string) (*Record, error) {
	waiter := isolate.NewWaiter() // Bound to the original isolate caller.
	allocator := call.Allocator()
	var result *Record
	var resultErr error

	// The host API must copy name if it retains the string after this call.
	requestName := strings.Clone(name)
	go func() {
		schema, err := catalog.Load(requestName)
		if err == nil {
			result, err = isolate.Allocate(allocator, func() *Record {
				return schema.NewRecord()
			})
		}
		resultErr = err // Must be caller-owned or explicitly shared on return.
		waiter.Wake()
	}()
	waiter.Park()
	return result, resultErr
}
```

This is a borrowing example: the host worker temporarily retains the result
slots until it completes. The boundary must root the captured storage until
completion, including after revocation, and the implementation must account for
those captures when separating allocation ownership. The worker never runs an
application continuation. A production library may encapsulate these slots in
its own synchronized operation object.

`NewWaiter` works in isolate code and in a registered operation on behalf of its
original caller. It is a runtime control handle, with no application closure or
user-data field. Each waiter has one associated goroutine and one completion.
`Wake` is safe from any host goroutine and is idempotent. A waiter created
inside a host operation records its outstanding asynchronous borrow; completion
releases that borrow. The worker must finish accessing borrowed data before
calling `Wake`, even when the isolate has already been revoked. Waking before
parking records completion; the subsequent park does not lose the notification.

`Park` commits the wait and releases the deterministic dispatch token atomically
with respect to wakeup. It marks the caller blocked for suspension and resource
accounting. Completion writes before `Wake` happen before reads after `Park`
returns. A wake makes the caller eligible for admission; it does not bypass a
suspended instance, read-only fence, or the dispatch queue. Using a waiter from
the wrong goroutine or parking it more than once panics.

Revocation terminates the isolate continuation. If host cleanup is required,
the wait unwinds through the enclosing host boundary before the continuation is
discarded. The runtime retains any outstanding host borrows until their workers
release them; late wakeups safely release remaining waiter bookkeeping without
resuming isolate execution. Kill may report pending host work when a worker has
not finished. It cannot forcibly stop arbitrary host code or promise reclamation
before that code releases its borrows.

Ordinary local channel and synchronization waits retain their existing runtime
support. A host operation must not silently block in an unclassified operation
while retaining the deterministic token. The initial interface requires explicit
parking for externally completed work. General synchronous blocking adapters and
cross-goroutine application callbacks are separate extensions.

## Determinism and SDK policy

Shared host mutation, host time, I/O, and completion timing are observable inputs.
The programmer's sharing contract does not make those inputs deterministic.
The runtime supplies dispatch and admission; the SDK decides which operations
are permitted during replay, when external completions become eligible, and how
results are recorded or replayed. A generic waiter cannot decide durable order.

Existing Temporal commands, durable timers, task delivery, queries, updates,
logging, and replay continue through their current APIs. No saved history,
operation number, command ordering, or random-stream consumption changes merely
because the new host integration surface is added. Any later migration requires
its own replay and scheduling comparison.

## Relationship to cgo

The useful precedent is an explicit call boundary, allocation-based pointer
rules, borrowed arguments, callback reentry, and scheduler participation. See the
Go source documentation for [pointer passing](../../src/cmd/cgo/doc.go) and
[call transitions](../../src/runtime/cgocall.go).

There is no C ABI, C allocation, generated C wrapper, or foreign stack. Ordinary
Go types cross this boundary. Both heaps are GC-visible, so retaining a pointer
has different consequences from hiding one in C memory. Pinning an allocation
would not change its isolate owner or grant permission to retain it. Normal Go
stack movement remains supported through rooted, compiler-visible references.

The trusted mode also differs from cgo's default dynamic pointer
checks. Optional diagnostics can check publication and borrow escape, analogous
to stronger cgo checking, while normal integration relies on the documented
programmer obligations. Any diagnostic exemptions must describe generic runtime
operations, never library names or dependency versions.

## Implementation boundaries and acceptance

The Go fork owns callable-handle provenance, context transitions, allocator
selection and lifetime, private-state restoration, park/wake, revocation, and
optional ownership diagnostics. The SDK and library integrations own adapters,
constructors, shared caches, synchronization, source/version policy, and replay.
An external library upgrade must not require compiler/runtime layout changes.

Implementation should proceed in independently reviewable stages:

1. Add host-controlled allocation scopes and lifetime leases, using generic
   outside-GOROOT constructor fixtures. Keep existing transport unchanged.
2. Add registered typed host functions and synchronous callback reentry, with
   compiler-created trampolines and private-state restoration.
3. Add explicit one-shot waiters and asynchronous borrow cleanup. Exercise
   deterministic admission and suspension before adapting blocking libraries.
4. Integrate libraries in SDK-owned code. Replace current library-specific
   rewrites only after equivalent behavior and lifecycle tests pass. Retiring
   their source guards is part of that replacement, not this core implementation.

Required coverage includes nested structs, maps, slices, interfaces, closures,
cycles, constructor panic, callback panic, nested boundaries, two isolates using
the same host function, read-only scratch ownership, and concurrent GC/stack
movement. Lifecycle tests race allocation, completion, suspension, and kill;
verify wake-before-park, duplicate wake, late wake, pending cleanup, and memory
budget accounting. Race checks verify completion synchronization and shared-cache
fixtures. Generic tests need no protobuf, Temporal, or OTel dependencies.

The existing compiler, runtime, SDK, and replay gates must continue to pass with
no migration from `Call`. Optional publication diagnostics should have negative
fixtures for retained pointers hidden in every supported aggregate form. They
must remain distinct from any claim that unchecked shared structures are safe
when programmers violate their contract.

## Implementation and verification

`src/isolate/host_integration.go` exposes the typed API. The runtime keeps host
execution contexts, allocation scopes, and scheduling membership separate.
Constructors track newly allocated extents without traversing result graphs, so
an allocator alone cannot authorize access to preexisting private objects.
Scratch allocators carry a service lifetime and expire when that service exits.
Allocation and outstanding asynchronous-borrow leases participate in the drain
fence; `KillPendingError.PendingHostWork` reports work that still holds them.

`src/isolate/host_integration_test.go` exercises domain selection, nested scopes,
constructor and callback panic, context misuse, two instances sharing a host
function, GC, budgets, scratch lifetime, one-shot synchronization, suspension,
and completion racing kill. The generic module fixture
`src/cmd/go/testdata/script/build_isolate_host_integration.txt` compiles ordinary
constructors and a shared cache outside GOROOT under compulsory ownership and
effect checks, both normally and with the race detector. Its negative cases
include direct and reflected callbacks, ordinary host closures, host metadata
field reads, expired contexts, and read-only mutation.

SDK library migration and optional publication/borrow-escape diagnostics remain
separate work. This API does not change existing Call transport or replay policy.
