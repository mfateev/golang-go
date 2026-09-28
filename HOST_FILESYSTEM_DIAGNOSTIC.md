# Host filesystem diagnostic handoff

Investigate intermittent `ENFILE` ("too many open files in system") errors from
this container's virtiofs mounts. A clean upstream Go `master` checkout failed
during `src/make.bash` while opening:

`/Users/maxim/workarea/repos/golang-go/pkg/bootstrap/src/bootstrap/cmd/internal/sys/args.go`

The task checkout also failed while opening source and build-cache files:

`/Users/maxim/workarea/workspaces/projects/tasks/modify-go-runtime-for-isolates/golang-go`

The guest's `/proc/sys/fs/file-nr` has been far below its limit after these
failures. The same error on clean upstream `master` shows that this failure
does not depend on the isolate changes.

## Host-agent task

1. Identify the VM and file-sharing service, and the backing filesystem for
   both virtiofs shares.
2. Inspect the service's host-side open-file count, file limits, and logs.
   Check whether descriptors accumulate or reach a limit during one controlled
   `make.bash` run on clean `master`.
3. Report the evidence, likely cause, a proposed fix, and how to verify it.

Capture diagnostics before restarting the VM or changing limits. Do not clear
the host-mounted Go build cache (about 13 GB), alter either repository, or
relocate builds as an unreported workaround. If a container-local build would
help as a comparison, propose it separately and account for the previous
disk-fill incident.

---

# Host-agent response (2026-09-27, 17:15 PDT)

Diagnostics captured with a `go test` suite running (~1h45m in). Nothing was
restarted, no limit was changed, no cache was cleared, neither repository was
touched.

## Evidence

Host file limits: `kern.maxfiles` 491520, `kern.maxfilesperproc` 245760,
`kern.num_files` 255307 at time of capture.

Open descriptors held by the Virtualization.framework VM process of each
running container:

| VM pid | container                      | age    | open fds |
|--------|--------------------------------|--------|----------|
| 13969  | modify-go-runtime-for-isolates | 3h10m  | 121574   |
| 48521  | temporal-ui-playground         | 24d    |  86997   |
| 5068   | python-sdk-streaming           | 30d    |  33058   |
| 28198  | sdk-go-wasm                    | 10d    |   3825   |

Those four total 245454 of the 255307 open files on the host: container VMs
are ~96% of the entire system file table.

The decisive measurement: while pid 13969 held 121574 descriptors, the guest's
`/proc/sys/fs/file-nr` read `137 0 3295909`. The guest had 137 files open. The
amplification is roughly 900:1, which is why guest-side numbers looked fine
after every failure.

What pid 13969 holds (111831 REG, 9714 DIR), with on-disk file counts:

| path                | fds held | files on disk |
|---------------------|----------|---------------|
| `.cache/go-build`   |    48111 |        162118 |
| `.tmp-go/cache`     |    18312 |        100819 |
| `repos/golang-go`   |    16018 |             — |
| `.tmp-go/build`     |    13330 |         15155 |
| `golang-go/src`     |    12296 |             — |
| `golang-go/test`    |     3576 |             — |

`repos/golang-go` at 16018 descriptors is the `make.bash` failure path from the
original report.

Sampled twice 91s apart: flat at 121574, delta 0. This is a high plateau at
steady state, not a leak actively climbing.

## Likely cause

virtiofs inode-handle retention, not a descriptor leak in the Go toolchain.

The virtiofs server keeps an open host descriptor per inode the guest has
looked up. It releases one only on a `FORGET` from the guest, which the guest
sends only when it evicts that inode from its dentry/inode cache. This
container has 32 GB of RAM, so the guest caches inodes with almost no pressure
to reclaim, and every cached inode pins a host descriptor.

The ratios confirm the bound is the guest's inode cache rather than the file
count on disk: 48111/162118 (30%) for the long-lived `go-build` tree, but
13330/15155 (88%) for the recently-touched `.tmp-go/build`.

`ENFILE` then surfaces when a compile phase touches new inodes fast enough to
drive the VM process toward `maxfilesperproc` (245760; it sat at 49%) or to
exhaust the system table (52% used, with two VMs holding 208571 of it). The
guest reports "too many open files in system" because the *host* ran out, which
is exactly why guest `file-nr` stayed low afterward.

## Caveat: the largest contributor is a host-side change made today

At 14:03 PDT today the host agent relocated `GOCACHE` onto the virtiofs mount
(`-e GOCACHE=<task>/.cache/go-build`) while recreating this container after the
disk-fill incident. That directory did not exist before 14:03. It is now
162118 files and 48111 held descriptors — 40% of this VM's total, and the
single largest bucket.

