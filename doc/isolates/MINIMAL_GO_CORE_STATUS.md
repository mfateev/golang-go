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
7. [ ] Replace protobuf/OTel compiler and runtime special cases with audited
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
