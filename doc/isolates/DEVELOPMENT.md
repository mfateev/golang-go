# Build from clean checkouts

## Repositories and pins

Use four sibling directories:

```text
poc/
  golang-go/         generic Go compiler/runtime fork
  sdk-go-poc/        native SDK, tgo driver, toolchain builders and source pins
  temporal-go-sdk/   host Temporal SDK with integration bindings
  samples-go-poc/    native sample applications and nested workspace modules
```

The development branch is `task/modify-go-runtime-for-isolates`. The matching Go,
host SDK and samples revisions are recorded in SDK `toolchain/go-source.json`.
Read that file instead of deriving source identity from `go version` or assuming
a sibling HEAD is the selected dependency. `go.mod`/`go.work` filesystem
replacements select the host SDK source actually used. The current declared host
SDK dependency is v1.50.0 with binding contract 2; protobuf is v1.36.11 and OTel
trace is v1.44.0. Numeric runtime contracts are metadata/API/determinism 1.

## Prerequisites

Use Git, Python 3.11+, a C compiler and a bootstrap Go installation at least
1.26.7. Native development/CI targets Linux and macOS on arm64/amd64. Linux full
Go tests need ordinary system data such as `/etc/services` (Debian `netbase`).
Set `GOROOT_BOOTSTRAP` to the bootstrap installation, not this fork. SDK builds
use `GOTOOLCHAIN=local` and explicitly enable isolates.

## Reproducible source setup

Run in the directory that will contain the checkouts:

```sh
git clone --branch task/modify-go-runtime-for-isolates https://github.com/mfateev/sdk-go-poc.git
git clone --no-checkout https://github.com/mfateev/golang-go.git
git clone --no-checkout https://github.com/mfateev/temporal-go-sdk.git
git clone --no-checkout https://github.com/mfateev/samples-go-poc.git
python3 - <<'PYSETUP'
import json, subprocess
from pathlib import Path
pin = json.loads(Path('sdk-go-poc/toolchain/go-source.json').read_text())
for repo, key in [('golang-go', 'revision'), ('temporal-go-sdk', 'temporal_sdk_revision'), ('samples-go-poc', 'samples_revision')]:
    subprocess.run(['git', '-C', repo, 'fetch', 'origin', pin[key]], check=True)
    subprocess.run(['git', '-C', repo, 'checkout', '--detach', pin[key]], check=True)
PYSETUP
```

Build the matching source checkout in place, from `sdk-go-poc`:

```sh
export GOROOT_BOOTSTRAP=/absolute/path/to/bootstrap/go
python3 toolchain/build.py --source ../golang-go --local
export PATH="$(cd ../golang-go/bin && pwd):$PATH"
tgo version
tgo env GOROOT GOEXPERIMENT
```

`--local` deliberately includes current checkout files and records that fact in
`pkg/TGO_BUILD.json`. For a pinned snapshot with an empty destination, omit
`--local` and supply `--output /absolute/path/to/new-toolchain`; `--source` reads
the pinned commit from an existing repository without fetching. Without
`--source`, the builder fetches the recorded fork/pin. The SDK driver is built
beside the compiler and conventional Go tools. See SDK `toolchain/README.md`.

For active development use branches/worktrees at your intended revisions rather
than silently updating detached pins. Changing compiler code requires rebuilding
it; changing the tgo driver requires rebuilding `cmd/tgo`. A stock `go` does not
understand workflow directives or the isolate package. `TGO_GOROOT` is the explicit
local driver override; caller `GOROOT` cannot redirect an installed tgo bundle.

## Development cycle

From `golang-go`, rebuild generic runtime/compiler changes with:

```sh
cd src
GOEXPERIMENT=isolates GOTOOLCHAIN=local ./make.bash
cd ..
```

From `sdk-go-poc`, rebuild only the driver after driver edits:

```sh
../golang-go/bin/go build -o ../golang-go/bin/tgo ./cmd/tgo
```

Use `tgo build`, `tgo test` or `tgo run` for native applications. The driver builds
a marked temporary executable for run. `tgo install` forwards to Go and does not
perform native entry generation. Marked test variants support `test -c` but not
coverage. SDK and samples use a sibling `temporal-go-sdk` replacement; samples
`go.work` also loads SDK, codec-server and grpc-proxy modules.

Run [validation](VALIDATION.md) appropriate to the changed contract. Keep matched
experiment/compiler fingerprints for script tests. Use a separate checkout/output
for disabled-experiment builds; rebuilding installed tools while tests execute
can corrupt or invalidate those tests.

## Cache and environment hygiene

Isolate cache identity includes generated source contents and normalized relevant
compiler inputs. Unrelated entries can reuse shared dependencies. `GOCACHE` may
be placed outside repositories for targeted cleanup. After all active Go commands
finish, periodically run `./bin/go clean -cache` during extended toolchain testing.
Keep disk space for compiler and race artifacts; do not commit caches or IDE data.

For a runtime stall capture `TaskDiagnostics` before teardown. Preserve the
matching source pins, build settings and failing input for a reproducible test;
do not turn local machine incident logs into design requirements.
