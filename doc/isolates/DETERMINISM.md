# Determinism — Required Runtime Changes

Status: **historical analysis**. Verified against the worktree at `2ff5743d9f`.
The implementation sequence and current contracts are in
[NATIVE_DETERMINISM_PLAN.md](./NATIVE_DETERMINISM_PLAN.md). In particular,
canonical map iteration removes the need to make map hashing deterministic.
Companion to [ISOLATES_DESIGN.md](./ISOLATES_DESIGN.md) (decision 6) and
[ISOLATE_SUBSET.md](./ISOLATE_SUBSET.md) (section D).

The subset doc lists time and map iteration as "virtualized by the runtime" in a
one-line table. That named the requirement without designing it. This document
works both through against the actual source. Neither is as simple as the table
implied, and map iteration has one source of nondeterminism that **cannot be
fixed by seeding at all**.

---

## The structural problem: `rand()` is per-M

`src/runtime/rand.go:163`

```go
func rand() uint64 {
	mp := getg().m
	c := &mp.chacha8
	...
```

The runtime's PRNG state lives on the **M**, not the g and not the isolate.
Isolates are single-threaded (decision 5) but are *not* pinned to an M, so every
consumer of `rand()` inherits whichever thread happened to run the isolate.

Everything below inherits this. The fix is one change — move the PRNG state into
the isolate struct, reached from `g` exactly as globals addressing already does
— but it has to happen before any of the individual fixes mean anything.

The consumers that matter are map seeding, map iterator offsets, and `selectgo`.

## Map iteration — five sources, one unfixable by seeding

| # | Source | Location |
|---|---|---|
| 1 | Per-map hash seed at creation | `internal/runtime/maps/map.go:288`, `:352` |
| 2 | **Seed *reset* on growth and clear** | `internal/runtime/maps/map.go:699`, `:786` |
| 3 | Iterator start offsets | `internal/runtime/maps/table.go:757-758` |
| 4 | Process-global hash keys | `internal/runtime/maps/runtime_alg.go:56` |
| 5 | **Architecture-dependent hash implementation** | `memhash_{amd64,arm64,386}.s`, `memhash_nosimd_amd64.s`, `aeskeysched` |

Source 2 is the one most likely to be missed. The seed does not merely get set at
creation — it is *re-randomized during the map's life*:

```go
// Reset the hash seed to make it more difficult for attackers to
// repeatedly trigger hash collisions. See https://go.dev/issue/25237.
m.seed = uintptr(rand())
```

So a fixed initial seed is not sufficient; the reseeding events must themselves
be deterministic.

Source 3 is explicit about its purpose:

```go
it.entryOffset = rand()
it.dirOffset   = rand()
```

Sources 1–4 all fall out of the per-isolate PRNG fix.

### Source 5 is the real problem

Source 5 is not about *which seed* — it is about *which algorithm runs*. The
tree ships separate hash implementations for amd64, arm64, and 386, plus
separate SIMD and non-SIMD amd64 paths, with an AES-based schedule
(`aeskeysched`) where hardware AES is available. Two machines compute **different
hash values for the same key with the same seed**, which produces a different
table layout, which produces a different iteration order.

No amount of seeding fixes this. It forces a decision:

- **If replay must work across architectures** — isolate maps have to use the
  portable `memhash` path and never `aeshash`. This is a real hot-path cost and
  needs measuring before it is committed to.
- **If replay can be pinned to a CPU class** — the hash stays fast, but replay
  acquires a placement constraint. Note this is finer-grained than
  "amd64 vs arm64": the SIMD and non-SIMD amd64 paths differ too, so the
  constraint is a microarchitecture class, not an architecture.

For Temporal this looks forced. A workflow evicted from one worker may be
replayed on another, and heterogeneous fleets (amd64 + arm64) are normal. **I'd
treat cross-architecture replay as required and plan to pay the portable-hash
cost**, but it should be measured, not assumed.

### Two smaller points

**Pointer-keyed maps hash the address.** For iteration to be reproducible, the
hash must be computed over the isolate-base-relative offset, not the absolute
address. This is the base-relative principle from the design doc reaching
further than it first appears.

