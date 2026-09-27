# Phase 2B implementation progress

This records implemented slices of the selected compiler/runtime path. It is
not an ordinary-Go isolate conformance result. The Phase 0 decision and
measurements are in [PHASE0_RESULTS.md](./PHASE0_RESULTS.md).

## Independent initialized state

The opt-in `phase0_e4` runtime probe carries a GC-visible base pointer on each
user goroutine and copies it to a native child at `newproc1`. The opt-in
compiler flag `-d=isolatee4=1` redirects two globals in one toy package to
that base. Reexecuting the package's generated `init.0` builds independent
map, pointer, closure, and integer state for each instance. A process-owned
entry registration is run once; calls through the registered entry read the
selected instance's state. The layout's second offset is a toy-specific
constant. General layout generation, dependency initialization, and a
standard-library package case remain open.

## First-dispatch revocation

`runtime.g` now carries a pointer to a revocation group and a flag recording
whether it has been dispatched before. `newproc1` inherits the group for user
goroutines and increments its live count. `gdestroy` decrements that count.
At `execute`, a group member that has never started is discarded before its
first user instruction when the group is revoked. This path closes its race
and trace records and uses ordinary `gdestroy`; it runs no user defer.

The opt-in test creates its child in the external `runtime_test` package so
the runtime classifies it as a user goroutine and inherits the group. It
holds one P through child creation and revocation so that child is still
unstarted. A companion test checks that an admitted child does execute and
decrements the group count on ordinary exit. Both passed 1,000 native Linux
arm64 race-detector repetitions and 100 emulated Linux amd64 runs, including
`TestSizeof`. The full native `runtime` package suite passed. Run:

```bash
cd src
../bin/go test -race -tags=phase2b_revocation \
  -run='^TestIsolate(FirstDispatchRevocation|GroupCompletion)$' \
  -count=1000 -timeout=60s runtime
GOARCH=amd64 CGO_ENABLED=0 ../bin/go test -c -tags=phase2b_revocation \
  -o /tmp/runtime-revocation-amd64.test runtime
qemu-x86_64 /tmp/runtime-revocation-amd64.test \
  -test.run='^Test(IsolateFirstDispatchRevocation|IsolateGroupCompletion|Sizeof)$' \
  -test.count=100
```

This is an admission check, not `Kill(ctx)`. On multiple Ps, a child can pass
the check before revocation and execute its first instruction afterward. A
group kill must linearize admission with revocation, then wait for admitted
goroutines to stop or return a pending error. A goroutine that has already
run and parked is
outside this hook; its waiter and scheduler records need owner-aware cleanup
before it can be discarded. The group has no host-facing API or execution
count yet, so its live count alone cannot establish that no goroutine is
currently executing. The tagged test deliberately keeps creation and
revocation on one P to prove only the first-dispatch case.

## Next implementation work

1. Generate layout and pointer metadata for all isolate-owned globals in a
   package, then handle dependency initialization and a standard-library
   initialized-state case.
2. Define group membership and running-state transitions across every
   scheduling path, including parked goroutines, channels, `sync`, timers,
   preemption, and GC. Add a durable revocation fence and a host `Kill(ctx)`
   wait whose nil result proves execution has stopped.
3. Enforce the selected E2 map-key contract or provide a broader portable
   mechanism for ordinary and reflected iteration. Add logical time and
   deterministic selection only after native quiescence is observable.
