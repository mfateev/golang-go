# Development and environment recovery

This file is for an agent starting from this Git repository alone. Read
[TASK_STATUS.md](./TASK_STATUS.md) and
[PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md) before extending the implementation.

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
The full `src/all.bash` run after the first-dispatch revocation change remains
unverified. The user explicitly requested the subsequent documentation push
without running tests, so the commit that adds this guide is also untested.

## Why the previous container needs replacement

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
and present. Its cause is unresolved. Do a clean run in a replacement
container before investigating that diagnostic as a source issue.

## Recover from outside the container

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
"$GOROOT_BOOTSTRAP/bin/go" version
```

Record the new suite result in [PHASE2B_PROGRESS.md](./PHASE2B_PROGRESS.md).
