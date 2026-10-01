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

Imported-global address generation now uses a separate runtime lookup. A
legacy single-package base cannot serve as the base for an imported package:
the lookup panics if that base is selected without a package table, while
ordinary process initialization still uses the process global. The tagged
`e4layouttoy` cross-base test and the `encoding/base64` positive suite each
passed 100 native arm64 race runs.

The retained-dependency-record link fixture also passed with both packages
selected. When only the importer was compiled in isolate mode and its
dependency was left unselected, linking failed specifically on the key
relocation from `e4linktoy.isolateDependencyTask`.
The complete native `src/all.bash` suite passed after the runtime lookup
change, including race and `../test`.

The compiler now emits one immutable descriptor per opted-in package with its
path, identity key, layout type slot, dependency record, and initializer
record. The tagged host helper accepts descriptor pointers, preventing callers
from accidentally pairing metadata from different packages. The two-package
and `encoding/base64` tagged suites each passed 100 native arm64 race runs.
The complete native `src/all.bash` suite passed after this change, including
race and `../test`.

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

## Exported generic functions with selected globals

On 2026-09-30, the earlier exported-generic guard was replaced by generated
offset symbols for unexported globals. An exported generic body instantiated
by an importing package can now read or write those globals through the
selected package layout. Compiler-generated `.dict.` symbols remain shared
immutable metadata instead of being mistaken for package variables. The
static build script calls exported generic read and write functions from an
isolate, checks that a second instance starts fresh, and verifies that a host
using the same package retains its own value. The focused script and a
standalone `-race` build/run passed.

A temporary diagnostic build selected many public standard packages by
default while keeping runtime and process-service packages out of that probe.
It compiled and ran a small `fmt`, `strings`, and `encoding/json` workload
after the generic offset and dictionary fixes. The diagnostic selection knob
was removed; this result proves compiler feasibility for that workload, not
standard-library ownership or effect safety. Selecting every package global
with the current eager layout would also duplicate immutable standard-library
tables per instance, so default selection needs generic immutable-data sharing
or lazy materialization to meet the density target.

The generic layout change passed `src/make.bash`, the focused static-build
script and tagged generic compiler test, a standalone `-race` build/run, and
a complete Linux arm64 `src/all.bash` run.

## `time` and `context` ownership audit

`context` mostly keeps identity sentinels in package globals: `Canceled`,
`DeadlineExceeded`, `cancelCtxKey`, and the permanently closed `closedchan`.
Its `goroutines` counter is diagnostic. The mutable cancellation graph lives
in allocated context values, not a package-global registry. Selecting all of
`context` would duplicate those identities without solving callback ownership.

`time` has different state. `Local` and its zone caches initialize lazily from
the process environment or zone files. `startNano` is initialized from the
process monotonic clock. The static build now selects `time` globals per
instance; the focused script verifies fresh `Local` state in two instances
while the host retains its own value. The runtime timer heap remains
process-scoped. `time.AfterFunc` and the `context`
callback paths now reject registration from an active isolate when the later
callback could start with process ownership. `time.NewTimer`, `time.Sleep`,
`time.Now`, and zone loading still need an isolate clock and effect policy;
this audit does not classify either package as fully supported. The 10,000
prepared-instance harness measures `time` at about 1.81 KB total per instance,
roughly 1.42 KB more than the empty program; its fixed global layout is 584
bytes. The default JSON variants also reach `time`, raising their eager floors.

## Native goroutine group attachment

The trusted boundary now allocates one runtime goroutine group per instance.
`RunOwner` and `Run` attach that group while executing the initializer and
program entry. Native child goroutines inherit it through `newproc1`, and the
runtime decrements its live counter when each child is destroyed. Nested entry
into the same boundary does not count the current goroutine twice; entry into
a different instance's group panics. An internal diagnostic can read the live
count. A focused race test parks a child after its parent returns and checks
that the count drops after the child exits; it passed 100 runs. The complete
native `runtime` suite and focused static-program script also passed.

This count is not quiescence: it includes an attached host goroutine during
initialization or a direct boundary run, and it does not track timer work,
running state, or every scheduler wakeup. No host `Kill` claim follows from it.
Its prepared-instance cost is about 24 retained bytes and one object.

## Main goroutine failure reporting

The trusted host API now has `Wait`, which observes `Done` and returns a
process-owned error when the program's main goroutine panics or calls
`runtime.Goexit`. The error contains no panic value, so an isolate-owned
pointer or a user-defined formatter cannot escape through it. A focused test
checks normal return, panic, and `Goexit`. `New` runs selected package
initialization on a dedicated goroutine, returning a process-owned error for
panic, `Goexit`, or a returned error without exiting its host caller. The
returned error is generic because an initializer's error object can contain
isolate-owned data. This does not catch panics in native child goroutines,
detach waiters, or reclaim instance memory.

