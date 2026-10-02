# The Isolate Subset — Draft

Status: **draft for review**. Companion to [ISOLATES_DESIGN.md](./ISOLATES_DESIGN.md).

The subset is the language and library restriction that makes decision 2
(hostile code, contained at build time) real. It must simultaneously guarantee
four things:

1. **Containment** — no isolate can reach another isolate's or the host's memory
2. **The base-relative invariant** — no absolute address escapes into isolate memory
3. **Determinism** — execution is reproducible across replay
4. **Future forceful killability** — no code region that can run indefinitely
   without a safe termination point; this is not an MVP guarantee

A useful reference point: the Go SDK's existing `workflowcheck` linter already
encodes much of requirement 3 as advisory rules. This subset is a stricter,
soundly-enforced superset of it — the difference being that a linter can be
ignored and a toolchain gate cannot. Diffing the two lists is worth doing before
finalizing.

---

## The central problem, and the tiering that solves it

A naive reading of "ban `unsafe`" kills the entire standard library. `fmt`,
`reflect`, `sync`, `strings`, and `time` all use `unsafe`, assembly, or
`linkname` internally. A blanket transitive ban leaves you with almost nothing.

So the subset is defined over **two tiers**, exactly as V8 separates engine from
script:

| | Tier 1 — Platform | Tier 2 — Isolate code |
|---|---|---|
| What | Runtime + curated stdlib | Workflow code |
| Written by | Isolate maintainers | Tenants |
| Trust | Audited once, trusted thereafter | Never trusted |
| May use | `unsafe`, assembly, `linkname`, pragmas | None of it |
| Enforcement | Human audit | Toolchain gate |

**This tiering is where the security actually lives, and it should be stated
plainly: the containment guarantee is only as good as the Tier 1 audit.** The
ban list below is mechanically checkable and therefore the easy half.

Note that the enforcement strategy below — trapping at entry points rather than
restricting imports — deliberately makes Tier 1 *large*, essentially the whole
curated stdlib. That is a real trade. It buys library compatibility at the cost
of moving the audit question from "which packages are in Tier 1" to "is every
effectful entry point in Tier 1 guarded." The second question has more surface,
but it is enumerable and testable, where the first tends to be resolved by
excluding useful libraries.

## Enforcement — what must be static, what can trap at runtime

Because decision 3 puts the build in the platform's hands, a toolchain gate is
credible in a way a linter never is. But most of these rules do not need one.
Two questions decide where each rule lives:

1. **Is there a call site to trap?**
2. **If a rule is missed, does containment break, or only determinism?**

### Rules that cannot be runtime checks

`unsafe.Pointer` conversions, `unsafe.Add`/`Slice`/`String`, and pointer→`uintptr`
are *compile-time type conversions*. They emit no call, and in the arithmetic
cases only a few inline instructions. There is nothing to trap. Likewise
`//go:linkname` resolves at link time into a call indistinguishable from a
legitimate one; assembly bodies *are* the hostile code, with no Go-level
construct wrapping them; and `//go:` pragmas are consumed by the compiler.

These stay static — and they are exactly the containment-critical ones. So the
compiler pass is unavoidable. The good news is it shrinks to roughly six rules
and needs no knowledge of packages at all.

(cgo is the near-miss: `cgocall` is a real runtime function and *could* trap,
but `CGO_ENABLED=0` bans it at build time for free.)

### Rules that trap cleanly

Everything mediated by a single Tier 1 entry point can be a runtime check: all
of `os`, `net`, `syscall`, `os/exec`, `plugin`, `crypto/rand`, `runtime/debug`,
and the individual symbols `runtime.SetFinalizer`, `LockOSThread`, `NumCPU`,
`ReadMemStats`, and `reflect.NewAt`.

The check is one load and one branch — the isolate is already reachable from `g`
for globals addressing — on functions that are either rare or already
syscall-priced.