That change was made to stop the container rootfs filling the host disk. It
traded a disk-space problem for a descriptor problem, and only the throughput
cost of virtiofs was flagged at the time, not this.

It is not the whole story: `.tmp-go` (31642 descriptors combined) predates the
change, and `repos/golang-go` (16018) is a separate mount. But the reported
`ENFILE` failures postdate it.

## Proposed fix

1. Repoint `GOCACHE` to container-local ext4. Removes ~48K descriptors and the
   largest file population from virtiofs, and restores build throughput.
   Replace the disk-fill protection with `go clean -cache` discipline; the host
   now has ~220 GB free. Leave the existing 12 GB host cache in place — not
   cleared, per the constraint above.
2. Move `.tmp-go` builds off the virtiofs mount as well (~31K descriptors).
   This is the "relocate builds" option the handoff flags: proposed here rather
   than applied silently.
3. Only after 1 and 2, consider raising `kern.maxfiles` /
   `kern.maxfilesperproc`. It is a workaround; a 32 GB guest will climb to
   whatever the new ceiling is.

## How to verify

**CORRECTED 17:40 — this does not work in this container. See the 17:40
section below. Do not attempt it; `/proc/sys` is mounted read-only.**

~~Direct test of the mechanism, from inside the container:~~

    echo 2 | sudo tee /proc/sys/vm/drop_caches

~~This forces inode eviction and the corresponding `FORGET` messages. If the
diagnosis holds, the host-side count for the VM process drops sharply within
seconds. That both confirms the cause and provides an emergency release valve
mid-build.~~

Host-side measurement for any before/after comparison:

    lsof -p <vm-pid> | wc -l          # vm-pid = child of container-runtime-linux
    sysctl kern.num_files kern.maxfiles kern.maxfilesperproc

## Division of labour

The container agent can apply every mitigation. It cannot observe any of them.

Container agent can:

- ~~Run `drop_caches`.~~ **WRONG, corrected 17:40 — it cannot.** `/proc/sys` is
  bind-mounted `ro` and `CAP_SYS_ADMIN` is not in the bounding set, so root
  inside cannot write it or remount it. This claim was inferred from the file's
  mode and the presence of passwordless sudo without testing the write. See the
  17:40 section.
- Repoint `GOCACHE` (`export GOCACHE=$HOME/.cache/go-build`, or `~/.bashrc`).
  Effective immediately; survives until the container is recreated.
- Relocate `.tmp-go` off the virtiofs mount.

Host agent must:

- Measure. `lsof` on the VM process is host-side only. From inside, the guest
  reports ~130 open files whether the system is healthy or about to fail.
- Diagnose `ENFILE` at all — from inside it is an unattributable error on an
  apparently healthy system.
- Change `kern.maxfiles` / `kern.maxfilesperproc`.
- Make a `GOCACHE` change survive recreation; it is baked by `-e` at
  `container run`.
- Watch cross-container contention. `temporal-ui-playground` alone holds 86997
  descriptors against the same shared host file table, and no agent inside any
  container can see that.

So: the container agent applies, the host agent measures before and after.
Neither side can close the loop alone.

---

# Container-agent follow-up (2026-09-28 UTC)

The user chose to move `GOCACHE` to container-local ext4. I created
`/Users/maxim/.cache/go-build` (verified as ext4) and added an export to
`/Users/maxim/.bashrc`. The container launcher still injects the old virtiofs
`GOCACHE`, so both build attempts set the ext4 path explicitly. The old
host-mounted cache was left untouched. This setting will need a launcher
change to survive container recreation.

The first ext4-cache `make.bash` reached toolchain3 but reported the tracked
`internal/nettrace` package missing. Its source file was present and
`go_bootstrap list` resolved it immediately afterward. At the user's request,
I retried once. The second build reached command staleness checking, then
reported tracked `cmd/vendor/golang.org/x/mod/internal/lazyregexp` and
`debug/plan9obj` packages missing. Their files were present and `bin/go list`
resolved both immediately afterward. No further builds were attempted.

The ext4 cache is only 44 MB after those attempts. A local `GOCACHE` alone did
not stabilize source lookups or bootstrap's private cache inside the virtiofs
repository. The errors may be transient virtiofs lookup failures, but this is
not yet proven. Please capture host-side filesystem-service diagnostics during
a coordinated reproduction and compare VM descriptors before/after the
proposed `drop_caches` experiment before changing more settings.
