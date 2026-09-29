# Independently built isolate programs and runtime loading

Status: **feasibility investigation, not an adopted MVP design**.

## Finding

Go's `-buildmode=plugin` is a plausible loader foundation. It builds one
`package main` and its imports as a shared object, and `plugin.Open` loads it
into the host process. The loaded code uses the host Go runtime: runtime
`moduledata` is added to the process's module list, then the runtime registers
GC masks, types, and interface tables. A worker could load one artifact per
isolate *program* and create many isolate *instances* from it.

This does not make an ordinary Go plugin an isolate. `plugin.Open` runs new
packages' `init` functions once, does not run `main`, and cannot unload the
plugin. Plugin globals are ordinary process globals until the isolate compiler
rewrites their accesses and the runtime selects an instance layout. The fork's
current opt-in layout and package descriptor probes are a start; they have not
been tested with `-buildmode=plugin`.

Sources: [Go's plugin package](https://pkg.go.dev/plugin),
[`plugin.Open` implementation](../../src/plugin/plugin_dlopen.go),
[`runtime.plugin_lastmoduleinit`](../../src/runtime/plugin.go), and
[`runtime.modulesinit`](../../src/runtime/symtab.go).

## What "independent" can mean

| Property | Feasibility |
|---|---|
| Build and deploy each program as its own artifact | Yes, with Go plugins on their supported platforms. |
| Load a program after the worker starts | Yes. `plugin.Open` loads code and metadata once per artifact. |
| Run many instances of one loaded program | Requires the isolate state, goroutine, and heap work already planned. Loading once can amortize code and metadata. |
| Let each program choose arbitrary versions of shared Go packages | No with the current plugin ABI. The runtime compares package fingerprints and rejects a plugin whose shared package differs from the loaded version. |
| Unload an old program after its last instance exits | No with the current plugin implementation. Reclaiming code, types, and runtime metadata would need a separate design and proof. |

The host and every plugin must use the same forked toolchain, compatible build
flags and tags, and identical source for packages they share. The current
`-d=isolatepackages` selection changes generated package code. Independently
built artifacts therefore need one pinned package-classification policy and
compatible build inputs for every common import. The loader's package hash
check is useful, but it is not a substitute for an isolate coverage proof.

Two programs may share immutable code and runtime metadata, while every
instance gets its own mutable selected-package state. A common package path
must have one compatible compiled definition across the worker and loaded
plugins. Programs needing genuinely different implementations of a dependency
would need distinct import paths and type identities, or separate processes.

Sources: [plugin compatibility warning](https://pkg.go.dev/plugin#hdr-Warnings),
[`runtime.plugin_lastmoduleinit`](../../src/runtime/plugin.go),
[`cmd/link` package hash emission](../../src/cmd/link/internal/ld/symtab.go), and
the [current package selection probe](../../src/cmd/compile/internal/base/isolate.go).

## Proposed program contract

An isolate directory could contain `package main` and `isolate.json` with a
stable logical name. The build would produce a plugin with a distinct immutable
artifact identity, plus a manifest of its package closure and state
descriptors. The logical name selects the workflow program; the artifact
identity identifies its exact code and must differ for concurrently loaded
versions. The config file alone does not make a package safe to load.

The build would generate an exported entry wrapper in the plugin that calls
the program's `main`. After loading, the host would look up that wrapper,
allocate one instance's package layouts, replay that program's selected
initializers under the instance, and start its `main` in an isolate goroutine.
Initial input and completion would use a defined boundary protocol because
ordinary `main()` has neither parameters nor a return value.

Loading must not execute isolate-owned initialization in process state. The
plugin loader currently calls `doInit` for every newly loaded package, so it
would need to run process-owned initialization once while deferring selected
package initialization to instance creation. The build must reject any
reachable package whose ownership or effects are unclassified. Before
`dlopen`, the host should validate the artifact's toolchain, package, and
isolate manifest; the current plugin's package hash check happens after the
dynamic loader has mapped the shared object.

## Costs and limits

- Plugins currently support Linux, FreeBSD, and macOS, require cgo for the
  loader, and have documented race-detector limitations. These constrain the
  platforms and verification available to a plugin-based MVP.
- Loading a second build with the same plugin path is rejected. A stable
  logical program name therefore cannot also be the plugin's unique identity.
- Plugins cannot be closed. Many instances of one program are compatible with
  that lifecycle; many successive program versions accumulate code and
  metadata until the worker process exits.
- The dynamic loader, package hash checks, Go type registration, and GC module
  registration are useful existing machinery. A custom loader would have to
  reproduce and extend them, so extending the plugin path is the smaller
  first experiment.
- `-buildmode=c-shared` exposes a C ABI for a foreign host. It is not the
  direct Go symbol and module path provided by `-buildmode=plugin`.

## Memory measurements on Linux arm64

Measured 2026-09-29 with this fork's Go toolchain, cgo enabled, and
`GOMAXPROCS=1`. A host using `plugin` loaded separately built `package main`
plugins. Each sample forced GC and called `debug.FreeOSMemory`; process RSS
came from `/proc/self/smaps_rollup`, and plugin mapping RSS from
`/proc/self/smaps`. A single call to each plugin's exported `Entry` touched
some code. The host started at 4.6 MiB RSS, about 1.8 MiB `MemStats.GCSys`,
and 63 KiB live Go heap. Those are overlapping process accounting views:
`GCSys` is not an additional amount to add to RSS.

| Plugin workload | `.so` disk size | Plugin mapping RSS after one call | Incremental process RSS |
|---|---:|---:|---:|
| One integer global and entry function | 2.13 MiB | 0.70 MiB | 0.74–1.04 MiB |
| `encoding/json` marshals one small map | 5.63 MiB | 2.35 MiB | 2.57–2.93 MiB |
| `net/http` reads one header | 9.99 MiB | 4.70 MiB | 5.23–5.54 MiB |

The ranges reflect loading order and the first plugin's loader warmup. Loading
a *second*, separately built JSON plugin after the first increased process RSS
another 2.31 MiB, even though both used the same `encoding/json` build. Exact
package compatibility therefore does not imply that independently linked
plugin files share all resident code and metadata pages. Calling more library
paths can fault in more pages; these are light-use observations, not maxima.

`MemStats.GCSys` grew by roughly 7–56 KiB for a minimal plugin, 17–134 KiB
for JSON, and 257–364 KiB for HTTP across the loading orders. Retained Go
heap grew by roughly 27–40 KiB, 92–108 KiB, and 153–184 KiB respectively.
These deltas combine loader metadata, package initialization, and allocator
rounding; they do not isolate the cost of `init` alone. They do show that the
observed per-plugin RSS was dominated by mapped code and metadata rather than
another multi-megabyte GC runtime.

Per-instance package state is separate. The tagged
[`encoding/base64` benchmark](../../src/internal/isolateproto/testdata/e4base64toy/state_test.go)
created 10,000 initialized package instances in three fresh processes. It
retained 1,521–1,522 B of Go heap and 1,629–1,633 B of heap spans per
instance. It created no isolate goroutines or user state. The existing
Phase 1 [mixed suspended-instance proxy](./PHASE0_RESULTS.md) measured
7,567–7,594 B incremental RSS per instance in three fresh runs on this tree;
about 4.9 KiB was stack spans and 2.3 KiB was retained heap. The two probes
have not been combined into a real dynamic isolate and must not be treated as
an accepted total memory budget.

## Process-per-instance comparison

A separate 2026-09-29 probe built three pure-Go executables with this fork.
Each did one tiny operation, reported `runtime.MemStats`, signaled ready,
and slept. A parent started one, then 30 identical children with
`GOMAXPROCS=1`, and read their `/proc/PID/smaps_rollup` records. Three
fresh repetitions gave stable memory values:

| Executable | One process RSS | At 30: PSS per process | At 30: private memory per process | Warm start to ready, median |
|---|---:|---:|---:|---:|
| Minimal | 1.85 MiB | 0.69 MiB | 0.65 MiB | 0.87–1.05 ms |
| `encoding/json` marshal | 2.87 MiB | 0.84 MiB | 0.77 MiB | 0.96–1.27 ms |
| `net/http` header read | 3.61 MiB | 1.07 MiB | 0.98 MiB | 1.24–1.58 ms |

PSS divides shared executable pages among their users; summing RSS counts
those pages 30 times. Private memory here is `Private_Clean +
Private_Dirty`; neither measure includes per-process kernel structures.
These results assume many instances of the **same** executable, for which
code sharing is best. Different binaries and warm-up behavior can change the
numbers.

At readiness, each process reported 1.43–1.59 MiB of `MemStats.GCSys`,
192–224 KiB of `StackSys`, and only 39–80 KiB of `HeapAlloc`.
`GCSys` is Go's allocation accounting for GC metadata, not an additional
resident-memory charge to add to PSS. Its size exceeding private resident
memory illustrates why these fields cannot be summed into an RSS estimate.
`GODEBUG=inittrace=1` reported 11, 18, and 74 package init tasks and
0.027, 0.067, and 0.218 ms of summed task clock time, respectively. This
trace excludes much of OS launch and runtime startup, and instrumentation
changes timing; the ready signal is the practical end-to-end launch measure.

In a separate three-run sample, `plugin.Open` took 0.42–0.47 ms for the
minimal plugin, 0.81–0.95 ms for JSON, and 1.63–1.97 ms for HTTP. This is
paid once per loaded *program*, not per *instance*. The existing Phase 1
single-Inbox proxy's 10,000-instance create/park/cleanup batch took
51–52 ms, or about 5.1–5.2 µs per instance with its benchmark overhead.
Neither prototype timing includes all planned production isolate work.

Linear extrapolation of the 30-process private-memory observations to 10,000
processes would be roughly 6–10 GiB **before** kernel structures and real
workflow state. The 10,000-instance single-Inbox proxy retained about 65 MB
incremental process RSS, and the mixed proxy about 76 MB. This is an
order-of-magnitude comparison, not an acceptance result: the real isolate
runtime, per-instance package initialization, and workload heaps are still
unmeasured together.

## Proof before changing the MVP architecture

1. Build a worker and two independently linked plugin programs with the same
   pinned fork, classification policy, and shared dependency versions. Load
   both after worker startup and call their generated entry wrappers.
2. Instantiate one program twice. Verify independent initialized globals and
   dependency state, including state used by a native child goroutine.
3. Demonstrate that loading does not run isolate-owned initializers in process
   state, and that a package missing a selected layout is rejected before an
   instance starts.
4. Deliberately change a common package's build inputs and verify a clear
   compatibility rejection. Try two versions of one logical program with
   distinct artifact identities.
5. Measure loaded code and metadata per program, and state per instance.
   Record the effect of retaining old versions until worker restart.

The static linked-program design remains the current MVP decision. This
investigation supports a plugin-based prototype, not an isolation or unload
claim.