**This is the real simplification: it deletes the import allowlist.** That layer
is the most painful one, because import graphs are coarse. A library importing
`os` solely to write to `os.Stderr` on an error path gets rejected wholesale
even though it never takes that path. With entry-point traps it simply works.
Importing a package is harmless provided every effectful function in it traps —
`os.Args` is ambient data, not authority.

Diagnostics improve too. A build failure says "package foo transitively imports
os." A trap says which call, in which isolate, on which stack.

### Violations must be isolate-fatal, not panics

If a violation `panic`s, `recover` turns the ban into a catchable, *observable*
condition:

```go
func probe() (banned bool) {
    defer func() { banned = recover() != nil }()
    runtime.NumCPU()
    return
}
```

That is not a containment break — the operation still didn't happen — but it
lets isolate code branch on host policy. And if policy can differ between
original execution and replay, it becomes a divergence channel in a system whose
entire purpose is replay.

Use the isolate-fatal mechanism already required by section E (deadlock, OOM,
stack overflow) instead: violations terminate the isolate and cannot be
recovered. That machinery has to exist anyway, so this costs nothing.

### The asymmetry that decides the rest

Runtime traps are **fail-open**: a function you forget to guard works fine. An
import allowlist is **fail-closed**: a package you forget to classify fails the
build.

Under decision 2 (hostile code), fail-open is the wrong default for anything
that can break containment. But it is perfectly acceptable for anything that can
only break determinism — because a determinism hole is *self-revealing* (replay
diverges and you find out) whereas a containment hole is silent.

So split by consequence:

| Consequence of a missed rule | Enforcement |
|---|---|
| No call site exists to trap | Compiler pass — static |
| Containment | Package denylist — fail-closed, re-reviewed each Go version bump |
| Determinism only | Runtime trap — isolate-fatal |

The containment denylist is short and stable. The stdlib packages holding
ambient authority are `os`, `net`, `syscall`, `os/exec`, `os/signal`, `plugin`,
`runtime/debug`, `runtime/pprof`, `runtime/trace`, and `crypto/rand`. Everything
else in the stdlib is computation. A denylist is fail-open against *new* stdlib
packages, which is why the Go version bump must be a review gate — on a private
fork it already is one.

### Net effect

Three layers become: a ~6-rule compiler pass, a ~10-entry package denylist, and
runtime traps for the long tail. Link-time object verification stays as defence
in depth, but now only has to check the same six static rules. The import
allowlist — the layer that would have broken the most third-party code — is gone.

A further payoff: runtime traps can be *per-isolate policy* rather than build
configuration, so different tenants could hold different capability sets from a
single binary. That policy must be pinned per workflow execution, or it becomes
the replay-divergence channel described above.

---

## A. Banned language features (Tier 2)

| Feature | Why |
|---|---|
| `import "unsafe"` — pointer forms | Arbitrary memory access; defeats containment entirely |
| `unsafe.Sizeof` / `Alignof` / `Offsetof` | **Allowed** — constant-folded at compile time, no pointer involved |
| `import "C"` (cgo) | Arbitrary memory, and non-preemptible — breaks kill |
| `//go:linkname` | Binds to arbitrary runtime symbols, bypassing every other rule |
| Assembly (`.s` files, assembly-backed decls) | Unrestricted, and may lack stack maps — breaks kill |
| All `//go:` pragmas | `nosplit`, `noescape`, `uintptrescapes` etc. are runtime-internal contracts |
| Pointer → `uintptr` conversion | The round trip reconstitutes arbitrary pointers. `uintptr` as a plain integer stays legal |
| `reflect.NewAt` | Constructs a value at a caller-supplied address |
| `reflect.SliceHeader` / `StringHeader` | Address-bearing struct forms |

Everything else in the language is available, including goroutines, channels,
`select`, `defer`, `panic`/`recover`, generics, and `go:embed`.

## B. Address-returning APIs — rewritten, not banned

