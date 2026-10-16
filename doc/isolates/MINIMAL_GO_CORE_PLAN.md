# Minimal Go isolate core execution plan

Progress and baseline evidence: [MINIMAL_GO_CORE_STATUS.md](MINIMAL_GO_CORE_STATUS.md).

## Objective and agreed decisions

Keep a small, generic isolate implementation in the Go fork that follows the
surrounding compiler and runtime architecture. Move Temporal-specific behavior,
integration policy, and toolchain orchestration into `sdk-go-poc`. Preserve the
current supported workflow behavior and isolation guarantees while doing so.

The user explicitly approved moving the building of `tgo` and the custom Go
runtime into `sdk-go-poc`. That repository may own the driver, build scripts,
toolchain source revision selection, packaging, installers, and release jobs.
The compiler and runtime source changes themselves should remain a focused,
reviewable Go fork; moving build ownership does not require copying the Go tree
into the SDK repository.

Keep experimental findings and useful regression tests. Remove superseded
experimental implementations, toy-specific compiler paths, obsolete flags,
and hooks used only by retired experiments. Git history preserves old code.
Maintain one current implementation; backward compatibility with old POC
interfaces is not required. This does not authorize silently changing replay
behavior or weakening the supported API and isolation guarantees.

This is an implementation handoff. The review that produced it made no code
changes and did not run behavioral tests. Recheck the current source and CI
status before execution. Initial reviewed Go revision: `10678846d0`.

## Repository boundaries

| Keep in the Go fork | Own in sdk-go-poc |
|---|---|
| Package-state layouts, address lowering, and per-instance initialization | Workflow discovery, registration, and generated entry adapters |
| Memory ownership checks, allocation attribution, and reference transfer enforcement | Temporal payloads, failures, search attributes, and sentinel handling |
| Goroutine membership, revocation, resource accounting, and necessary scheduling enforcement | Workflow-task phases, query/update policies, delivery sequencing, and worker deadlines |
| Generic clock, timer, randomness, and host transport mechanisms | Durable timer protocol, replay integration, and Temporal command encoding |
| Necessary generic immutable-data and metadata mechanisms | Protobuf, Temporal API, SDK, and OTel dependency integration and version policy |
| Compiler/runtime conformance tests using generic fixtures | SDK compatibility, saved-history replay, samples, packaging, and release checks |

Runtime enforcement needed to control native goroutines must remain in Go.
Do not move scheduler internals into ordinary SDK code. Expose only the small,
generic controls the SDK actually needs, with explicit ownership and lifecycle
contracts. Avoid a general plugin framework or arbitrary privileged callbacks.

## Initial findings and source map

Paths below are relative to the Go repository unless stated otherwise.

1. `src/cmd/go/internal/work/isolatefunc.go` discovers `//temporal:workflow`.
   `isolate.go` selects state, generates integration entries, and checks external
   module versions. `isolateflags.go` includes dependency-sensitive compiler
   configuration in invocation and cache identity. Move Temporal orchestration
   to the SDK driver without losing those build invariants.
2. `src/cmd/internal/isolatepolicy/metadata.go` pins protobuf, Temporal API,
   Temporal SDK, and OTel details. `src/cmd/compile/internal/ir/abi.go` derives
   metadata trust partly from external package names. These are integration
   policies, not permanent compiler semantics.
3. `src/runtime/isolate_search_attribute_key.go` recognizes SDK private types and
   layouts; `src/internal/runtime/maps/isolate_iter.go` calls its special cases.
   Replace this with SDK-owned deterministic traversal or representation.
4. `src/cmd/compile/internal/typecheck/isolate_metadata.go` rewrites protobuf
   implementations and generated functions using names, filenames, and IR
   shapes. It also publishes Temporal's `ErrNoData` during SDK initialization.
   `src/runtime/isolate_error_metadata.go`, `isolate_protobuf.go`,
   `isolate_protobuf_cache.go`, and portions of `isolate_metadata.go` encode
   corresponding library-specific knowledge.
5. Effect transformations live in `typecheck/isolate_effects.go`; package body
   validation lives in `pkginit/init.go`. A dedicated isolate compiler pass
   should own generic validation and transformation, with explicit ordering.
6. `src/cmd/compile/internal/ssagen/ssa.go` still recognizes the E4 toy package's
   `global` and `epoch` variables alongside generated package layouts.
   `base/debug.go`, runtime build tags, fields, hooks, and comments retain phase
   terminology. Some E4-named code is used by the current implementation, so
   classify references before deleting it.
7. `misc/tgo`, `.github/workflows/tgo-release.yml`, and tool-name substitutions
   throughout Go are candidates for SDK ownership. An SDK-owned launcher can
   expose `tgo` while its bundled Go toolchain retains conventional internal
   executable names.

## Execution sequence

### 1 Establish the baseline and inventory