## Implicit standard-stream formatting

`fmt.Print`, `Printf`, `Println`, `Scan`, `Scanf`, and `Scanln` now reject calls
from an active isolate before using process standard streams. String formatting
and scanning and explicit reader/writer variants remain available. A focused
race test covers each guarded entry point and the useful local variants.
This is an effect guard, not a proof that an explicit reader or writer is
isolate-owned.

## Goroutines associated with execution threads

The runtime group now counts goroutines associated with an M. `execute`
increments the count; `dropg` decrements it after a goroutine parks, yields,
or exits. Coroutine switches transfer the count directly because they bypass
those scheduler functions. A host goroutine attached by `Run` or `RunOwner`
also counts while attached. The count deliberately includes a goroutine in a
syscall until it returns or yields its M. A focused race test covers a parked
native child and `iter.Pull` coroutine switches in 100 runs.

This remains a diagnostic, not a safe teardown condition. A zero count does
not revoke runnable or parked goroutines, detach their waiters, account for
timer callbacks, or prevent a new dispatch. Runtime work after `dropg` may
also still reference instance records. `Kill(ctx)` therefore remains open.
The waiter inventory and required detach order are in
[RUNTIME_REVOCATION.md](./RUNTIME_REVOCATION.md).

The trusted host lifecycle now calls the existing atomic group revocation
hook when `main` exits or instance preparation fails. This blocks a child
created later by an already parked goroutine from entering its first user
instruction. Two 100-run race tests release a parked child after explicit
boundary revocation and after host `main` exits, then observe that the
grandchild is discarded. Main failure and static two-program tests pass.
Already started children remain alive at this admission fence, so it is not
`Kill`.

The provisional `Call` bridge now has a stop channel. Both its command send
and reply receive select against that channel. A caller that observes stop
exits by `Goexit`. A reply can win immediately before stop, leaving that
caller active until another boundary. The host stops the bridge when main
exits or preparation fails. A reply arriving after stop remains nonblocking.
Two 100-run race tests cover an outstanding reply wait, a blocked command
send, and a main child parked in `Call`; the static two-program script passes.
`Goexit` still runs defers, and unrelated channel, semaphore, timer, and
netpoll waits remain outside this stop path.

## Process-owned Unicode regexp cache

`regexp/syntax` lazily builds a Unicode alias map through `sync.Once`. A first
use inside an isolate would write isolate-allocated data into process-global
state. The static probe's `phase0_e4` build now initializes this immutable
cache at process startup. The static two-program script compiles and matches
a Unicode class in an isolate; its ordinary and race builds pass. The broader
`regexp` effect and reader-ownership audit remains open, as does a generic
way to classify and share immutable standard-library state.

## Map owner check

`internal/runtime/maps.Map` now carries the active numeric owner at creation.
The compiler's stack-allocated small-map fast path sets the same field without
calling a runtime constructor. All ordinary assignment paths, including fast
string and integer keys, plus reflection, `delete`, and `clear`, check the
current owner before writing. A process-owned map can be read from an isolate
but cannot be changed through a local alias. The header grows by 8 bytes on
64-bit systems and 4 bytes on 32-bit systems. A 100-run race test covers
process maps, `unicode.Categories`, another isolate's map, and allowed local
maps. The static two-program script exposed and then verified the compiler
fast-path fix.
`maps.Clone` now sets the copy's owner to its caller rather than retaining
the source header's owner. Cloning a process map into an isolate is allowed;
cloning another isolate's map is rejected. The first full-suite run reached
the final test directory and exposed a missing liveness expectation in
`live_regabi.go` for the new compiler call. With that expectation updated,
the final `src/all.bash` run passed, including `GOEXPERIMENT=nojsonv2`, race,
and the final test directory.

The check does not follow pointers in map values, protect process-owned
slices, or supply deterministic map iteration. Cross-owner pointer storage
and allocation ownership remain separate work.

The next read-side check rejects lookups and iteration of another isolate's
map, including reflected access and iterator advancement after an owner
change. Process maps remain readable for immutable standard tables. The
compiler still reads `len(map)` directly in ordinary builds; isolate builds
now call `runtime.isolateMapLen`, which checks the owner and handles nil maps.
The host boundary must still not pass map aliases between owners because
their entries can contain mutable foreign pointers. A tagged 100-run race
test covers map length from the owner, host, and another isolate. The static
two-program script, compiler builtin test, and 32-bit isolate build pass after
the compiler change. The complete `src/all.bash` suite also passes.
`src/all.bash` passed with the read guard. After removing a redundant check
from map lookups, the 100-run race test, static two-program script, and
focused map/runtime tests passed again.