**Much of the observable surface is already deterministic.** `fmt` sorts map
keys when printing, and `encoding/json` sorts them when marshaling. The real
exposure is direct `range` over a map where behavior depends on order — narrower
than it sounds, but exactly the case that produces silent replay divergence.

## Time — Go already ships a virtual clock

Better news here. The playground's faketime mode is a working per-process
virtual clock: `src/runtime/time_fake.go` makes `nanotime()` return a `faketime`
global, and `proc.go:6484` advances it to the next timer's deadline when nothing
is runnable — a discrete-event clock.

That is a precedent, not a solution: it is a whole-process build tag. Five things
are needed beyond it.

1. **Per-isolate, not global.** The clock moves into the isolate struct, reached
   from `g`.
2. **`nanotime` must split in two.** Runtime internals — the scheduler, the GC
   pacer, mutex contention profiling — need *real* time. Isolate code needs
   virtual time. Today they call the same function. This needs two distinct
   entry points rather than a mode flag, or the runtime starts making scheduling
   decisions on simulated time.
3. **Wall and monotonic must stay consistent.** `time.Now()` embeds a monotonic
   reading, and `Sub`/`Since`/`Until` use it in preference to the wall reading.
   Both have to come from the isolate clock or the two disagree.
4. **Policy: injected, not simulated.** For Temporal, workflow time comes from
   history timestamps recorded by the server — it is an *input*, not something
   the isolate computes. The runtime should expose a settable per-isolate clock
   and let the host drive it. faketime's auto-advance is the right mechanism but
   the wrong policy (it is right for a standalone isolate with no host).
5. **Timers must be host-driven.** `time.Sleep` in a workflow is a durable timer
   command, not a runtime timer — the goroutine parks until the host resumes it.
   `context.WithTimeout` and `time.AfterFunc` route the same way.

### Two things fall out of this

**Per-isolate deadlock detection and clock advance are the same work.** faketime
advances inside the runtime's check for "nothing can run" (`proc.go:6484`, by
the deadlock path). The design doc already requires per-isolate deadlock
detection so that "all goroutines asleep" terminates one isolate instead of the
process. That is the identical hook — build them together.

**A cross-isolate leak path closes.** The design doc flagged `timer.arg` parking
user pointers in a P's timer heap, with callbacks firing on an arbitrary P. If
isolate timers are host-driven and never enter the runtime timer heap, that path
disappears rather than needing to be isolate-tagged.

**Suspension gets easier too.** An isolate suspended for an hour must not observe
a monotonic jump on resume. Injected time gives this for free; measured time
would need explicit correction.

## `select`

`src/runtime/select.go:191` shuffles the poll order:

```go
j := cheaprandn(uint32(norder + 1))
```

Same per-isolate PRNG fix, no separate design needed.

## Still to work through

- **Goroutine scheduling order** — the core of decision 6 and the largest piece;
  not analyzed here
- Goroutine IDs appearing in stack traces and panic output
- GC timing — invisible if isolate time is injected rather than measured, which
  is a further argument for injection
- `sync.Map` iteration — inherits whatever the underlying map does
- Finalizer timing — moot, banned by the subset

## Cost

| Change | Cost |
|---|---|
| Portable hash instead of `aeshash` for isolate maps | Real, hot-path — **measure this** |
| PRNG state from isolate instead of M | Negligible; same pointer chase as globals |
| Split `nanotime` into real and virtual entry points | Negligible |
| Per-isolate clock and host-driven timers | Negligible |

## Open decisions

1. **Is cross-architecture replay required?** Blocks the hash decision, and the
   hash decision has the only non-negligible cost on this page.
2. **Injected or simulated clock?** Recommend injected for Temporal; a standalone
   isolate might still want faketime's auto-advance, so this may need to be a
   per-isolate mode.
3. **What happens to reseed-on-growth?** It exists to resist hash-flooding
   (go.dev/issue/25237). Removing it for isolate maps would weaken that. But it
   does not have to be removed — reseeding from the *isolate's* deterministic
   PRNG keeps the anti-flooding property while staying reproducible, since an
   attacker still cannot predict the sequence without the isolate's seed.
   Recommend deterministic reseed over removal.
