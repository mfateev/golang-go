# The Isolate API

Status: **draft for review**. Companion to [ISOLATES_DESIGN.md](./ISOLATES_DESIGN.md),
[ISOLATE_SUBSET.md](./ISOLATE_SUBSET.md), [DETERMINISM.md](./DETERMINISM.md).

Two surfaces: what the **host** calls to create and drive isolates, and what
code **inside** an isolate sees. The second is the interesting one, because the
whole point of the preceding decisions is that it should look like ordinary Go.

---

## The claim

Workflow code stops needing a parallel universe of SDK types. Today
`workflow.Context`, `workflow.Go`, `workflow.Channel`, `workflow.Selector`,
`workflow.Sleep`, and `Future.Get` exist because Go's own goroutines, channels,
`select`, and clock aren't deterministic. Decision 6 moves that guarantee into
the runtime, so the replacements stop earning their keep.

**Before** — today's SDK:

```go
func Order(ctx workflow.Context, o OrderReq) (Receipt, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var charged Charge
	if err := workflow.ExecuteActivity(ctx, Charge, o.Card).Get(ctx, &charged); err != nil {
		return Receipt{}, err
	}

	results := workflow.NewChannel(ctx)
	for _, item := range o.Items {
		item := item
		workflow.Go(ctx, func(ctx workflow.Context) {
			var r Shipped
			_ = workflow.ExecuteActivity(ctx, Ship, item).Get(ctx, &r)
			results.Send(ctx, r)
		})
	}
	shipped := make([]Shipped, 0, len(o.Items))
	for range o.Items {
		var r Shipped
		results.Receive(ctx, &r)
		shipped = append(shipped, r)
	}

	_ = workflow.Sleep(ctx, 24*time.Hour)
	return Receipt{charged, shipped}, nil
}
```

**After** — inside an isolate:

```go
func Order(o OrderReq) (Receipt, error) {
	charged, err := activity.Charge(o.Card)   // blocks; host-mediated
	if err != nil {
		return Receipt{}, err
	}

	shipped := make([]Shipped, len(o.Items))
	var wg sync.WaitGroup                      // real sync.WaitGroup
	for i, item := range o.Items {
		wg.Add(1)
		go func() {                            // real goroutines
			defer wg.Done()
			shipped[i], _ = activity.Ship(item)
		}()
	}
	wg.Wait()

	time.Sleep(24 * time.Hour)                 // real time.Sleep — durable
	return Receipt{charged, shipped}, nil
}
```

That `time.Sleep` suspends the isolate, which may then be evicted and restored a
day later. `go` and `sync.WaitGroup` are the real ones, made deterministic by
the runtime rather than replaced by the SDK.

### The larger win is third-party code

Today a library that calls `time.Now()` or starts a goroutine silently breaks
workflow determinism, which is why workflow code is confined to a small vetted
set of dependencies and guarded by `workflowcheck`. Under isolates, that library
is deterministic *because everything in the isolate is* — its `time.Now()`
returns injected time and its goroutines run on the deterministic scheduler.
Ordinary Go libraries become usable in workflow code.

---

## Isolate-side API: two primitives

The entire runtime-provided surface inside an isolate is two functions. Keeping
it this small matters — it is the part that has to be maintained against a
runtime fork forever.

```go
package isolate

// Call blocks the calling goroutine, emits an operation to the host as a
// command, and returns the host's response. Safe to call from many goroutines
// at once; the runtime correlates each call with its response.
//
// op is opaque to the runtime: it is stored, reported, and handed to the host
// unmodified. The runtime never switches on it. The whole value space belongs
// to the SDK.
func Call(op uint32, payload []byte) ([]byte, error)

// Inbox delivers messages pushed in by the host on resume — signals, updates,
// cancellation.
func Inbox() <-chan []byte
```

Everything else is the SDK encoding command types into those bytes. Activity
invocation, child workflows, signals, queries, continue-as-new, and timers are
all `Call` with a different payload — none of them need runtime support.

