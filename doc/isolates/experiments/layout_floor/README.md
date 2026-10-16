# Prepared-instance package-state floor

This experiment compares the heap retained by 10,000 `isolate.New` instances
of marked workflow functions. All six functions are linked into one executable;
the host chooses one function and creates 10,000 instances of it. The instances
are kept alive but never started. `New` allocates selected package layouts and
replays their initializers, so the measurement includes that eager state, the
isolate runtime structures, and the package table. It excludes a running stack
and later JSON cache population.

From this directory, using this fork's built toolchain:

```bash
layout_run_dir=$(mktemp -d)
../../../../bin/tgo build -o "$layout_run_dir/floor" ./host
for variant in empty withtime withreflect withjsontext withv2 withjson; do
    "$layout_run_dir/floor" "$variant"
done
../../../../bin/tgo build -o "$layout_run_dir/inspect" ./inspect
"$layout_run_dir/inspect"
../../../../bin/tgo build -o "$layout_run_dir/inspecttime" ./inspecttime
"$layout_run_dir/inspecttime"
```

The host forces a GC before and after creating the instances, keeps every
instance reachable, and reports the `HeapAlloc`, `HeapObjects`, and
`TotalAlloc` differences. Each variant runs in a fresh process. Two Linux arm64
runs on 2026-10-16 agreed within 2 retained bytes per instance. These current
measurements supersede the earlier figures; they include the present
per-instance runtime and control structures as well as selected package state.

| Program import | Retained bytes/instance | Retained objects/instance | Allocated bytes/instance |
|---|---:|---:|---:|
| None | 13,440.6 | 34.5 | 15,017.9 |
| `time` | 14,873.3 | 46.6 | 17,378.7 |
| `reflect` | 13,457.6 | 35.6 | 15,055.4 |
| `encoding/json/jsontext` | 17,585.3 | 89.6 | 23,366.2 |
| `encoding/json/v2` | 23,345.0 | 149.6 | 31,650.9 |
| `encoding/json` | 23,671.9 | 153.5 | 32,310.2 |

The `time` root adds about 1.43 KB retained per instance. The JSON program
retains about 10.23 KB more per instance than the empty function. The
inspectors read the current compiler descriptor and runtime type ABI; they
report fixed layout sizes of 1,448 bytes for `reflect`, 40 for `encoding/json`,
96 for `encoding/json/internal`, 288 for
`encoding/json/internal/jsonopts`, 856 for `encoding/json/jsontext`, and 800
for `encoding/json/v2`, and 584 for `time` (4,112 bytes combined for the JSON
program). The remaining difference includes per-instance runtime structures,
package tables, other metadata, and objects created by initializers. These
import-graph comparisons do not attribute every byte to one package.

This is a heap floor for the current eager POC, not an RSS or live-workflow
measurement. It makes reducing per-instance runtime structures and sharing
immutable package state concrete density improvements to investigate before
claiming the 10,000-instance goal.
