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

## Package-state partition and dependency initialization probe

The current whole-package ownership classification and remaining audit are in
[PACKAGE_STATE.md](./PACKAGE_STATE.md). A tagged two-package probe now uses a
package identity key to select each package's generated layout while a single
goroutine calls across the dependency edge. The test reruns both packages'
variable and user initializers in dependency order, verifies that the importer
observes the dependency's initialized state, and then mutates both graphs in
two independent instances. A native child inherits the package table.
One hundred native arm64 race runs passed. The tagged `NewPackageInstance`
helper now accepts an explicit manifest, validates selected dependencies,
sorts their initializers topologically, and creates the state table. The test
lists the importer first to check ordering. The compiler now emits an immutable
per-package initializer record, separate from the process init task's state,
and a list of direct imports selected by `-d=isolateimports`. The host helper
uses that list for ordering and missing dependency validation; no dependency
strings are supplied in the host manifest. Whole-program package selection and
automatic dependency selection remain open. The host helper replays the
initializer record for the dependency toy and `encoding/base64`; both tagged
tests passed 100 native arm64 race runs. The new
`runtime.g` field changed `TestSizeof`; after updating its checked size, the
focused native `runtime` test passed. A Linux/386 runtime test binary compiled,
but was not executed. The complete native `src/all.bash` suite then passed,
including its race section and `../test`.

```bash
cd src
GOMAXPROCS=4 GOGC=20 ../bin/go test -race \
  -tags=phase0_e4,phase2b_layout,phase2b_dependency \
  -gcflags='internal/isolateproto/testdata/e4...=-d=isolateglobals=1,isolateinit=1' \
  -gcflags='internal/isolateproto/testdata/e4importtoy=-d=isolateglobals=1,isolateinit=1,isolateimports=internal/isolateproto/testdata/e4deptoy' \
  -count=100 internal/isolateproto/testdata/e4importtoy
```

The generated selected-dependency record also compiled in a Linux/386 tagged
test binary. After the workspace switched to a network-restricted sandbox,
`src/all.bash` failed in `context.ExampleAfterFunc_connection` because opening
a loopback socket returned `operation not permitted`. The run was stopped;
both tagged race tests then passed 100 runs inside that sandbox. The last
complete `src/all.bash` pass was on the preceding initializer-record commit.

## Executable package initialization probe

The build-wide `-d=isolatepackages=path1:path2` probe now applies one selected
set to every compiler invocation. A selected package gets its layout and
replayable initializer mode automatically; all importers route direct accesses
to selected package globals through the current instance. The compiler also
records selected direct imports from this set. The external `e4importtoy`
test package reads and writes an exported dependency global in two instances
and verifies that the process global is unchanged. Its tagged suite passed
100 native arm64 race-detector runs:

```bash
cd src
GOMAXPROCS=4 GOGC=20 ../bin/go test -race \
  -tags=phase0_e4,phase2b_layout,phase2b_dependency,phase2b_autoselection \
  -gcflags='all=-d=isolatepackages=internal/isolateproto/testdata/e4deptoy:internal/isolateproto/testdata/e4importtoy' \
  -count=100 internal/isolateproto/testdata/e4importtoy
```

This is a consistent opt-in selection mechanism, not package discovery or a
whole-program classification proof. Selected-package reachability and a
standard-library state audit remain open.
The existing `encoding/base64` tagged suite passed 100 native arm64 race runs
with the same build-wide flag selecting `encoding/base64`. The complete native
`src/all.bash` suite passed after this change, including race and `../test`.

Selected direct-import records now carry a relocation to each dependency's
generated package key, alongside its path. A retained record cannot link if
the selected dependency has no generated layout key. The tagged host helper
also rejects a manifest whose key differs from the compiler's record before
replaying initializers. The two-package tagged suite passed 100 native arm64
race runs with this format. The `encoding/base64` tagged suite passed 100
native arm64 race runs, and the complete native `src/all.bash` suite passed,
including race and `../test`.

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
keeps callers executing its own compiled global accesses. After the
container-local cache restored the build, the cross-package setter passed
100 native arm64 race runs. An exported generic setter still wrote the
process global because its body was instantiated in the importing package.
The opt-in compiler now rejects exported generic functions and methods; a
tagged compiler guard test checks both diagnostics. This is a conservative MVP
restriction until importing compiler invocations can use the owning package's
layout. Compiler-created helper functions still need a cross-package audit.
Package selection, host state partitioning, dependency initialization, and
standard-library cases remain open.

## Standard-library initialized-state probe

The opt-in compiler now emits a package-owned offset symbol for each exported
global. A caller using `-d=isolateimports=encoding/base64` loads that offset
and the package identity key, then selects the current instance's address.
The generated layout metadata and initializer record are explicitly linkable,
which permits the tagged test to use a standard-library package. Two reruns
of `encoding/base64` initialization create separate standard, URL, and raw
encoding pointers. A direct `StdEncoding` assignment in an importing package
changes only one selected instance; 100 native arm64 race runs passed.

```bash
cd src
GOMAXPROCS=4 GOGC=20 ../bin/go test -race \
  -tags=phase0_e4,phase2b_stdlib \
  -gcflags='all=-d=isolateimports=encoding/base64' \
  -gcflags='encoding/base64=-d=isolateglobals=1,isolateinit=1' \
  -count=100 internal/isolateproto/testdata/e4base64toy
```

The imported-package flag is manual but can be applied to all compiler
invocations in one build. A tagged test now checks direct accesses in the
test package and two separate importing packages; 100 native arm64 race runs
passed with that build-wide setting. Automatic package selection, automatic
dependency selection, and the standard library's process-state audit remain open; this
probe does not establish whole-program isolation.
The complete native `src/all.bash` suite passed after this change, including
the race section and `../test`.

After the generated initializer record and host replay helper were added,
the dependency and `encoding/base64` tagged tests each passed 100 native
arm64 race runs. A subsequent complete native `src/all.bash` run also passed,
including its race and `../test` sections. The tagged two-package test binary
compiled for Linux/386; it was not executed. Package dependency selection and
transitive caller coverage still require explicit host/build inputs.

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

1. Turn the explicit package partition and dependency/standard-library probes
   into whole-program package selection, transitive caller coverage, and
   automatic dependency order. Audit compiler-created helpers crossing
   package boundaries and the selected standard-library packages' process
   state.
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

On 2026-09-28, the same missing `/etc/services` caused the first full-suite
run after the exported-generic guard to fail only the two `net` cgo port tests.
After reinstalling `netbase`, both focused tests and the complete `src/all.bash`
rerun passed, including the race section and `../test`. The cross-package
inlining test passed 100 native arm64 race runs; the tagged generic guard
test passed.