- Read applicable `AGENTS.md` files in both repositories and existing development,
  determinism, ownership, and integration documents.
- Record source revisions, worktree status, supported platforms, and the current
  build and test entrypoints. Preserve unrelated local files and changes.
- Inventory added or modified Go files against the upstream merge base. Classify
  each mechanism as generic core, SDK integration, retained test, historical
  finding, or superseded experiment. Trace callers, linknames, generated symbols,
  build tags, and CI references before removal.
- Establish passing focused runtime/compiler, SDK, and saved-history checks.
  Record pre-existing failures separately. Capture relevant build-cache,
  relocation, startup-memory, and execution performance baselines.

Deliverable: a concrete migration checklist and baseline evidence, including
dependencies between removals. Update this plan as facts are established.

### 2 Move toolchain ownership into the SDK

- Add SDK-owned build and packaging entrypoints that fetch or use an explicitly
  pinned Go fork revision. Keep local development checkout support explicit.
- Establish a generic compiler/core entry-registration contract before moving
  registration generation into the SDK. Current generation injects imports of
  `internal/isolatebridge` and `internal/isolateproto`, constructs state factories,
  publishes compiler descriptors, and supplies entry-alias compiler flags.
  Ordinary external generated sources cannot import those internal packages.
  Define supported access to compiler-created entries, descriptors, and state
  factories, with provenance and validation enforced by the core. Do not expose
  arbitrary descriptor pointers or privileged callbacks as the replacement.
- Add a generic fixture outside GOROOT that builds and executes through that
  contract, including per-instance package initialization and generated entry
  invocation. Require this fixture and migrated SDK builds/tests to pass before
  removing the existing Go orchestration.
- Move workflow discovery, registration generation, dependency/version policy,
  installers, release metadata, and Temporal integration CI into the SDK.
- Preserve the `tgo` user command through the SDK driver. Preserve ordinary Go
  command forwarding, application/test builds, package selection, generated
  entry linking, and useful diagnostics.
- Prefer conventional Go executable and internal tool names. Remove branding
  substitutions that exist only to rename the toolchain, once the launcher and
  distribution checks work.
- Determine how the external driver supplies per-package compiler configuration
  and generated sources using supported Go command mechanisms. Do not assume a
  shell wrapper alone can reproduce the current action graph or cache identity.
  If a generic Go build hook is necessary, document the exact requirement and
  keep that hook free of Temporal names and module policy.
- Verify build/test cache correctness when source, package selection, entrypoints,
  and instrumentation settings change. Preserve dependency cache reuse across
  programs and test packages, including external test packages.

Deliverable: SDK-owned `tgo` builds and tests the existing examples; the Go command
does not discover Temporal directives or enforce external dependency pins.

### 3 Remove Temporal behavior from the core

- Move search-attribute ordering into SDK-owned code. Inspect actual call paths
  in upstream SDK types: an SDK wrapper is insufficient if upstream methods
  still iterate interface-keyed maps inside an isolate. Adapt the integration,
  representation, or dependency implementation at the SDK boundary as needed.
- Replace `ErrNoData` and other Temporal global exceptions with SDK-owned
  initialization/representation or a narrowly defined generic sharing contract.
  Preserve observable sentinel identity where supported code relies on it.
- Move workflow-specific naming and phase selection out of runtime and public
  isolate APIs. Retain generic scheduling/admission controls where enforcement
  requires the runtime; do not merely rename Temporal policy in place.
- Keep Temporal command IDs, serialization, retries, task budgets, queries,
  updates, and saved-history policy in the SDK.

Deliverable: no Temporal module paths, concrete types, dependency pins, workflow
markers, or workflow-specific algorithms in production compiler/runtime code.

### 4 Replace library-specific metadata rewriting

- Separate generic reflection/type-sharing needs from protobuf and OTel adapters.
- Implement library integration in SDK-owned adapters, generated sources, or
  explicitly patched dependencies. Select the smallest mechanism after tracing
  the current supported converter and generated-protobuf behavior.
- Remove compiler pattern matching on protobuf private functions, generated
  filenames, body shapes, and external module names once replacements pass.
- Remove runtime knowledge of protobuf private layouts and caches once generic
  primitives and SDK integration cover their required behavior.
- Preserve canonical type identity, lazy initialization synchronization,
  allocation ownership, panic cleanup, and revocation behavior. Application
  callbacks must never inherit process allocation or metadata privileges.
- Do not replace fixed allowlists with arbitrary user-configurable trust lists
  or an unrestricted enter-process-owner API. Define and test the trust boundary
  before exposing any generic mechanism.

Deliverable: external library upgrades do not require compiler/runtime edits;
the core exposes only documented, generic contracts needed by the integration.

### 5 Consolidate compiler and runtime structure

