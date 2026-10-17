# Minimal core migration evidence

## Starting checkout (2026-10-16, Linux arm64)

- Go: `b35501e040` (reviewed implementation `10678846d0`); upstream merge base
  `2ff5743d9fd52fac166225e75df0c2c1edf82abb`.
- SDK: `14740be322528b54fce7702ed390197b163cffcd`.
- SDK dependency checkout: `6570fab47a9f3d49969e5febce1a9736c9db7952`.
- Tracked worktrees clean. Untracked `.idea/` and Go `go-build-cache/` preserved.
- Bootstrap: `/usr/local/go-bootstrap` (Go 1.26.7). Existing fork executable:
  `bin/tgo`, Linux arm64, no GOEXPERIMENT selection. Supported distribution
  matrix: Linux/macOS, arm64/amd64; only Linux arm64 is available locally.
- The upstream inventory has 454 files, 43,156 additions and 944 deletions.
  [File inventory](MINIMAL_GO_CORE_INVENTORY.tsv) classifies every file; mixed
  files require mechanism-level review before removal.

## Baseline checks

All commands ran against the starting checkouts, with `GOTOOLCHAIN=local` from
existing toolchain defaults and the existing build cache.

- PASS: `./bin/tgo test -short -count=1 runtime isolate internal/isolatebridge
  internal/isolatepolicy cmd/internal/isolatepolicy` (runtime 39.559s).
- PASS: `./bin/tgo test -count=1 -timeout=15m cmd/go -run
  '^TestIsolateCompilerFlags$|^TestScript/build_isolate_(cache|contract|functions|support|metadata|readonly_libraries)$'`
  (38.669s).
- PASS (SDK): `../golang-go/bin/tgo test -count=1 -timeout=15m
  ./example/protobufvalues/check ./example/converter/check
  ./example/determinism/check ./workflow ./worker`. These include saved-history
  and generated protobuf checks (9.938s, 6.634s, 6.881s, 0.048s, 0.486s).
- PRE-EXISTING FAILURE: building `./example/driver` succeeds but execution panics
  in the embedded nil `WorkflowEnvironment.RegisterResetHandler`, called from
  `temporalbridge.(*definition).Execute` at bridge.go:241. This fixture predates
  the newer upstream environment method. Do not count it as a migration failure
  or a passing performance baseline (failed process: 25,492 KiB maximum RSS).
- Build-cache/relocation baseline: compiler cache script passes; distributions
  have not been rebuilt or tested during baseline establishment. Full Go suite,
  other architectures, and current remote CI were not verified by this baseline.

## Migration checklist and dependencies

1. [x] Read workspace/Go guidance and current development, determinism and
   integration documents; record revisions, worktrees and focused baseline.
2. [x] Classify the upstream diff (see inventory).
3. [ ] Establish a generic explicit entry/build manifest, keeping descriptor
   creation and privileged imports inside the toolchain. Prove it from a module
   outside GOROOT before deleting directive discovery.
4. [ ] SDK driver: discover directives using selected source files, generate
   typed adapters, choose per-instance and scratch state, validate dependency
   versions, and supply the manifest. Preserve tests and cache reuse.
5. [ ] SDK-owned pinned-source bootstrap, packaging, installers and CI. Only
   then restore conventional executable names and remove Go-owned packaging.
6. [ ] Move actual search-attribute traversals and sentinel handling into SDK
   integration; verify saved histories before deleting runtime exceptions.
7. [x] Replace protobuf/OTel compiler and runtime special cases with audited
   SDK dependency adaptations and small generic mechanisms. No arbitrary trust
   configuration or unrestricted process allocation scope is acceptable.
8. [ ] Consolidate generic compiler passes, experimental configuration and
   package-state naming; preserve provenance and scheduling enforcement.
9. [ ] Remove retired experiments after tracing consumers; retain generic
   regressions and mark historical commands accordingly.
10. [ ] Run enabled/disabled bootstrap and applicable full gates, replay,
    race, performance and distribution checks; reconcile docs and commit.

The key dependency is registration: ordinary external generated Go files cannot
import `internal/isolatebridge` or `internal/isolateproto`. A shell wrapper alone
also cannot reproduce cmd/go test-main construction or action cache identity.
The proposed small Go hook accepts explicit entry and package configuration;
SDK policy chooses it, while Go owns linking and hashes effective compiler flags
and generated source contents. This configuration must grant no metadata trust.

## Explicit build contract and SDK driver

Implemented `-isolate-config` for build/test, typed SDK-owned adapters, explicit
state and scratch package selection, and SDK source-directive discovery. Go's
public adapter API uses `isolate.Value`; privileged registration imports and
compiler-created descriptor access stay in a toolchain-generated entry.
See [BUILD_CONTRACT.md](BUILD_CONTRACT.md).

PASS: the outside-GOROOT manifest fixture, all 14 migrated generic compiler
script cases (15.533s), and the full SDK `tgo test -failfast -timeout=20m ./...`
through the new SDK driver. The focused baseline replay checks also pass through
the driver. Test-only adapters retain separate identities so tests imported as
ordinary dependencies cannot receive `_test.go` declarations.