This is the zero-compiler-change option. See **Boundary interfaces** below for a
typed alternative that subsumes `Call` and costs compiler work.

`activity.Charge(...)` in the example above is an SDK-generated stub: marshal
args, `isolate.Call`, unmarshal result. The typed ergonomics live entirely in
the SDK, as they do today.

### Why `op` is a separate argument, and why it is opaque

The op could equally be the first few bytes of `payload` — the SDK owns both
ends, so a varint tag costs nothing and needs no runtime concept. Four things
argue for lifting it into the signature anyway:

1. **Enforcement must not require parsing attacker-controlled bytes.** Per-isolate
   capabilities (ISOLATE_SUBSET.md) need to answer "may this isolate start child
   workflows?" If the answer lives inside the payload, every capability check
   becomes a decode of hostile input. A fixed-position integer is checkable
   without touching the payload.
2. **Routing, metrics, and limits without decoding.** Per-op counters and rate
   limits are the natural operational controls at 10k isolates.
3. **Diagnostics.** "Isolate 7741 parked in op 12" is answerable from the parked
   goroutine record alone — no payload decode, works in a snapshot, works when
   the payload is corrupt. At this scale, *what are they all waiting on* is a
   routine operational question.
4. **It maps 1:1 onto a future boundary method.** The deferred typed boundary
   generates one thunk per method, which is exactly one op per method. Adding it
   now makes that migration mechanical instead of requiring a mapping to be
   invented retroactively.

**Opaque, though — the runtime stores and reports it but never switches on it.**
That is what keeps the runtime from acquiring an opinion about Temporal
semantics. It also means no registry, no runtime/SDK namespace collision, and no
stable-value negotiation: the SDK owns the entire space.

Nothing in the runtime's own state machine needs the op. The case that looks
like it would — quiescence needing to distinguish a timer wait from a host call
— dissolves because timers are native (the virtualized clock has to own them
regardless, see DETERMINISM.md), so `Call` means exactly one thing to the
runtime: park until the host replies.

### Not a string, and not a handle

**Not a string.** A string carries a pointer, so crossing the boundary means
copying bytes on every call — pure waste when the host only needs an identity.
Human-readable names belong in a host-side table: **names live host-side, IDs
cross the boundary.**

**Not a registered handle** like `Entry`. `Entry` needed a registry because
dispatch is data-driven (the host has a workflow type name from history) and
because restore must rebind across builds. Neither applies here: the isolate
always knows its own op at the call site, and the runtime never resolves it.

### One trap: op stability across builds

Because the runtime does not interpret the op, *the SDK* is what must interpret
it on restore — and a snapshot taken by one build can be restored by another
during a rolling deploy. Ops assigned by `iota` over a list that gains an entry
in the middle will silently renumber and misinterpret in-flight calls.

**Use explicitly-valued constants, never bare `iota`,** and treat the op space
as an append-only wire contract. This is an SDK discipline rather than a runtime
guarantee, which is exactly why it is worth writing down — it fails silently and
only under mixed-version deploys.

### Concurrency falls out for free

`Call` blocking per-goroutine is what makes plain `go` work for fan-out. Each
parked goroutine is an outstanding command; the host sees several at once and
answers them independently. No `Selector`, no `Future`.

### Entry points

At the runtime boundary an entry point is `func([]byte) ([]byte, error)`. Typed
signatures like `func(OrderReq) (Receipt, error)` are an SDK adapter that
marshals through the DataConverter, exactly as today.

The natural first instinct is for `Config.Entry` to just *be* the function:

```go
iso, _ := isolate.New(isolate.Config{Entry: Order})   // doesn't work
```

A top-level func value is safe to share — it is a static symbol in read-only
data, not a pointer into anyone's heap. But three things rule it out anyway:

