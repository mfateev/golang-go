# Stable integration contracts (feature 9)

## Agreed scope

Version generated compiler metadata, the isolate API and deterministic execution
independently. Version the Temporal host/workflow transport separately. Reject
unsupported combinations before interpreting metadata or decoding workflow input.
Maintain supported SDK hooks and saved-history upgrade/rollback checks. Keep
ordinary Go SDK workflows on their existing execution path.

| Contract | Version | Producer and validation |
|---|---|---|
| Compiler metadata | 1 | Numeric version in generated function/program entries and fixed uint64 prefix in package descriptors; registration/startup checks before interpretation |
| Isolate API | 1 | `isolate.CurrentContract`; checked by the POC SDK before definition creation and in startup requests/replies |
| Determinism | 1 | Current shared random/select stream; checked as above and protected by retained observable histories |
| Temporal byte protocol | 1 | Fixed `ISOL` startup header plus four little-endian uint32 versions; validated before application decoding |
| SDK fork bindings | 1 | `internalbindings.IntegrationVersion`; exact audited module pin and definition-creation check |

Unsupported versions are rejected, including zero. There is no negotiated
downgrade or silent fallback. The startup header is internal transport metadata;
it adds no history event and changes no application Payloads. Runtime contracts
are generic and contain no Temporal dependency. Static function names remain
program identity; module checksums and embedded VCS revisions diagnose builds.

## Supported Temporal SDK integration

The existing `mfateev/temporal-go-sdk` fork has branch
`task/modify-go-runtime-for-isolates`, based on upstream v1.49.0, and immutable tag
`v1.49.0-isolates.1`. The SDK and samples replace `go.temporal.io/sdk` with this
exact version. A fourth sibling checkout is not required for sample builds.

Four additive bindings replace private-layout or numeric-flag access in the POC:

- Borrow previous failure metadata for copied-byte serialization.
- Reserve a child ID using the SDK's current, reset-aware run seed and sequence.
- Select memo conversion using the SDK's recorded encoding policy.
- Copy typed search-attribute updates, retaining unset markers and cloning lists.

Only the upstream baseline and this exact audited replacement receive existing
compiler metadata privileges. Other forks/revisions, local replacements, nested
modules and vendor sources remain rejected. The privileges themselves are not
broadened. Test fixtures may seed private SDK fields; production bridge code no
longer reads them. Existing audited library ownership manifests remain separate
from these supported SDK bindings; generic library adapters are deferred.

The portable SDK fork uses standard Go 1.26.7 for its canonical quality checks:
external analyzers currently cannot read the custom compiler's experimental export
format. Runtime/compiler, compiled isolate and replay checks use the custom toolchain.

## Compatibility and rollout policy

Keep and replay previous histories with a candidate build. Record candidate
histories and replay them with the proposed rollback build. Compare observable
results, not just command names: the determinism checker compares its full trace
against the activity result and completion retained in history. Preserve failed
fixtures for deliberate incompatibilities. The older independent random-stream
history remains a negative case; determinism 1 does not support it.

`sdk-go-poc/example/compatibility/check.sh` builds the previous immutable SDK
revision `4c2862a43c9a2f653c345addd761776b5ba5ac1a` and current source with the
current compiler. Both replay the pre-header shared-stream fixture and the
versioned-integration fixture at GOMAXPROCS 1/8. This tests SDK upgrade and rollback
within determinism 1, not arbitrary old toolchain or application-code compatibility.
The native CI matrix runs it on Linux/macOS arm64/amd64.

Deploy matching host/workflow code, pin the audited SDK dependency, record
`worker.GetIntegrationInfo()`, and run the retained replay corpus before upgrading.
Workflow versioning controls application command changes. Future incompatible
runtime behavior needs a new determinism version and a reviewed migration policy;
this POC does not automatically migrate running workflows.

## Acceptance checks

- Metadata registrations reject incompatible versions without publishing entries.
- Descriptor tests supply only an invalid version prefix, proving layout fields
  are not read before rejection.
- Compiler scripts verify numeric producer versions, public version APIs, report
  fields and invocation; trust-policy tests reject unsupported SDK sources.
- Protocol tests reject missing/truncated/incorrect headers before decoding or
  converter access; mismatches fail the Workflow Task.
- SDK fork canonical quality checks, focused unit/race tests and reset/typed
  attribute integration checks, with default and zero workflow cache.
- Compiled POC metadata, converter, child and tracing checks, saved-history replay,
  SDK/sample suites and two-revision upgrade/rollback replay.

### Local checkpoint (2026-10-10, Linux arm64)

Passed: full custom-toolchain bootstrap from standard Go 1.26.7; standard-Go
canonical SDK fork quality checks; custom-Go focused SDK
fork unit/race checks; standard-Go reset-aware child and typed-attribute
integration checks with default and zero cache; custom runtime/library regression
suite, metadata/descriptor tests and full isolate compiler script matrix; repeated
runtime/bridge race checks; full POC SDK and samples suites; protocol/bridge/worker/
search-attribute race checks; compiled metadata/converter/child race checks with
strict ownership and retained-history replay; and two-revision upgrade/rollback
replay. The upstream
memo integration test is explicitly skipped in v1.49.0; POC metadata and saved
history checks cover memo policy instead.

Each old/new history contains 195 observations. The previous and current SDKs
reproduced identical hashes at GOMAXPROCS 1/8: old history
`d32ba831efe3a3dd81f88fe400fcec9f1ebdf2be4806ada5f5527ac1d0d065b3`,
new history `98b8eb58a444fd2a202e61ce967b43984b19e28a59d610a9e04e17e45e31c072`.
Native Linux/macOS arm64/amd64 validation runs after publishing these changes.

### Native follow-up (2026-10-10)

The SDK fork's standard-Go binding CI passed. All four native platforms passed
bootstrap and runtime/compiler conformance, but SDK checks exposed an amd64
distribution-tail mismatch in the shared-stream history and synthetic test
deadline failures. The histories remain unchanged. Deterministic `math.Log`
and `math.Exp` now specify the original arm64 IEEE fused evaluation order;
65,536-input rounding checks and local saved-history replay pass. A native
bit-pattern gate exercises normal and exponential distributions with hardware
features enabled and disabled. Final native acceptance remains pending.
The broader bit-pattern gate exposed additional implicit fusion in normal
tail returns; both rand APIs now specify it explicitly. Rejection thresholds
pin float32 fused rounding using an exact product and compensated addition,
including a test for float64-to-float32 double rounding. Per-API digests make
future native failures distinguish Log, Exp and each distribution stream.
Local full math/rand suites, repeated bit-pattern race tests, exact rational
float32 checks, and unchanged-history upgrade/rollback replay pass after these
changes. A later cold full SDK run reached the integration fixtures' shared
three-minute build/execution limits; samples passed. Separating fixture budgets
is awaiting feedback, and no deadline has been relaxed.

Native run [38065454916](https://github.com/mfateev/golang-go/actions/runs/38065454916),
compiler commit `6fa9249064`, passed the distribution-rounding gate on Linux
amd64 and arm64 with CPU features enabled and disabled. Runtime/compiler and
macOS checks are still running; this is not final acceptance of the full matrix.