The old dependency-source guard deliberately remains in Go while dependency-
specific compiler metadata rewrites exist. Removing it before those rewrites
would grant unaudited sources the existing name-based service privileges. This
is a transitional constraint, not a completed metadata-policy migration.

## Experiment, conventional commands and distribution ownership

Added `GOEXPERIMENT=isolates` (default off), explicit disabled-build rejection,
and a disabled public-API regression. Replaced the active `phase0_e4` build tag
and removed the Go command's discovery/reload cycle. Restored upstream executable
names, bootstrap scripts, command forwarding and command-location tests.
SDK-owned packaging, installers, integration CI and source pinning replace
`misc/tgo` and the fork's integration/release workflows. Go CI now owns only
compiler/runtime conformance with explicit enabled/disabled bootstraps.

PASS: disabled bootstrap (`GOROOT_BOOTSTRAP=/usr/local/go-bootstrap`,
`GOEXPERIMENT=noisolates`); enabled runtime/conformance package checks (runtime
108.371s under concurrent full-suite load) and focused enabled race checks
(runtime 2.351s, isolate 3.146s). The disabled full suite exposed a remaining
`go doc` command-name override and non-inlined RWMutex fast paths. Restored
upstream doc command lookup and compiled mutex/read-only probes only when the
experiment is enabled. Focused `Test/inline_sync.go`, runtime isolate internals,
sync and internal/sync checks then pass. A final full disabled rerun remains
required after all core changes.

The first enabled compiler-script run used a disabled-bootstrap compiler and
failed with the command/compiler experiment fingerprint mismatch. This is not
recorded as a passing enabled bootstrap or script gate. Rebuild the matching
configuration before rerunning it.

SDK driver discovery now respects overlays, test variants and `-C`; negative
dependency-source tests, paths-with-spaces invocation and experiment selection
pass. The example driver needed a no-op `RegisterResetHandler` for the upstream
SDK's newer environment interface; the fixture never resets cached executions.
With that fixture repair its serial/concurrent smoke passes (0.241s wall,
32,460 KiB max RSS). This cannot be compared with the failed starting fixture
as a performance regression measurement.

Samples at `0229414` now select the SDK's same sibling Temporal checkout through
a workspace override. Their full `tgo test -failfast -timeout=20m ./...` passes.
The starting sample workspace had conflicting module replacements before that
override. SDK/Go/sample revisions will be reconciled in the final handoff.