1. **The callable is isolate-scoped, not process-global.** The SDK adapter that
   marshals `[]byte` into `OrderReq` is a *closure*: it captures the
   DataConverter and the target func. Registration runs in template `init`, so
   those captures live in the template image and are copied into each isolate.
   Each isolate therefore has its *own* adapter closure. The host, sitting
   outside, has no single func value it could name.
2. **Dispatch is data-driven.** The host learns which workflow to run from
   history — it has the string `"Order"` in hand, not a compile-time reference.
   A name lookup is unavoidable.
3. **Snapshot and restore need a stable identity.** Decision 9 requires an
   isolate to be restorable in another process or another build. A code pointer
   is meaningless there; a symbolic name survives.

So `Entry` is a **handle**: named at registration, typed at registration,
resolved per-isolate at start.

```go
// Entry is a process-global, comparable handle to a registered entry point.
// The callable it names is resolved inside the isolate at start.
type Entry struct{ id uint32 }

// Register binds a name to an entry point and returns its handle. Must be
// called from package init, so the binding becomes part of the template image.
func Register(name string, fn func([]byte) ([]byte, error)) Entry

// Lookup resolves a name registered earlier — the path the host takes when the
// entry is chosen by data rather than by code.
func Lookup(name string) (Entry, bool)
```

```go
var OrderEntry = isolate.Register("Order", orderAdapter)   // in template init

// host
iso, _ := isolate.New(isolate.Config{Entry: OrderEntry, ...})
// or, driven by history:
e, ok := isolate.Lookup(ev.WorkflowType)
```

This keeps the reference type-checked at the point that matters — `Register`
takes a typed func, so a misspelling or a signature change is a compile error,
unlike a bare string in `Config`.

### The registry is split across the partition

The registry is two structures, and this is the clearest worked example of the
isolate-scoped / process-global split:

| | Scope | Contents |
|---|---|---|
| Name → id | **Process-global** | Populated once during template init; idempotent |
| id → callable | **Isolate-scoped** | A slot table copied from the template into every isolate |

`Config.Entry` carries only the id. At start, the runtime indexes the isolate's
*own* slot table. Nothing points across an isolate boundary at any point, and
the host never holds a callable.

An earlier version of this document claimed the registry was wholly
process-global and held static func values. That is wrong: it holds adapter
closures, which are per-isolate data. A process-global table of them would be
exactly the cross-isolate reference the design forbids.

### The alternative: code generation

If the SDK generated real top-level functions instead of closures —

```go
//go:generate temporal-gen
func orderEntry(b []byte) ([]byte, error) { ... }   // generated, top-level
```

— then the adapter has no captures, the callable *is* process-global, and
`Config.Entry` could hold the func value directly with full compile-time typing.
Reasons 2 and 3 above still force a name registry to exist alongside it, so this
buys typing rather than removing machinery, at the cost of a generate step. It
is a real option and a departure from the SDK's current reflection-based
registration; worth deciding deliberately rather than by default.

---

## Boundary interfaces — DEFERRED

> **Status: future improvement, not in scope for the initial implementation.**
> The shipping boundary is `Call([]byte)`. This section is kept because the
> approach is sound and because `Call` is forward-compatible with it — when
> boundary interfaces are added, `Call` becomes one method on the standard
> boundary interface and nothing built against it changes.

Instead of a bytes primitive, the entry point could receive an **interface**
supplied by the host, whose methods are restricted to value types:

```go
type Env interface {
	isolate.Boundary                                  // marker

	ExecuteActivity(name string, in []byte) ([]byte, error)
	Sleep(d time.Duration)
	Now() time.Time
	Log(level int, msg string)
}

func Order(env Env, o OrderReq) (Receipt, error) { ... }
```

The value-type restriction is what makes this work, and it is doing more than it
looks like.

### Why the restriction makes it decidable

Copy-only across the boundary is currently a *convention* enforced by
serializing to bytes. Restricting method signatures to value types turns it into
a property the **compiler can verify**, and the copy into code the compiler can
**generate**.

