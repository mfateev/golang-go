# Validation

Run commands from the stated repository with matching built tools. These are
current reproduction commands, not a record of past passes. A focused gate does
not establish a full Go or cross-platform pass.

## Generic compiler and runtime

From `golang-go` after an isolates-enabled bootstrap:

```sh
GOEXPERIMENT=isolates ./bin/go test -short runtime isolate internal/isolatebridge internal/isolatestate internal/isolatepolicy context time sync iter reflect internal/runtime/maps math math/rand math/rand/v2
GOEXPERIMENT=isolates ./bin/go test -timeout=20m cmd/go -run 'TestIsolate|TestScript/.*isolate' -count=1
GOEXPERIMENT=isolates ./bin/go test cmd/compile/internal/typecheck -count=1
GOEXPERIMENT=isolates ./bin/go test -race isolate runtime sync reflect runtime/debug -timeout=20m -count=1
GOEXPERIMENT=isolates,staticlockranking ./bin/go test -short runtime isolate internal/isolatebridge -run 'TestIsolate|TestLifecycle|TestKill|TestDeterministic|TestOwnershipFault' -count=1
```

The race runtime suite can take several minutes. The heap-dump regression is
`runtime/debug.TestWriteHeapDumpReflectType`; it exercises dynamically allocated
reflection names under race instrumentation. Keep it enabled.

Compiler scripts cover external manifests, global/init routing, imported/generic
provenance, compulsory checks, cache reuse, ownership, faults, lifecycle,
read-only execution, resources, nil-interface ordering and private embedded data.
The regenerated compiler builtin table is checked by the typecheck package.

For full ordinary Go conformance, use a separate build with
`GOEXPERIMENT=noisolates`, bootstrap via `src/make.bash`, then run
`GOEXPERIMENT=noisolates ./bin/go tool dist test`. Full isolates-enabled
`src/all.bash` is the broader compiler/stdlib gate when changing runtime internals.
A stock source build defaults to isolates disabled; tgo enables it explicitly.

## Native SDK and samples

From `sdk-go-poc`:

```sh
tgo test -timeout=20m ./...
tgo test -race -p=2 -timeout=20m ./...
tgo build -race -o /tmp/isolate-driver ./example/driver
/tmp/isolate-driver
tgo build -o /tmp/isolate-determinism ./example/determinism/check
GOMAXPROCS=1 /tmp/isolate-determinism
GOMAXPROCS=2 /tmp/isolate-determinism
GOMAXPROCS=8 GODEBUG=cpu.all=off TZ=America/Los_Angeles /tmp/isolate-determinism
```

Repeat replay with normal/disabled optional CPU features at all three processor
counts for numerical/runtime changes. The checker compares freshly computed
observations against recorded activity results and completion; corrupted-history
tests verify rejection. Command-name replay alone does not compare every payload.
The full SDK test suite includes compiled handlers, private protobuf, converters,
propagators, failures, dynamic dispatch, plugins, streams, queries and saved replay.

From `samples-go-poc`:

```bash
tgo test -p=2 -timeout=20m ./... ./codec-server/... ./grpc-proxy/...
sample_build_dir=$(mktemp -d)
while IFS= read -r sample_package; do
    if [ -n "$sample_package" ]; then
        tgo build -p=2 -o "$sample_build_dir/program" "$sample_package" || exit
    fi
done < <(tgo list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... ./codec-server/... ./grpc-proxy/...)
```

The nested modules require explicit patterns. Build one main at a time for
native registration; a multi-package build only compiles packages.
Cloud collectors/deployments and server-dependent checks are separate from unit
and compile success. SDK [TESTING.md](https://github.com/mfateev/sdk-go-poc/blob/task/modify-go-runtime-for-isolates/TESTING.md)
describes opt-in dev-server checks, cache modes, leak tests and fixture budgets.

## Behavioral acceptance

| Area | Assertions |
| --- | --- |
| State | Host plus peers are independent; imported/inlined/generic globals and init order agree; embedded data is private |
| Ownership | Foreign reads/writes/publication fail before access across scalar, aggregate, map, channel, reflection and atomic paths |
| Services | Canonical metadata remains immutable; callbacks and source-name impersonation gain no authority |
| Dispatch | Same full trace across processors/GC/peers; inbound/high-priority/read-only fences preserve queued work |
| Lifecycle | No execution after successful cleanup; startup cancellation, permanent recovery suppression, late callbacks and retained handles release roots |
| Queries/validators | Writes reject before mutation, scratch caches are private, workflow random state/continuations stay intact, completed queries work |
| Resources | Reservation rollback, GC refunds, stack/goroutine accounting, watchdog task failures and cache-idle policy |
| Transport | Copy ownership, single reply, malformed protocol rejection, schema round trip and observation drops |
| Temporal | Options/headers/payloads/failures reach host SDK correctly; cancellation/child/update/continuation semantics and cache 1000/0 agree |
| Replay | Compare computed results and payloads where the host replayer does not; validate toolchain/SDK changes with representative histories |

## CI and portability

Go `.github/workflows/isolate-core.yml` builds isolates/noisolates on Linux and
macOS arm64/amd64. SDK `.github/workflows/isolate-integration.yml` reads source
pins, builds the toolchain and runs core, compiled SDK, samples and replay gates.
`.github/workflows/isolate-context-diagnostics.yml` runs context dispatch
investigations. SDK release jobs test packaging/relocation, not full runtime
correctness. A configured job is not evidence it passed for an untested revision.

After all Go jobs finish, clean build caches as required by repository guidance.
Keep test histories as runnable fixtures; Git and CI preserve change history.
