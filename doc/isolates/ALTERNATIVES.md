# Implementing Without Forking Go

Status: **analysis**. Rationale behind the fork-vs-external decision point in IMPLEMENTATION_PLAN.md — see the
recommendation at the end.

The design so far assumes a private fork of golang/go. That assumption has never
been tested. It should be, because a fork must be rebased on every Go release
forever, and because the alternative turns out to cover more than expected.

## What the design actually needs

| Requirement | Fork needed? |
|---|---|
| Per-isolate globals | **No** — achievable by source rewriting |
| Deterministic goroutine scheduling | **No** — the baton trick, already proven by the SDK |
| Deterministic map iteration | **No** — and the external answer is *better* |
| Deterministic `select` | **No** — source rewriting |
| Injected clock | **No** — source rewriting |
| Quiescence detection | **No** — the scheduler owns it |
| Hard kill | **Yes** |
| Span ownership / bulk free | **Yes** |
| Containment against hostile code | **Yes** (and even then, barely) |

The line falls in a meaningful place: **everything that delivers the programming
model can be done externally. Everything that delivers resource control and
containment needs the fork.**

---

## Option 1 — `-toolexec` source rewriting *(the serious one)*

`go build -toolexec=mytool` interposes on every compiler and linker invocation,
so a tool can rewrite Go source before it reaches the compiler — including
standard library packages. This is not theoretical: `garble` uses exactly this
mechanism to rewrite Go source at scale, stdlib included, and has been
maintained across Go releases for years.

What gets rewritten:

| Source | Becomes |
|---|---|
| `var x int` at package scope | a field on the per-isolate state struct |
| `x` | `cur().pkg_x` |
| `go f()` | `sched.Go(f)` — baton-scheduled |
| `select { ... }` | deterministic selector |
| `for k, v := range m` | sorted-key iteration |
| `time.Now()`, `time.Sleep()` | isolate clock |

### The "which isolate am I?" problem, and why single-threading solves it

Rewritten code needs to know which isolate it is running in. Threading an
explicit parameter through every function is the obvious answer and a bad one —
it changes signatures, which breaks interface satisfaction and every function
value crossing a rewritten/unrewritten boundary.

But **decision 5 already made isolates single-threaded.** If only one isolate
runs at a time in a process, "current isolate" can be an ordinary package-level
pointer, updated on baton handoff. No parameter threading, no goroutine-local
storage, no TLS. The rewrite becomes local and mechanical.

Parallelism then comes from **processes, not threads** — and the 10k
mostly-idle target is what makes that acceptable. With few isolates active at
any moment, a machine might run 8–16 worker processes hosting several hundred
isolates each, one active per process. The scale answer that shaped the memory
design turns out to also be what makes the fork-free approach viable.

### The bonus: cross-architecture determinism comes free

DETERMINISM.md identified architecture-dependent hashing as the one source of
map nondeterminism that no amount of seeding fixes, forcing a choice between the
portable hash's cost and pinning replay to a microarchitecture class.

Rewriting `range m` into sorted-key iteration **sidesteps it entirely.** Sorted
iteration is deterministic on any machine regardless of hash values, so
cross-architecture replay works for free and `aeshash` stays untouched on the
hot path. This is strictly better than the fork's answer, at the cost of sorting
on each range.

Experiment E1 should be re-scoped accordingly: portable hash vs. `aeshash` vs.
sorted iteration, three ways.

### What it costs

- Rewriting stdlib source is fragile; garble carries many special cases
- `runtime` itself cannot realistically be rewritten, so anything needing runtime
  internals is out
- Build times increase materially
- Debugging degrades unless `//line` directives are emitted carefully
- Packages using `unsafe` may not survive rewriting
- Global access costs a call plus a dereference rather than a register-relative
  load — slower than the fork, and exactly what E4 measures

---

## Option 2 — WebAssembly

Compile workflow code to WASM and run it in `wazero` (pure Go, no cgo).

Architecturally this is the **best** answer, and it is worth being clear about
how much it wins on:

| | WASM | The fork |
|---|---|---|
| Memory isolation | Real — separate linear memory | Invariant-based, needs a verifier |
| Per-instance globals | Free | The hardest part of the whole design |
| Snapshot | Linear memory *is* a byte array | Requires relocatable layout throughout |
| Hard kill | Drop the instance | The one thing that might be impossible |
| Bulk free | Drop the memory | Needs span ownership |
| Fork required | None | Yes |