The type predicate has three tiers, all statically decidable:

| Tier | Admits | Copy |
|---|---|---|
| A | Pointer-free types — `abi.Type.PtrBytes == 0` (`internal/abi/type.go:204`) | `memcpy` of the arg frame |
| B | + `string` | one extra byte copy; immutable, so never cyclic |
| C | + slices, maps, arrays, structs of A/B/C | generated per-type deep copy |

Excluded throughout: pointers, `unsafe.Pointer`, funcs, channels, and
interfaces. Tier A is nearly free — the boundary becomes a memcpy of a fixed
struct rather than a protobuf round trip.

**The cycle question resolves statically, which is the part I'd have expected to
be the blocker.** A deep copy must terminate, so cyclic values have to be
impossible. Without pointers, funcs, chans, or interfaces you might assume
cycles already are — but they are not:

```go
type T struct{ xs []T }
a := make([]T, 1)
a[0].xs = a        // a[0].xs aliases a — a cycle, no pointers in sight
```

A value cycle requires a cycle in the *type* graph passing through a slice or
map. That is decidable at compile time: reject any boundary type whose type
graph has a slice- or map-mediated cycle. `[]T` where `T` is pointer-free can
never cycle, so `[]byte`, `[]int`, `[]string` are all fine. No runtime cycle
detection is needed.

So the answer to "how hard is the verification" is: **easy, and fully static.**

### How the interface value avoids crossing

The subtle part. An interface value is an itab pointer plus a data word. If the
host implements `Env`, the data word would point at the host's receiver — a
cross-isolate pointer, exactly what the design forbids.

The fix: **the data word holds an isolate-local handle, not a pointer.** The
host resolves handle→receiver in its own table. The itab is static shared data
(type descriptors and method pointers, allocated with `persistentalloc`), and
the methods it names are generated thunks in shared `.text`. Nothing points out
of the isolate. Go already has machinery nearby for word-sized interface data —
`abi.Type.IsDirectIface` (`internal/abi/type.go:207`).

### Difficulty

| Piece | Effort |
|---|---|
| Type predicate + cycle rejection | Easy — a recursive type walk |
| `isolate.Boundary` marker and its checking | Easy — marker embedding, no new syntax |
| Thunk generation, Tier A (memcpy frame) | Medium — the cgo wrapper generator is the precedent |
| Generated deep copy, Tier C | Medium-hard — nested types, per-type routines |
| Handle-in-data-word interface construction | Medium — contained, but touches interface construction |
| Park/resume inside a thunk, and snapshot while parked | Hard — **but shared with `Call`, not additional** |

Overall: meaningfully harder than the bytes primitive, but not disproportionate,
and the hardest piece is work the bytes primitive needs anyway.

**The real cost is strategic, not technical.** `Call([]byte)` needs *zero*
compiler changes. Boundary interfaces add a permanent compiler surface to a fork
maintained indefinitely. That marginal cost is lower here than it would normally
be — per-isolate globals already commit to compiler work — but it is additive,
and compiler surface is the most expensive kind to carry across Go version bumps.

### Where it wins, and where it doesn't

**Wins for in-process calls.** Clock, timers, logging, state queries, signal
delivery, cancellation — these never leave the process, so Tier A memcpy
replaces a serialize/deserialize round trip entirely. Typed, compiler-checked,
nearly free.

**Doesn't win for activity payloads.** An activity argument has to reach another
process, so it must become bytes regardless. Deep-copying a typed struct into
host memory only to serialize it there adds a copy rather than removing one.

That is why `ExecuteActivity` above takes `[]byte`: the boundary interface is
one mechanism, and whether a given method carries typed values or opaque bytes
is a per-method choice. Version skew is a non-issue either way, because
decision 3 guarantees both sides are always the same build.

### It subsumes `Call`

If boundary interfaces exist, `Call` is just a method on a standard one, and the
isolate-side surface becomes a single concept rather than two. `Inbox` stays
isolate-local — a channel is a pointer and cannot be a boundary type, but it
doesn't need to be, since the runtime feeds it from inside.

