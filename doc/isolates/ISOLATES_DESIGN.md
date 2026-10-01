# Go Isolates — Design Definition

Status: **design draft with Phase 1 prototype and MVP scope revision**.
Last updated 2026-09-30.

## What an isolate is

An **isolate** is an independent Go execution context inside a single OS process:
its own package-level state, its own goroutines, its own memory, its own
deterministic scheduler. A very large number of small, fully isolated Go
programs run concurrently in one runtime, with no isolate able to observe or
reach another isolate's objects.

The model is V8/Cloudflare Workers, with three deliberate differences:

| | V8 isolates | Go isolates (here) |
|---|---|---|
| Code source | Each isolate loads its own script | All isolate code is compiled into the one host binary |
| Containment | Free — JS has no raw pointers | Build-time: a language subset banning `unsafe`, cgo, `linkname` |
| Determinism | Not a goal | A runtime guarantee — scheduling, map order, time, rand |

The driving use case is **Temporal workflow isolation**: many workflow
executions per worker process, each sandboxed and deterministically replayable.

## Decisions

| # | Decision | Choice |
|---|---|---|
| 1 | Motivation | Temporal workflow isolation |
| 2 | Trust model | Hostile code, contained by an enforced language subset |
| 3 | Code loading | Same binary, N instances (no dynamic loading) |
| 4 | Package globals | Per-isolate, via indirection through an isolate base |
| 5 | Concurrency | Single-threaded isolates; many isolates run in parallel |
| 6 | Determinism | Guaranteed by the runtime, not the SDK |
| 7 | Memory | Isolate-owned spans drawn from the shared `mheap_` pool |
| 8 | GC | Global initially; per-isolate later, without allocator changes |
| 9 | Snapshotting | Not implemented, but the layout must stay relocatable |
| 10 | Termination | MVP: whole-isolate revocation at runtime boundaries; forceful kill of uninterrupted computation is future work |
| 11 | Scale target | 10k+ isolates, long-lived, mostly idle |
| 12 | Distribution | Private fork of golang/go — *assumed, not confirmed* |
| 13 | Boundary ABI | Bytes (`Call([]byte)`); typed boundary interfaces deferred |

## The unifying mechanism: isolate-base-relative addressing

Four of the requirements above look independent but are satisfied by one
mechanism. Everything an isolate owns is addressed **relative to its isolate
base pointer** rather than by absolute address:

- **Decision 4** already requires it: global access becomes
  `base + static_offset` instead of a link-time fixed address.
- **Decision 6** benefits: relative addresses are reproducible across replay,
  which closes the residual determinism hazards (`%p`, pointer identity,
  iteration over pointer-keyed maps) instead of relying on having banned every
  way to observe an address.
- **Decision 9** requires it: a snapshot is only restorable at a different base
  if nothing inside stored an absolute address.
- **Suspend-time compaction** (below) requires it: an isolate can only be
  relocated or compacted if its internal pointers survive a base change.

This makes base-relative addressing the load-bearing decision of the whole
design. If it is compromised anywhere — one absolute pointer escaping into
isolate-owned memory — determinism, snapshotting, and compaction all degrade
together. It should be treated as an invariant to be enforced and tested, not a
convention.

## Derived architecture

### The host is isolate 0

Rather than special-casing host code against isolate code, the host runs as
isolate 0. Every selected global access is then uniformly indirected. A
package has one compiled code definition, with separate host and isolate
state copies where its mutable globals are selected.

### The isolate-scoped / process-global partition

Because all isolate code is known at link time (decision 3), the build can
compute the transitive package graph reachable from isolate entry points.
The target rule is to give every reachable Go package's mutable globals an
isolate copy by default, including standard-library packages. The compiler
and runtime then select the current instance's globals and tag allocations
made in that context with the same owner. Host calls use the host state copy;
code and immutable type metadata remain shared.

The scheduler, GC, allocator, and other runtime services need an explicit
process-owned exception set. A process-owned service must not retain an
isolate pointer or return mutable process state into an isolate without a
defined boundary operation. Initialization needs separate review when it
performs I/O, starts goroutines, or changes process state. Clocks, files,
network calls, and randomness need effect routing in addition to memory
ownership. The build must reject reachable code whose ownership or effects
have not been classified; silently sharing its globals is not a valid
default.

The current static POC selects reachable application packages plus
`encoding/base32`, `encoding/base64`, the mutable `encoding/json` v2 family,
and `reflect`. This list probes the compiler path; it is not the intended
default ownership rule or a complete standard-library audit. Heap allocation
ownership and cross-owner pointer enforcement are not implemented yet.

### Statically linked programs

Each isolate program can live in its own directory with `package main` and a
small `isolate.json` containing its stable logical name. The build selects
program directories, compiles their mains under distinct internal paths, and
links their entry wrappers into the host executable. Many instances of one
program share its code while retaining separate selected package state. See
[STATIC_PROGRAMS.md](./STATIC_PROGRAMS.md) for the proposed contract and the
current linker probe. All programs in the executable use one compatible
definition per Go import path. Arbitrary versions of the same import path in
different programs are a future build-system question.

### Suspend → compact → drop from root set

