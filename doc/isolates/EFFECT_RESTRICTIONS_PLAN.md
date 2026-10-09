# Effect restrictions: productization feature 4

Status: complete; implementation and acceptance gates passed (2026-10-07).
Features 1 and 2 are complete. Feature 3, cached-heap GC optimization, is deferred.

## Agreed contract

Workflow code executes with deterministic native dispatch, a history clock and
instance-owned state. External work is performed by activities or explicit
Temporal operations. Ordinary host code and activities keep their Go behavior.

| Operation | Workflow policy |
|---|---|
| Printing and standard logging | Format in the isolate; send copied bytes through a reserved host Write without acknowledgment. Configure output handling on the worker. |
| Environment and configuration files | Reject reads and mutations; pass configuration in workflow input. |
| Files, networking, subprocesses, process control | Reject before the effect occurs. Pure parsing, formatting and instance-local buffers remain available. |
| Time and timers | Use the supported history-clock and durable-timer implementations. Explicit time-zone data remains available; machine time-zone loading is rejected. |
| Randomness | Keep supported deterministic PRNGs; reject OS entropy and crypto/rand. |
| Runtime configuration, profiling, GC observations | Reject operations exposing machine/collector state or changing the process. |
| Finalizers, cleanup callbacks, weak/unique observations | Reject application entry points that introduce GC-dependent execution or results. Runtime-owned allocator cleanup remains internal. |
| Unsafe, native code and aliases | Reject application escape paths; permit audited runtime/library implementations with verified source provenance. |
| Violation | Permanently revoke the isolate, suppress workflow recovery and defers, and report the operation and stack trace as a Workflow Task failure. |

Existing isolate-local exit behavior is preserved: `os.Exit` and `syscall.Exit`
terminate the instance without terminating the worker process. Supported native
Go synchronization and local context cancellation remain deterministic.

## Implementation sequence

1. Share a compiler/runtime operation manifest. Make effect enforcement
   compulsory throughout isolate builds, including dependencies initialized on
   the host. Diagnose recognizable direct violations in isolate entry functions;
   retain runtime guards for helpers, indirect/reflected calls and native wrappers.
2. Extend the fatal ownership-reporting fence with effect diagnostics. Checks use
   isolate membership even during process-owned metadata services. Cleanup must
   release trusted locks before discarding application execution.
3. Introduce the reserved logging Write and worker configuration. Printing returns
   the formatted byte count and nil without waiting for delivery; host sink errors do not
   become workflow inputs. Handle initializer logging before `New` returns.
   Replay state is supplied by the host to the worker's logging handler.
4. Integrate fatal violations with Workflow Task failure reporting and cache
   teardown. Prevent worker panic configuration from turning these violations
   into Workflow Execution failures through the supported worker adapter.
5. Validate the operation matrix, alias/native bypasses, dependency and goroutine
   paths, reflection, cleanup, host compatibility, logging isolation and replay.

## Acceptance gates

- Forbidden operations perform no external effect; recovery and application
  defers cannot resume a failed workflow or suppress its diagnostic.
- Compile-time diagnostics and runtime enforcement agree on the operation
  manifest, including platform-specific syscall wrappers and generic methods.
- Printing/logging works from initializers and workflow goroutines, with copied
  records and worker-controlled handling. Host stdout/logging remains ordinary.
- Worker/replayer configuration controls replay output without changing workflow
  results, activity/timer commands or command ordering.
- Workflow Task failures carry the offending operation and isolate stack trace;
  no Workflow Execution failure command is emitted for the violation.
- All existing supported SDK/sample and saved-history replay tests remain green.
- Full `src/all.bash`, ownership/effect stress under race detection and static
  lock ranking, and native Linux/macOS arm64/amd64 CI pass before closure.

The exemptions and validation results below define the supported contract.
Hostile native-code containment, custom converters and frozen-heap GC keep
their separate productization gates.

## Enforcement and audit boundaries

The operation manifest is shared by the compiler and runtime. The build command
forces effect, heap and metadata checks across the linked dependency graph;
turning `isolateeffects` off through `-gcflags` cannot disable enforcement in an
isolate build. Helpers containing unsafe operations or unauthorized linknames
are conservatively guarded at entry, even if the unsafe branch is not taken.
Host calls to those helpers preserve normal behavior.

Dynamic functions, interface dispatch, goroutine/defer targets and reflection
are checked before invocation. Application native wrappers are rejected. Native
standard-library implementations carry verified GOROOT source provenance.
That provenance authorizes their implementation, not application callbacks or
process-owned allocation. Reflection's unsafe accessors require an audited
implementation caller; reflected application calls cannot inherit that trust.

Only generated entry aliases are authorized for source entry wiring. A source
package cannot gain privileges by naming itself after an implementation package
or by choosing a generated-looking function name.

Two narrow value-transfer exceptions support ordinary Go implementation details:

- `internal/abi.Escape` is the verified compiler escape-analysis stub. Its dead
  process store is behind an immutable false condition; its exemption grants no
  allocation service or callback privilege.