### Decision

**Deferred.** The typed boundary is the better API and the restriction is sound,
but it is not what to build first. `Call([]byte)` needs no compiler work and
lets the scheduler, quiescence hook, and memory model be validated on their own.

Two things to preserve so this stays open:

- **Keep payload shapes value-type-clean where it is free to do so.** Anything
  that would later become a typed boundary method should avoid pointers, funcs,
  chans, and interfaces in its payload struct now, so the migration is mechanical.
- **Keep `Call` correlation IDs out of addresses** (already required for
  snapshot/restore). Thunk-based calls inherit the same mechanism.

Revisit when: the in-process call surface (clock, timers, logging, state
queries, cancellation) is large enough that serialization overhead on calls
that never leave the process is measurable.

## Host API

```go
package isolate

type Config struct {
	Entry        Entry         // handle from Register/Lookup — see below
	MemoryLimit  int64         // enforced via span accounting
	GoroutineLimit int
	Clock        Clock         // host-injected time (see DETERMINISM.md)
	Seed         uint64        // determinism seed for the isolate PRNG
	Capabilities Capabilities  // per-isolate trap policy (see ISOLATE_SUBSET.md)
}

func New(cfg Config) (*Isolate, error)

// Resume runs the isolate until it goes quiescent, completes, or fails.
// Commands are the payloads of every goroutine currently parked in Call.
func (iso *Isolate) Resume(ctx context.Context, events []Event) (State, []Command, error)

// Kill irreversibly revokes the whole isolate, then waits for its currently
// executing goroutine to stop at a runtime-controlled point. ctx bounds only
// that wait. A pending error does not undo the revocation request.
func (iso *Isolate) Kill(ctx context.Context) error
func (iso *Isolate) Stats() Stats         // pages, goroutines, CPU consumed

// KillPendingError means an isolate goroutine was still executing when ctx
// expired. Stack is a best-effort sample, possibly empty or truncated.
type KillPendingError struct {
	GoroutineID uint64
	ThreadID    int64
	Stack       string
}
func (e *KillPendingError) Error() string

func (iso *Isolate) Snapshot() ([]byte, error)              // later
func Restore(b []byte, cfg Config) (*Isolate, error)        // later
```

A command is one goroutine parked in `Call`; an event is a reply or an inbound
message:

```go
type Command struct {
	ID      uint64   // correlates with the Event that answers it; never an address
	Op      uint32   // opaque, as passed to Call
	Payload []byte   // copied out of isolate memory
}

type Event struct {
	ID      uint64   // 0 for unsolicited events delivered to Inbox
	Payload []byte   // copied into isolate memory
	Err     error
}
```

`ID` must not be derived from an address — a parked call has to survive snapshot
and restore, where addresses do not.

The host loop is the workflow task loop:

```go
for {
	state, cmds, err := iso.Resume(ctx, events)
	if err != nil || state != Quiescent {
		break
	}
	events = host.Process(cmds)
}
```

`State` is one of `Quiescent`, `Completed`, `Failed`, `Deadlocked`,
`LimitExceeded`, or `Killed`. The `ctx` deadline on `Resume` bounds that host
call; exceeding it leaves the isolate resumable. The Phase 1 prototype returns
the context error and retains its active task. The trusted MVP will revoke
the whole isolate at runtime-controlled scheduling and boundary points, so
queued and parked goroutines cannot resume after revocation. `Kill(ctx)`
returns nil once no isolate goroutine can execute again; heap reclamation may
finish later. If the currently running goroutine does not reach a boundary
before `ctx` expires, it returns `*KillPendingError` with a best-effort Go
stack sample and the OS thread ID at sampling time. The isolate remains
revoked, but that goroutine may still run; retrying `Kill` waits on the same
request. A stack sample may fail even when the kill request succeeded, so an
empty `Stack` must be handled. Only a later forceful-kill milestone can claim
bounded termination of uninterrupted code.

