# Prepared-instance package-state floor

This experiment compares the heap retained by 10,000 `isolate.New` instances
of tiny statically linked programs. The instances are kept alive but never
started. `New` allocates selected package layouts and replays their
initializers, so the measurement includes that eager state, the provisional
host boundary, and the package table. It excludes a running stack and later
JSON cache population.

From this directory, using this fork's built toolchain:

```bash
layout_run_dir=$(mktemp -d)
for variant in empty withtime withreflect withjsontext withv2 withjson; do
    ../../../../bin/go build -isolate-dir="./$variant" -o "$layout_run_dir/$variant" ./host
    "$layout_run_dir/$variant"
done
../../../../bin/go build -isolate-dir=./withjson -o "$layout_run_dir/inspect" ./inspect
"$layout_run_dir/inspect"
../../../../bin/go build -isolate-dir=./withtime -o "$layout_run_dir/inspecttime" ./inspecttime
"$layout_run_dir/inspecttime"
```

The host forces a GC before and after creating the instances, keeps every
instance reachable, and reports the `HeapAlloc`, `HeapObjects`, and
`TotalAlloc` differences. Each variant runs in a fresh process. On Linux
arm64, after the host boundary moved to a `Call`-only API, two runs on
2026-10-01 agreed to about 1 byte per instance:

| Program import | Retained bytes/instance | Retained objects/instance | Allocated bytes/instance |
|---|---:|---:|---:|
| None | 434 | 10 | 506 |
| `time` | 1,850 | 22 | 2,026 |
| `reflect` | 2,450 | 13 | 2,626 |
| `encoding/json/jsontext` | 3,146 | 44 | 3,546 |
| `encoding/json/v2` | 10,410 | 99 | 12,186 |
| `encoding/json` | 10,690 | 103 | 12,626 |

Removing the unused Inbox channel and its initial message reduced every
variant by about 144 retained bytes and two objects per instance. The JSON
program retains about 10.26 KB more per prepared instance than the
empty program. The inspectors read the current compiler descriptor and
runtime type ABI; they report fixed layout sizes of 1,448 bytes for `reflect`,
40 for `encoding/json`, 96 for `encoding/json/internal`, 288 for
`encoding/json/internal/jsonopts`, 856 for `encoding/json/jsontext`, and 800
for `encoding/json/v2`, and 584 for `time` (4,112 bytes combined for the JSON
program). The rest of the retained
difference includes package tables, other metadata, and objects created by
initializers. These import-graph comparisons do not attribute every byte to
one package. The JSON v2 and JSON variants also reach `time`, so selecting
`time` raises their floors by about 1.42 KB per instance.

Attaching a runtime goroutine group to each boundary added about 24 retained
bytes and one object per prepared instance. The table includes that group,
plus about 16 bytes for the provisional main failure result field. It
excludes stacks for goroutines created after `Start`.

This is a heap floor for the current eager POC, not an RSS or live-workflow
measurement. It makes lazy or shared immutable package state a concrete
density improvement to investigate before claiming the 10,000-instance goal.
