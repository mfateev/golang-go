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
first user instruction when the group is revoked. Admission and revocation
now share one atomic state, so their order is defined across multiple Ps. The
state counts admitted goroutines until they exit, but does not count only
currently executing goroutines. The discard path closes its race and trace
records and uses ordinary `gdestroy`; it runs no user defer.

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

This is an admission check, not `Kill(ctx)`. On multiple Ps, a child can be
admitted before revocation and execute its first instruction afterward. A
group kill must wait for admitted goroutines to stop or return a pending
error. A goroutine that has already run and parked is outside this hook; its
waiter and scheduler records need owner-aware cleanup
before it can be discarded. The group has no host-facing API or execution
count yet, so its live count alone cannot establish that no goroutine is
currently executing. The tagged test deliberately keeps creation and
revocation on one P to prove only the first-dispatch case.

A separate tagged race test drives admission and revocation concurrently,
checking that the admission count agrees with the winning order and that no
later admission succeeds. It passed 10 native arm64 race-detector runs, each
with 1,000 admission/revocation races. It exercises the atomic state directly;
the scheduler test above establishes the actual pre-start discard path.

## Registration model decision

The host keeps a process-wide name and handle; each isolate owns its callable,
including any captured state (confirmed 2026-09-27). The Phase 1 registry
stores a process-wide function value and therefore cannot implement that
contract for a capturing adapter. The E4 toy's registered entry does not
capture isolate state, so its test does not resolve the gap. Phase 2B must
materialize the callable for each initialized instance and resolve the stable
handle through that instance's table.
The tagged Phase 2 conformance test now has an entry adapter that captures an
initialized pointer. It still fails as expected on the Phase 1 prototype:
the second instance returns `2/2/2` rather than `1/1/1`.

## Executable package initialization probe

The compiler's opt-in `-d=isolateinit=1` mode keeps package initialization
assignments in executable code instead of moving them to the process data
image. The E4 toy now declares `epoch = 41`, then subtracts 40 in its user
`init`. A fresh state calls its generated variable initializer before
`init.0`, obtaining `epoch == 1` while recreating the independent map,
pointer, and closure graph. One hundred native arm64 race runs passed with
`-d=isolatee4=1,isolateinit=1`. This proves the order for one package; it does
not implement dependency init order or generated layout.

## Generated package layout probe

The new opt-in `-d=isolateglobals=1` compiler mode collects a package's
variables, sorts them by symbol name, builds a struct with their exact
field types and offsets, and emits the struct's runtime type for precise GC
scanning. Local global accesses use their generated offset from the selected
goroutine base; ordinary process initialization uses the original symbol when
no base is selected. The tagged runtime probe allocates this type with
`newobject`. An isolate-only toy package passed 100 native arm64 race runs
with two independently initialized map/pointer/closure/scalar graphs and a
native child inheriting the base. Run:

```bash
cd src
GOMAXPROCS=4 GOGC=20 ../bin/go test -race \
  -tags=phase0_e4,phase2b_layout \
  -gcflags='internal/isolateproto/testdata/e4layouttoy=-d=isolateglobals=1,isolateinit=1' \
  -count=100 internal/isolateproto/testdata/e4layouttoy
```

This is package-local code generation, not a whole-program layout. A separate
tagged conformance test shows that a setter inlined into a caller compiled
without the package layout writes the process global; the selected isolate
still reads epoch `1` instead of `7` with the previously built compiler. It
runs with the additional `phase2b_layout_inline` tag. For the MVP, the compiler
now declines to inline functions defined in an opt-in layout package; this
keeps callers executing its own compiled global accesses. This change has not
been tested because the diagnostic bootstrap failed with `ENFILE` and removed
the tree's tool binaries. Exported generic bodies and compiler-created helper
functions still need a separate cross-package audit. Package selection, host state
partitioning, dependency initialization, and standard-library cases remain
open.

The current container lacks `qemu-x86_64`; amd64 execution was deferred at
the user's request. The amd64 tagged runtime test binary compiled.

The first full native `src/all.bash` run after the atomic admission change
passed its main package group and alternate build modes, then failed in cgo
while opening a file in the host-mounted Go cache with `ENFILE` ("too many open
files in system"). Later runtime, race, and `../test` sections were skipped.
The host mount is `virtiofs`; `/proc/sys/fs/file-nr` was far below its limit
afterward. A same-cache focused cgo rerun passed while Linux's allocated file
count peaked at 1,840 against a 3,295,909 limit. The container also has over
1,000 zombies adopted by PID 1 (`sleep`), which does not reap them; zombies do
not hold file descriptors. The cause of the transient `ENFILE` is not
established. Focused native tagged race tests and the full native `runtime`
package suite passed.

A later unchanged full-suite rerun with the generated-layout probe failed
earlier, during bootstrap staleness checking: `cmd/link/internal/wasm` was
stale because `internal/goarch` reported a build ID mismatch. Its Linux open
file count peaked at 1,034, so this run did not reproduce `ENFILE`.
`all.bash` uses a temporary cache under `pkg/obj/go-build` for this stage and
removes it on exit; the regular host-mounted `GOCACHE` is separate. The cause
of the bootstrap diagnostic remains under investigation.

A diagnostic `make.bash` with `GOMAXPROCS=1 GODEBUG=gocachehash=1` then failed
almost immediately with `ENFILE` opening `pkg/include/asm_ppc64x.h` on the
repository's `virtiofs` mount. Linux's allocated file count afterward was 222
against a 3,295,909 limit. The failed build removed `pkg/tool/linux_arm64/*`
before it could replace them, so no further `../bin/go test` can run until a
successful toolchain build. The host-mounted build cache was not cleared;
this failure was in the repository, not the cache.

## Next implementation work

1. Verify the opt-in package inlining restriction after restoring the build
   environment, classify host-owned state, then handle dependency
   initialization and a standard-library initialized-state case.
2. Define group membership and running-state transitions across every
   scheduling path, including parked goroutines, channels, `sync`, timers,
   preemption, and GC. Add a durable revocation fence and a host `Kill(ctx)`
   wait whose nil result proves execution has stopped.
3. Enforce the selected E2 map-key contract or provide a broader portable
   mechanism for ordinary and reflected iteration. Add logical time and
   deterministic selection only after native quiescence is observable.

## Full-suite verification after filesystem recovery

After this runtime change, `src/all.bash` passed the package tests,
`runtime`, `cmd/compile`, `cmd/go`, alternate build modes, and the race
section. It failed in the final `../test` section when `/dev/vdb` returned
write I/O errors and ext4 aborted its journal. `/tmp` became read-only;
`cmd/internal/testdir` could no longer create temporary files. The worktree
was restored on its writable bind mount, but `/tmp` remained read-only.
Namespace-based reruns were inconclusive for the reasons recorded in
[DEVELOPMENT.md](./DEVELOPMENT.md). The focused native runtime suite, tagged
race tests, and emulated amd64 tests passed before the filesystem failure.
The documentation update was pushed at the user's request without another
test run.

After container recreation on 2026-09-27, `src/make.bash` passed. The first
fresh `src/all.bash` run failed only in `net` because the new container lacked
`/etc/services`: `TestCgoLookupPort` and `TestCgoLookupPortWithCancel` could
not resolve `smtp`. Installing Debian's `netbase` package restored the file,
and both focused tests passed. A complete second `src/all.bash` run then
finished with `ALL TESTS PASSED`, including the race section and `../test`.
The opt-in first-dispatch revocation tests passed 100 native arm64 race runs;
the opt-in E4 compiler toy passed 100 native arm64 race runs with its compiler
flag. No source change was needed to obtain the full-suite pass.