"10k+, mostly idle" (decision 11) creates a specific problem: under a global GC,
10k idle isolates still get their stacks and per-isolate globals scanned on
every cycle, contributing nothing but cost. Because isolates are suspended at a
known point, suspension is a natural place to compact an isolate into a
quiescent form and remove it from the root set entirely.

This is the same machinery as snapshot-readiness. Doing decision 9's groundwork
therefore also buys the fix for idle root-scanning cost — they should be built
as one thing.

### Termination scope

The MVP revokes the **whole isolate**, never one goroutine at a time. Its
scheduler must refuse to dispatch any queued or parked goroutine after
revocation. Their cleanup may happen later without scheduling them. A
goroutine already executing uninterrupted computation can continue until it
reaches a runtime-controlled point; the MVP cannot claim forceful kill or a
request-to-stop deadline for that case. This limits the MVP to trusted
workflows, even after state isolation and owned memory are implemented.
`Kill(ctx)` commits revocation and waits for the running goroutine. If the
context expires first, it returns a pending error with a best-effort stack
sample and sampled OS thread ID; the request stays in force. A nil return
means isolate code cannot run again, while memory cleanup may continue.

Forcefully terminating uninterrupted computation is a future improvement.
Single-threaded isolate execution means at most one goroutine of an isolate
is executing at a time, but all of its goroutines and wait records still
need whole-isolate teardown. Locks wholly owned by the dying isolate can be
discarded with it; scheduler records, GC roots, runtime locks, and shared
metadata require a separate safety proof. Banning tenant cgo and assembly
does not make trusted runtime assembly, such as large `memmove`, immediately
preemptible. The Phase 0 arm64 probe failed its 100 ms acknowledgment test
for that case, so it is not the MVP termination mechanism.

## Consequences and tensions

**The memory floor is the cost to watch.** Decision 7 keeps a per-isolate
size-class floor: roughly one 8KB span per size class an isolate touches, so
~40–80KB per isolate assuming 5–10 classes. At the 10k target that is roughly
0.4–0.8GB of pure floor — tolerable, but it is precisely the cost identified as
dominant for this profile. **Measure this before anything else.**

*Mitigation — the small-isolate nursery:* give each isolate a single
bump-allocated span covering all small objects, with no size classes at all, and
promote to size-classed spans only once it outgrows the nursery.

**Update: this is mandatory, not a proposal.** The budget was later derived
bottom-up from what a suspended workflow actually holds (IMPLEMENTATION_PLAN.md § E2) at a
median of ~8KB per isolate. Size-class spans cost more than the entire budget,
so the nursery is not contingent on the floor measurement. Two further
consequences: per-isolate globals must be lazily materialized rather than eagerly
copied, and **stack serialization on suspend becomes the mechanism that reaches
the density target** — `stackMin` is 2048 bytes and `shrinkstack` will not go
below it, so ~2KB per blocked goroutine is otherwise unreclaimable. That argues
for pulling decision 9 (snapshot) closer to the critical path than
"design for it, implement later."

**Global GC is a staging decision, not a resting place.** It is the right start
because it keeps the allocator stable, but at 10k mostly-idle isolates the
global root scan and global STW are the first things that will hurt. The span
ownership model exists to make the later split cheap — that option must not be
allowed to decay.

**The no-cross-reference invariant still needs a runtime audit.** Per-isolate
globals and the subset close most leak paths, but these hold user pointers
outside any isolate and need isolate-scoping or banning:

- `runtime.SetFinalizer` — closure holds isolate data, queued globally, and runs
  on `runfinq`, outside the isolate
- Timers — `timer.arg` parks a user pointer in a P's timer heap; the callback
  fires on an arbitrary P
- `sync.Pool` — the `Pool` struct becomes per-isolate for free, but
  `poolCleanup` is registered through a single global runtime hook, leaving it
  ambiguous which isolate's `allPools` it drains
- `g` reuse from `gFree` and stack reuse from `stackpool` — `g.param`,
  `g.labels` need explicit clearing on cross-isolate reuse

**Determinism is now a runtime contract.** Decision 6 means deterministic
goroutine scheduling, deterministic map iteration, and virtualized time and rand
become runtime properties. The payoff is that the SDK's deterministic dispatcher
(`workflow.Go`, `Selector`, the coroutine state machine) could largely be
deleted, along with a class of non-determinism bugs. The cost is that the
guarantee must hold under every runtime path, including GC and preemption.

## Still open

1. **The subset definition.** Now the critical undefined piece. Minimum bans:
   `unsafe`, cgo, `//go:linkname`, `reflect` escape hatches
   (`Value.UnsafeAddr`, `unsafe.Pointer` round-trips), `runtime.SetFinalizer`,
   direct syscalls, and ambient `os`/`net` access. Needs to be decided as a
   compile-time-enforceable list.
2. **The boundary ABI.** How a workflow task enters an isolate and commands come
   out. Must be copy-only or use an explicitly shared immutable region — any
   pointer crossing breaks the invariant.
3. **Confirm decision 12** — private fork vs. an upstream ambition.

## Non-goals

- Dynamic loading of independently compiled Go programs (decision 3)
- Parallelism *within* one isolate (decision 5)
- Defending against `unsafe`-bearing code at runtime; containment is build-time
  (decision 2)