`reflect.Value.Pointer()`, `.UnsafeAddr()`, `.UnsafePointer()`, and `%p` in
`fmt` all leak absolute addresses, which breaks determinism under replay.

Rather than ban them, **they return isolate-base-relative offsets**. This is
consistent with the base-relative principle already adopted, keeps `fmt` and
`encoding/json` working, and makes the values reproducible across replay. The
rule is clean: address-*returning* APIs are rebased; address-*consuming* APIs
(`reflect.NewAt`) are banned.

This is what makes curating `reflect` viable rather than banning it — and
`reflect` has to survive, because `encoding/json` depends on it and workflow
code will want serialization.

## C. Denied packages

Enforced per the table above: the containment-critical entries are a fail-closed
import denylist; the rest trap at their entry points.

| Package | Why |
|---|---|
| `os` | File IO, ambient state, and `os.Exit` would kill the whole process |
| `net`, `net/http` | Network access; workflow code must not do IO |
| `syscall`, `golang.org/x/sys` | Direct kernel access |
| `os/exec`, `os/signal` | Process control |
| `plugin` | Dynamic code loading |
| `runtime/debug` | `SetGCPercent`, `FreeOSMemory` affect the whole process |
| `runtime/pprof`, `runtime/trace` | Process-global profiling state |
| `crypto/rand` | OS entropy — nondeterministic and a syscall |
| `testing` | Pulls in `flag` and `os` |
| `log` | Writes to `os.Stderr` — needs a host-mediated sink instead |

### Banned individual symbols

- `runtime.SetFinalizer` / `runtime.AddCleanup` — nondeterministic timing *and*
  the cross-isolate leak path identified in the design doc
- A direct ban on these symbols is insufficient for standard-library callers:
  `unique.Make` registers `runtime.AddCleanup` internally, and `net/netip`
  calls `unique.Make` for IPv6 zones. The build must check transitive effects
  or the runtime must schedule cleanup with the owning isolate

The provisional runtime now panics on direct `SetFinalizer` and `AddCleanup`
calls in an active isolate. `unique.Make` also panics before inserting into its
process-wide map, so the indirect `net/netip.WithZone` path fails early. This
enforces the current restriction while owner-aware cleanup remains pending.
- `runtime.LockOSThread` — breaks the isolate↔thread model
- `runtime.NumCPU`, `NumGoroutine`, `GOMAXPROCS`, `ReadMemStats` — observable
  process state; either banned or virtualized per isolate
- `os.Exit` — must not be reachable at all

The provisional runtime now rejects `LockOSThread`, `UnlockOSThread`,
`NumCPU`, `NumCgoCall`, `NumGoroutine`, `GOMAXPROCS`,
`SetDefaultGOMAXPROCS`, and `ReadMemStats` before reading or changing process
state. These guards currently panic, like the provisional cleanup guards;
isolate-fatal handling and an audit of other process-state APIs remain open.

## D. Virtualized — allowed, but redirected by the runtime

These stay available because banning them would make ordinary Go unwritable.
The runtime supplies deterministic, isolate-scoped implementations.

**The table below names requirements; it does not design them.** See
[DETERMINISM.md](./DETERMINISM.md) for the worked analysis — in particular, map
iteration has a source of nondeterminism (architecture-dependent hashing) that
no amount of seeding fixes, and `time` needs `nanotime` split into real and
virtual entry points so the scheduler and GC don't run on simulated time.

| API | Behavior |
|---|---|
| `time.Now`, `Since`, `Sleep`, `Tick`, timers | Per-isolate logical clock |
| `math/rand` | Seeded deterministically from isolate identity |
| Map iteration order | Deterministic across supported architectures for the proven key set; ordinary range still needs Phase 2B implementation |
| `select` among ready cases | Deterministic per isolate (runtime, not subset) |
| Goroutine scheduling | Deterministic per isolate |
| `runtime.Stack`, panic traces | Per-isolate goroutine numbering, rebased addresses |
| `os.Args`, `os.Environ` | Per-isolate values, if exposed at all |