Every hard problem in the design dissolves. The catch is the per-instance
footprint: a Go program compiled to `GOOS=wasip1` carries its own runtime and
GC inside the module, so every *instance* pays for a Go heap. The module
compiles once and is shared, but linear memories are not.

**With the corrected budget, standard Go on WASM is disqualified by arithmetic
rather than by measurement.** The target derived in IMPLEMENTATION_PLAN.md § E2 is a median of
~8KB per suspended isolate. A `GOOS=wasip1` module carries the entire Go runtime
— scheduler, GC, heap arenas — inside *each instance's* linear memory. The
module compiles once and is shared; linear memories are not. Go's runtime alone
exceeds an 8KB budget by orders of magnitude, so no measurement is going to
rescue it.

This kills the branch cheaply, which is worth something: an earlier version of
this document proposed measuring it, on a threshold (~200KB) that was itself
anchored on allocator waste rather than on requirement. Fixing the budget
answered the question without the experiment.

**TinyGo is a different matter and stays open.** Its runtime is far smaller and
a whole module can be tens of KB, so per-instance memory could plausibly land in
range. The cost is that TinyGo's goroutine and GC support is restricted enough
to constitute a different language target — which collides directly with the
"workflow code becomes ordinary Go" premise that motivates the whole project.
Worth a day to size, not more, and only if the density target is the binding
constraint.

Note there is a `sdk-go-wasm` task in this same workspace; whatever was learned
there should be pulled in before re-deriving it.

## Option 3 — Interpreter

Run workflow code in a Go interpreter (`yaegi`, or a purpose-built bytecode VM).
Isolation, determinism, snapshot, and cheap instances all follow from owning the
interpreter loop, with no fork.

The cost is performance — an order of magnitude or more — plus incomplete
language coverage. Mentioned for completeness; the right choice only if workflow
code is small and rarely hot, which for Temporal it sometimes is.

## Option 4 — One process per isolate

Perfect isolation, no fork, and fails immediately at 10k: each Go process
carries megabytes of runtime. Viable only in the hybrid form described under
option 1, where processes provide parallelism and isolates provide density.

---

## Recommendation

**Build option 1 first, and treat the fork as a later stage rather than the
starting point.**

The argument is not that the fork is wrong — it is that the external version is
*shippable rather than throwaway*, and delivers the part of the design users
actually see:

- Workflow code becomes ordinary Go — real `go`, real channels, real `time.Sleep`
- Determinism is guaranteed, and cross-architecture for free
- Per-isolate globals work, including for stdlib packages
- The `Call` API and quiescence model are exercised against real workflows
- It installs as a normal Go build tool, with no custom toolchain distribution
- Maintenance is AST-tracking, not a permanent rebase

What it cannot do is hard kill, per-isolate memory reclamation, and containment
against hostile code. Those are precisely the things a fork buys — so the fork
decision reduces to: **do those three matter enough, and when?**

That reframes the trust model too. "Hostile, subset-enforced" was chosen when
the cost of containment was a fork you were already paying for. If the fork
becomes optional, the honest question is whether containment is worth a fork on
its own.

### Revised staging

1. **E2 against the derived budget** (median ≤8KB) — no wasip1 arm needed; the
   corrected budget disqualifies standard Go on WASM without an experiment.
2. **E1 three ways** — portable hash vs. `aeshash` vs. sorted iteration.
3. **E5** unchanged — the pure-Go API mock, which is now the first increment of
   option 1 rather than a throwaway.
4. **Option 1 MVP** — `-toolexec` rewriter, baton scheduler, isolate state
   struct, `Call`, quiescence. Roughly the same milestone shape as
   IMPLEMENTATION_PLAN.md Phase 2A, without compiler or linker work.
5. **Fork only if** hard kill, memory reclamation, or hostile containment turn
   out to bind.

### The honest counter-argument

Source rewriting at stdlib scale is fragile in a way that compiler work is not.
Garble is the proof it is possible, and also the proof it is a grind — its
history is largely special cases for packages that resist rewriting. A fork is
more work up front and more predictable thereafter.

Anyone who has maintained a large-scale source rewriter should weigh in before
this is settled; the failure mode is a long tail of packages that almost work.