- `reflect.Type.Method` can publish immutable function entry references. The
  exception does not authorize reading or modifying executable memory.

Trusted lifecycle callbacks use named handlers so edits to initializer logging
cannot change their audited identity through anonymous-function renumbering.
Violation checks remain active inside metadata services, whose cleanup finishes
before application execution is discarded. Diagnostics are copied into host
storage, with the offending goroutine's stack bounded to 64 KiB. Retaining a
reported error does not retain the private instance heap.

Stack capture allocates its diagnostic buffer before saving the offending
goroutine's stack pointer. Allocation can move the stack; retaining a numeric
pointer across that allocation caused an unwinder crash in the first native
acceptance run. The regression test forces stack movement during effect
reporting, and concurrent fault/kill tests exercise the reporting race.

Metadata registry mutation and unaudited service callbacks use the same fatal
effect reporting path. Nested isolate creation is a host operation, checked
before allocating another group or advancing its process identity counter.
Interrupted builtin print state is cleared when its goroutine is destroyed or
reused, and the print flag occupies existing padding in the goroutine record.
The reserved logging operation cannot be configured as a durable timer opcode.

## Worker integration

Printing, standard `log` and default `slog` use the reserved `isolate.LogOp` Write.
The host configures `worker.SetIsolateLogHandler` on an isolate worker or replayer,
either before or after registration. Custom handlers receive copied messages,
source, workflow/run/type identifiers and replay status. A nil handler restores
the SDK logger and its `EnableLoggingInReplay` behavior. There is no reply;
logging configuration and sink failures are not workflow inputs. Host handler
panics become diagnostics rather than Workflow Task failures. The SDK consumes
Writes during task/query dispatch and drains final records before releasing
completed or revoked state. The POC queue holds 64 messages of at most 64 KiB;
oversized records and full-queue writes are dropped, as for other observations.

Effect faults panic only on the host after revoking and closing the instance.
The standard SDK's `BlockWorkflow` policy converts that panic to a Workflow Task
failure. The supported worker adapter rejects isolate registrations configured
with `FailWorkflow`, while preserving ordinary workflow registration behavior.
Callers wrapping a worker must pass its original nondefault options. Low-level
factory registration also requires the SDK's `BlockWorkflow` policy.

## Validation evidence

Local acceptance passed on Linux arm64 on 2026-10-07:

| Gate | Result |
|---|---|
| Full compiler/runtime/standard library suite | Final `src/all.bash`: `ALL TESTS PASSED`. |
| Compiler integration | All seven scenarios passed, including mandatory enforcement, native/alias/reflection rejection, logging and isolate-only exit. |
| Runtime stress | Ownership, effect faults, deterministic dispatch, reflection registries and cached eviction passed with race detection and static lock ranking, each repeated five times. |
| SDK | Package tests and recorded-history replay passed. File and metadata-operation faults never invoked workflow execution completion. |
| Samples and SDK driver | All tracked sample packages passed. Serial/concurrent paths passed normally, with race detection, and under `GOGC=1` with `GOMAXPROCS=1,2,8`. Metadata rejection cases expect fatal revocation and cannot invoke their application callbacks. |
| Fresh-process replay | Six variants (`GOMAXPROCS=1,2,8`, default/disabled CPU features) reproduced the same 195 observations and SHA-256 `12500bc0e73b412e9166503f4c1cb009db6259375824d5a7a47e528439646916`. |
| Live Temporal Server 1.32.0 | Logging completed; file and metadata violations produced actual Workflow Task failure history events carrying operation and isolate stack, with no Workflow Execution failure/completion. Test executions were terminated after inspection. |

The final reporting regression passed with forced stack movement; concurrent
fault stress also passed twenty repetitions under both race detection and
static lock ranking. The final full suite was rerun after that fix.

Native acceptance uses these repository revisions:

| Repository | Revision |
|---|---|
| `mfateev/golang-go` | `40c683eeac61b606869ddf84c855f4e64dd477c2` |
| `mfateev/sdk-go-poc` | `c0116cf55c3841b8ec3f922c4f84bf10009dfae1` |
| `mfateev/samples-go-poc` | `a2f364746f75cb42dd0381c46597b5e8c782b94d` |

All four native Linux/macOS arm64/amd64 jobs passed in
[run 37557501679](https://github.com/mfateev/golang-go/actions/runs/37557501679).
Each runner passed runtime/compiler conformance, repeated race and static lock
ranking stress, all seven compiler integration scenarios, SDK/sample tests,
serial/concurrent drivers and six fresh-process replay variants. The downloaded
artifacts confirmed the exact revisions above and the same observation hash
across all 24 native replay executions.

Full test output is captured separately for failure review. The native workflow
uploads logs and the exact three repository revisions used by every runner.
Local final-suite output is in `/tmp/feature4-all-final.log`; downloaded native
artifacts are in `/tmp/feature4-native-final/`. These local paths are temporary;
the linked CI run is the retained acceptance record.