## E. Required runtime scoping (not subset bans)

The Phase 0 E2 proof covers string and signed/unsigned integer keys, including
named types. It visits a sorted snapshot of keys and reads each value when
visited. Pointer, float, interface, array, and struct keys and reflected map
iteration remain outside that proof. A Phase 2B compiler check or runtime
implementation must enforce any narrower key contract; the current subset
document is not itself enforcement. Expanding the key set requires a portable
total order and cross-architecture replay tests.

The subset is meaningless unless these are also isolate-scoped. They are runtime
work, listed here because they are part of the same guarantee:

- **Fatal errors must become isolate-fatal, not process-fatal.** Today
  `throw` kills the process. Deadlock detection ("all goroutines are asleep"),
  concurrent map write, stack overflow, and out-of-memory must all terminate
  only the offending isolate. Deadlock detection in particular becomes
  *per-isolate* — all goroutines in isolate A asleep means A is deadlocked, not
  the process.
- **Escaping panics** terminate the isolate, not the process.
- **`sync.Pool`'s `poolCleanup` hook** — the single global registration must
  become isolate-aware. (The `Pool` struct itself is per-isolate for free under
  per-isolate globals; only the hook is ambiguous.) Preferred over banning
  `sync.Pool`, which `fmt` depends on internally.
- **Timers** — `timer.arg` must be isolate-tagged so callbacks don't fire a
  user pointer on an arbitrary P.

## F. Resource limits (runtime enforcement, not subset)

The subset cannot stop hostile code from simply consuming everything. Per-isolate
limits are needed on: heap bytes (cheap — decision 7 makes accounting a page
count), goroutine count, stack depth, and CPU time before forced preemption.

---

## Initialization and possible future templates

The first implementation runs the permitted packages' initializers for each
isolate. E4's tagged toy shows why copying static global bytes is insufficient:
`init` can allocate a map, pointer, and closure whose reachable graph would
remain shared after that copy. Reexecuting the generated initializer with a
different global base produced separate graphs in the narrow probe.

The 10k target makes repeated initialization cost worth measuring on the real
implementation. A future template may reduce that cost only after its entire
post-init pointer graph can be copied or relocated safely, as required by E5b.
The subset must also reject initializer effects that cannot be repeated
deterministically or confined to the isolate.

## What this costs

Workflow code loses IO, process control, and unsafe — but **Temporal workflow
code is already forbidden all of these**, by convention and by `workflowcheck`.
The subset is therefore far less restrictive in practice than it reads: it
mostly converts existing advisory rules into enforced ones. Activities, which
legitimately do IO, run outside isolates and are unaffected.

The real losses are narrower:

- Third-party libraries that use `unsafe` for performance won't compile for
  Tier 2 without being promoted to Tier 1 (and audited) or patched
- `crypto/rand` is unavailable, so workflow code needing randomness must use the
  seeded deterministic source
- A library may spawn goroutines that outlive a call. The MVP scheduler must
  track them and prevent another dispatch after whole-isolate revocation;
  uninterrupted computation in one of them remains outside MVP killability

## Open questions

1. **Can every effectful Tier 1 entry point be enumerated?** This replaces the
   old "is the allowlist small enough to audit" question and is the one that
   decides whether the containment claim is credible. Needs a mechanical
   derivation — ideally guards generated from a list rather than hand-written,
   so a Go version bump surfaces new unguarded entry points instead of silently
   admitting them.
2. **Does `encoding/json` survive the curated `reflect`?** If not, serialization
   needs a different answer and the reflect decision changes.
3. **Diff against `workflowcheck`** — anything it bans that this list misses is
   probably a real determinism hole.
4. **Where does the host↔isolate boundary ABI sit?** Still undefined, and it
   interacts with this: whatever crosses must be copied, which likely means
   serialization, which means `reflect`, which means question 2.