Everything crossing the boundary — `Command.Payload`, `Event.Payload`, entry
input, and result — is **bytes, copied**. No typed deep-copy at the runtime
level: it would need to handle cycles, unexported fields, funcs, and channels,
and Temporal payloads are already bytes. Typing stays an SDK concern.

---

## Quiescence: three requirements, one hook

"No goroutine in this isolate can run" turns out to be the same condition for
three separate things established earlier:

| Requirement | Source |
|---|---|
| Isolate-fatal deadlock detection | ISOLATES_DESIGN.md §E |
| Virtual clock advance | DETERMINISM.md — faketime advances at `proc.go:6484` |
| The suspend point / `Resume` return | this document |

They are distinguished only by *why* nothing is runnable:

- Goroutines parked in `Call`, awaiting the host → **Quiescent**, return commands
- Goroutines parked on a timer and nothing else runnable → **advance the clock**
  (standalone mode) or **Quiescent** (host-injected mode)
- Goroutines blocked on each other with no outstanding host call and no timer →
  **Deadlocked**, isolate-fatal
- No goroutines left → **Completed**

So the per-isolate deadlock detector, the clock, and the host boundary are one
piece of work with three exits. This is the third convergence on that hook and
the strongest argument for building it first.

---

## Failure and termination

- **Panic escaping the entry point** — the isolate fails; host gets `Failed`
  with the panic value and stack (rebased addresses, per-isolate goroutine IDs).
  For Temporal this is a workflow task failure, which is retried.
- **Subset violation** — isolate-fatal, unrecoverable, not a panic
  (see ISOLATE_SUBSET.md: `recover` would otherwise become a probe channel).
- **Limit exceeded** — `LimitExceeded`; host decides whether to kill.
- **Kill** — unconditional; goroutines are stopped at preemption-safe points and
  the isolate's spans are returned without tracing.

Note the asymmetry worth preserving: a *panic* is the isolate's own business and
recoverable by isolate code; a *violation* is not.

---

## What this removes from the SDK

| Today | Under isolates |
|---|---|
| `workflow.Context` threading | Gone — no dispatcher state to carry |
| `workflow.Go` | `go` |
| `workflow.Channel`, `Selector` | `chan`, `select` |
| `workflow.Sleep`, `workflow.Now` | `time.Sleep`, `time.Now` |
| `Future.Get(ctx, &out)` | Ordinary blocking call and return |
| `workflow.SideEffect` for uuid/rand | Gone — the isolate PRNG is deterministic |
| `workflowcheck` linter | Enforced by the toolchain instead |

**What survives**, because these are Temporal concepts rather than Go ones:
activity options (timeouts, retry policy), versioning and patching for code
changes, continue-as-new, child workflow and signal semantics, and
`SideEffect` for genuinely external nondeterminism such as reading host config.

---

## Open questions

1. **Where do activity options live?** `workflow.WithActivityOptions` used the
   context that no longer exists. Options on the generated stub, a plain
   `context.Context`, or a per-call struct — this is an SDK design question but
   it shapes what the "after" code above actually looks like.
2. **Does `context.Context` still appear in workflow signatures?** It is no
   longer needed for dispatcher state, but cancellation still has to arrive
   somehow, and `Inbox` is a lower-level answer than users will want.
3. **Correlating `Call` responses across suspend/restore.** Call IDs must
   survive a snapshot and restore, so they cannot be addresses.
4. **Is one `Call` primitive really enough for queries?** Queries are
   host-initiated and must not mutate state — that is an inbound pattern
   `Inbox` can express, but read-only enforcement may need runtime help.
5. **Closure adapters or generated top-level entry points?** See "The
   alternative: code generation" above. Codegen buys compile-time typing on
   `Config.Entry` and removes per-isolate adapter closures, at the cost of a
   generate step and a departure from reflection-based registration.
