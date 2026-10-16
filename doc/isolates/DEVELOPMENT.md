# Development and environment recovery

This file is for an agent starting from this Git repository alone. Read
[TASK_STATUS.md](./TASK_STATUS.md) and
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md) before extending the implementation.

## Current toolchain ownership

The Go fork builds conventional `bin/go` and `bin/gofmt`. The isolate experiment
is disabled by default. Bootstrap and test each configuration explicitly:

```sh
cd src
GOROOT_BOOTSTRAP=/usr/local/go-bootstrap GOEXPERIMENT=noisolates ./make.bash
cd ..
GOEXPERIMENT=noisolates ./bin/go tool dist test
cd src
GOROOT_BOOTSTRAP=/usr/local/go-bootstrap GOEXPERIMENT=isolates ./make.bash
cd ..
GOEXPERIMENT=isolates ./bin/go test -short runtime isolate internal/isolatebridge
GOEXPERIMENT=isolates ./bin/go test cmd/go -run 'TestIsolate|TestScript/.*isolate'
```

Use matching bootstraps for compiler-script checks: rebuilding cmd/go under a
different experiment from its installed compiler can create version-fingerprint
mismatches. Normal application builds select the experiment through the SDK
launcher. The [generic manifest contract](BUILD_CONTRACT.md) describes Go's hook.
The SDK owns `cmd/tgo`, source pinning, bootstrap/packaging scripts and installers.
For a local development checkout, run the SDK's
`python3 toolchain/build.py --source /absolute/path/to/golang-go --local`.

The recovery notes below document earlier development environments. References
to Go-owned `bin/tgo`, phase tags and packaging are historical; use the SDK-owned
launcher and coordinated experiment above for new work.

## Checkout and build

The active branch is `task/modify-go-runtime-for-isolates` on the fork
`mfateev/golang-go` (`origin`). Push only to that branch on `origin`. The
development target is Linux arm64. The original sandbox image was
`sandbox-go:latest`, with `GOROOT_BOOTSTRAP=/usr/local/go-bootstrap` (Go
1.26.7). The worktree and its Git metadata were on a host bind mount; the
container root filesystem and Go build cache were disposable.
The latest runtime implementation slice is commit `32e548f9bc`.

From the repository root in a healthy container:

```bash
git status --short
git branch --show-current
test -w /tmp && touch /tmp/isolate-write-check && rm /tmp/isolate-write-check
cd src
./make.bash
./all.bash
```

Do not count a toolchain build or a focused runtime test as a full-suite pass.
After container recreation on 2026-09-27, `src/all.bash` passed in full; see
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md). The fresh container initially
lacked `/etc/services`, causing two `net` tests to fail until Debian's
`netbase` package was installed. Include `netbase` in future container setup.

## Compiler cache reuse

Isolate compilation enables compulsory checks with `-d=isolatebuild=1`, even
when a dependency has no selected private state. The compiler receives package
selection and entry lists limited to its transitive import graph. The command
uses exactly those flags for both compilation and cache hashing; ownership,
instrumentation, imported globals, and inlined entry diagnostics remain part of
the identity. Unrelated workflows can therefore reuse shared dependencies.

`TestIsolateCompilerFlags` checks flag normalization and ownership distinctions.
`TestScript/build_isolate_cache` warms host-only code, builds two independent
programs, checks private state, verifies shared compile actions were cached,
and checks privilege rejection with an empty state selection.

## Why the previous container needed replacement

During `src/all.bash`, `/dev/vdb` returned write I/O errors and ext4 aborted
its journal. Later checks showed `/` mounted as `ext4` with `emergency_ro` and
`touch /tmp/isolate-write-check` failed with `Read-only file system`, despite
hundreds of GiB of free space. The source bind mount remained writable.
`sudo mount -o remount,rw /` was denied inside the container. Freeing disk
space did not reset the aborted journal.

Attempts to run the suite in a private user/mount namespace did not establish
a pass: the first tmpfs filled; moving Go scratch files to the bind mount made
filesystem-permission tests fail there. A later 16 GiB tmpfs run failed during
bootstrap with a missing-vendored-package diagnostic for
`golang.org/x/tools/internal/moreiters`, although its source file is tracked
and present. Its cause in the damaged container was not determined. The
diagnostic did not recur in the replacement container's full-suite run.

## Recovery procedure for a future filesystem failure

Recreate the disposable sandbox with the same image, bind mounts, bootstrap
toolchain, and Git/SSH setup. If Docker is the launcher, identify the
`sandbox-go:latest` container, inspect its mount/configuration settings,
stop it, and remove it **without** `-v`; then use the original launch command
or configuration. A restart of the same damaged root filesystem is not a
substitute for recreating it. The repository bind mount should persist, but
check it before removal.

If a fresh sandbox still reports `emergency_ro`, its root volume may persist
outside the container. Shut down the VM/container, boot a rescue Linux
environment or attach the volume to another Linux VM, identify the actual
ext4 device with `lsblk -f`, ensure it is unmounted, and run `e2fsck -f`
interactively on that device. Never run a repairing `e2fsck` on the mounted
root filesystem. If write I/O errors recur after repair, inspect the backing
storage rather than repeatedly remounting it.

After recreation, verify `/tmp` is writable, check the worktree with
`git status --short`, and run the build and full suite above. If the
`moreiters` diagnostic repeats, inspect the checkout and bootstrap inputs:

```bash
git ls-files src/cmd/vendor/golang.org/x/tools/internal/moreiters
ls src/cmd/vendor/golang.org/x/tools/internal/moreiters
"$GOROOT_BOOTSTRAP/bin/tgo" version
```

Record the new suite result in [PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md).