- Put generic isolate IR validation/transformation in a dedicated compiler
  package. Document order relative to import, generic instantiation, inlining,
  escape analysis, package initialization, and SSA generation.
- Keep provenance across export/import, inlining, closures, and generic
  instantiation. Keep backend tables immutable during parallel compilation.
- Keep SSA lowering in `ssagen` and metadata emission with its relevant owner;
  avoid turning the new package into a collection of unrelated helpers.
- Follow nearby runtime lock, lock-rank, write-barrier, system-stack, pointer
  lifetime, race-detector, and goroutine-reuse patterns. Review every new field's
  initialization, inheritance, reset, and GC visibility.
- Use the repository's `GOEXPERIMENT` machinery for coordinated experimental
  feature enablement. Document the default and the bootstrap/build settings for
  enabled and disabled configurations. Have the SDK driver explicitly select and
  verify the required configuration for isolate builds/tests, including installed
  and local-source toolchains. Retain debug flags only for useful diagnostics and
  development controls; remove superseded modes and phase-specific tags.
- Reduce opaque pointers where concrete runtime types can express the contract.
  Keep justified unsafe ABI boundaries small and documented.

Deliverable: a generic compiler/runtime implementation whose organization and
invariants follow adjacent Go code.

### 6 Remove retired experiments and reconcile documentation

- Delete the toy-package global/epoch lowering, obsolete probe paths, flags,
  stubs, and production diagnostics used only by retired experiments.
- Rename retained E4/phase implementation symbols around their current purpose.
  Update producers, consumers, tests, generated metadata, and build scripts in
  the same migration; do not add compatibility aliases for old POC names.
- Convert useful experimental tests into generic regression tests. Use runtime
  test exports for runtime-local internals where appropriate; retain production
  hooks only when a real cross-package integration requirement remains.
- Preserve measured findings, constraints, and rejected approaches in historical
  documents. Mark retired commands and implementations as historical so another
  agent does not restore them as requirements.
- Keep an unresolved experiment outside production compiler/runtime paths only
  when it answers a named open question with a clear exit criterion.
- Update design/API/development docs, README links, and task status to describe
  the final ownership boundary and current implementation.

Deliverable: one supported isolate implementation, with useful findings and
regression coverage retained independently of obsolete experiment code.

## Verification and completion criteria

Use existing test entrypoints discovered in step 1. Run focused checks after
each change and the full applicable gates before completing the migration.

- Ordinary non-isolate Go builds and tests still work. The SDK driver forwards
  ordinary commands correctly and builds/tests marked workflow programs.
- Bootstrap succeeds with the isolate experiment explicitly disabled and enabled.
  Run the full ordinary Go suite with it disabled, and the isolate compiler,
  runtime, race, SDK, and replay gates with it enabled. Record the actual bootstrap
  and build settings for each configuration; enabled checks alone do not establish
  that the disabled configuration works.
- Verify that the SDK driver selects and checks the required experiment for
  isolate builds/tests across installed and local-source toolchains. Unsupported
  configurations fail clearly before an isolate can run without instrumentation.
- The outside-GOROOT fixture builds and executes using the generic registration
  contract, without Temporal dependencies or direct imports of Go internal
  packages. Entry registration preserves compiler-metadata provenance and
  per-instance initialization.
- Compiler script tests pass, including imports, inlining,
  generics, package initialization, negative enforcement cases, and build-cache
  invalidation/reuse.
- Runtime ownership, reference transfer, readonly access, resource accounting,
  goroutine reuse, synchronization waits, revocation, and race checks pass.
- SDK converters, protobuf values, search attributes, sentinel errors, timers,
  queries, updates, tracing, worker lifecycle, and sample checks pass.
- Saved histories replay with unchanged observable results across supported
  platforms and concurrency settings. Investigate differences before changing
  fixtures; fixture regeneration is not proof of preserved behavior.
- Relevant startup-memory and execution benchmarks show no unexplained material
  regression against the recorded baseline.
- SDK-owned distributions build from pinned source and pass fresh-install,
  relocation, paths-with-spaces, offline/local-source, and command-forwarding
  checks where those modes are currently supported.
- Production Go code contains no Temporal/protobuf/OTel module pins, SDK private
  type cases, workflow discovery, or superseded toy/phase paths. Historical docs
  and SDK integration tests may retain those terms. Classify search hits rather
  than treating ordinary uses of the word temporal as dependencies.
- Review the final Go diff against upstream for repository-native organization,
  comments, naming, formatting, and unnecessary changes outside isolate support.

Work in cohesive commits and finish/commit each step before starting unrelated
work, following the workspace instructions. Cross-repository commits must record
matching revisions so another agent can reproduce a working checkout. Do not
publish releases or deploy as part of this plan without an explicit request.
Record completed steps, commands/results, remaining limitations, and the final
Go and SDK revisions in the handoff.