Remote CI checked with `gh run list`: latest completed fork run at `9ac0e230f3`
is [successful](https://github.com/mfateev/golang-go/actions/runs/38840464433).
This does not validate the local migration commits or the newly moved CI jobs.

## SDK-specific runtime behavior removed

The dependency checkout at `643d90e` owns a shared key index for typed search
attributes and returns ordered copied entries through integration binding v2.
Native code uses `workflow.SearchAttributeEntries`; the old map-order regression
now exercises that SDK traversal with the same 517 keys and name/type order.
The saved history is unchanged. Core deterministic maps accept only generic
integer/string keys, including for SDK-keyed maps; arbitrary interface keys
still fail before traversal. Removed both runtime SDK-layout recognition hooks.

`ErrNoData` is now an immutable named-string error constant, preserving `==`,
`errors.Is`, error text and the SDK default failure type. The previous writable
sentinel binding is retired in this POC. The SDK derives the standard error type
locally instead of reading a privileged startup global. Removed compiler SDK-init
rewriting, runtime error publication, SDK global exceptions and Go-owned SDK
source validation. SDK source/version policy remains in the SDK driver.

The core admission primitive is `FenceReadOnly`: it admits read-only services at
the next park without driving a workflow phase or host protocol. Temporal task
completion and cache policy choose when to invoke it in the SDK. No process
allocation or metadata entry API was added.

PASS: SDK-owned pinned-source snapshot bootstrap with isolates enabled; generic
compiler script gate on that snapshot (67.655s). After rebuilding the local
source through the SDK entrypoint, current core runtime/isolate/maps checks pass
(runtime 124.807s under concurrent builds), and the generic compiler/script gate
passes (110.487s). Canonical upstream unit tests run across modules with race
checks and the focused search-attribute/sentinel tests. The initial quality gate
identified missing docs on the new binding fields; doclink generation plus field
docs resolves that check. The current ordered-entry metadata probe and its saved
history pass (3.408s). Full SDK, samples and final quality gates remain part of
the final verification.

Protobuf/OTel compiler rewriting and their corresponding source guards remain;
step 4 is not complete. Removing those guards before replacing their privilege
mechanisms would weaken isolation. They are intentionally retained rather than
replaced with user-configurable trust or unrestricted owner scopes.

## Generic pass and retired implementation cleanup

Generic effect transformation and global validation now live in
`cmd/compile/internal/isolate`, with ordering documented relative to imports,
generic-body rereads, inlining, initialization, escape analysis and SSA.
Source provenance and backend lowering stay with their existing owners. The
transitional metadata adapter still runs first; its prefix table is read-only
once parallel backend work starts.

`internal/isolatestate` retains descriptor validation, initialization ordering
and frozen standard-library preparation. A concrete `*isolatePackageState` on g
replaces the opaque table plus the obsolete single-base pointer. Producers,
consumers, child inheritance and both reuse resets use the same implementation.
Local/imported lowering uses one key lookup. No E4 aliases, toy global/epoch
lowering, single-import flag or phase-specific production kill hooks remain.
The ordinary 64-bit g layout loses one pointer (32-bit alignment leaves its
size unchanged). A 386 runtime test binary cross-compiles; its type descriptor
reports g size 440. This is not a 386 execution test.

The cooperative Task/Channel/RangeMap implementation and its experiment-only
fixtures are deleted. Findings remain in the historical documents. Useful
regressions map to current native checks:

| Retired coverage | Current coverage |
|---|---|
| Initialized pointer/map/closure graph | `build_isolate_manifest` (two independent instances and unchanged host graph) |
| Exported/imported generic globals | `isolate_generics`, imported/inlined ownership/effect script cases |
| Descriptor version prefix | `internal/isolatestate/state_test.go` |
| First dispatch and admission revocation | runtime admission tests, now enabled by the isolate experiment |
| Mutex/Cond/channel/semaphore wait teardown | isolate host, lifecycle and revocation tests |
| Prototype FIFO/maps | native isolate determinism and runtime map tests |

The old large-copy stress probe exercised the retired kill path; its measured
limits remain historical rather than being presented as a current kill bound.
The current API retains cancellation deadlines and KillPending diagnostics.

Named low-level program registration was used only by tests. It now lives in
`export_test.go`; production programs come from function handles. `New` validates
program metadata before calling initialization. The outside-GOROOT contract
also rejects accessor substitution/alternate linknames and type-checks invoker
ABIs inside their source package, rather than trusting cross-package linknames.

PASS after consolidation: enabled SDK-owned local bootstrap; runtime/isolate/
state/library conformance (runtime 130.820s, isolate 4.924s under concurrent
builds); compiler/script gate (48.948s); full SDK suite; focused lifecycle and
metadata checks after test-only registry migration (isolate 1.632s); race checks
(runtime 2.303s, isolate 2.660s); static lock-ranking checks (runtime 0.108s,
isolate 0.952s). The adapter ABI/provenance negative fixture passes (5.457s).
The sample suite found one remaining direct typed-map logging loop; it now uses
SDK entries and skips unset markers, with its focused sample passing (0.327s).
The final sample/full disabled/distribution checks are recorded below once run.

Upstream canonical quality checks and focused race-enabled unit checks across
modules pass. Typed-search-attribute, child, and Continue-As-New integration
checks pass with a dev server; the zero-cache run is recorded in final evidence.


## SDK-owned library adapters (2026-10-17)

Replaced the transitional protobuf/compiler integration with SDK-owned source
adapters using typed host operations and allocation scopes. Removed the pinned
module policy from `cmd/go`, the `isolatemetadata` flag and protobuf IR rewriting,
`isolate.IsGeneratedProtobuf`, protobuf runtime registries/layout inspection and
the special reflection clone. The remaining metadata scopes and immutable table
recorder serve standard Go reflection and audited standard-library tables only.
No third-party library receives compiler metadata callback provenance.

The SDK stages dependency sources outside the module cache and adapts generated
application sources with overlays. Lazy descriptors/coder tables stay host-owned;
new message graphs and views belong to the caller. Custom implementations and
unsupported registry APIs remain SDK policy. Read-only mutation diagnostics
return through the adapter to the query boundary; fatal policy failures use
copied host-panic diagnostics. Existing transport and DataConverter execution
remain unchanged. Older sections above describe the intermediate implementation.

Generic fixes exposed by this integration preserve standard allocator provenance
through nested generic wrappers, check metadata callbacks under effect
instrumentation, reject read-only writes to opaque host objects without a
library manifest, and keep private constructors out of process `sync.Pool`
caches. Regression tests cover these mechanisms independently of protobuf.


Validation after removal: enabled and disabled toolchain bootstraps passed. The
complete enabled `go tool dist test` passed, including compiler scripts, race,
cgo, alternate link modes and `../test`. The disabled short runtime/reflect/sync/
isolate/bridge suites passed. Focused isolate/bridge tests passed three race runs
with `staticlockranking` and `GOGC=10`; Linux/386 runtime and Plan 9/amd64 isolate
test binaries cross-compiled. The complete SDK `tgo test ./...` passed with the
final adapters, including native protobuf JSON/binary operations, canonical
registry resolution, private generated defaults, workflow clock construction,
read-only queries, custom-implementation rejection and history replay.

The [runtime API reference](RUNTIME_API.md) lists the complete exported package
surface, aliased data fields/methods, ownership/lifetime rules and build interfaces.
Its function/method signatures were checked against `go doc -all isolate`.
The protobuf checker also passed actual race-instrumented concurrent-cache and
binary-value runs with the final adapters.
