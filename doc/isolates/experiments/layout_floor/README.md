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
for variant in empty withreflect withjsontext withv2 withjson; do
    ../../../../bin/go build -isolate-dir="./$variant" -o "$layout_run_dir/$variant" ./host
    "$layout_run_dir/$variant"
done
../../../../bin/go build -isolate-dir=./withjson -o "$layout_run_dir/inspect" ./inspect
"$layout_run_dir/inspect"
```

The host forces a GC before and after creating the instances, keeps every
instance reachable, and reports the `HeapAlloc`, `HeapObjects`, and
`TotalAlloc` differences. Each variant runs in a fresh process. On Linux
arm64 with the default JSON v2 experiment, two runs on 2026-10-01 agreed to
within 1 byte per instance:

| Program import | Retained bytes/instance | Retained objects/instance | Allocated bytes/instance |
|---|---:|---:|---:|
| None | 538 | 11 | 610 |
| `reflect` | 2,554 | 14 | 2,730 |
| `encoding/json/jsontext` | 3,250 | 45 | 3,650 |
| `encoding/json/v2` | 9,090 | 89 | 9,954 |
| `encoding/json` | 9,402 | 93 | 11,274 |

The JSON program retains about 8.86 KB more per prepared instance than the
empty program. The inspector reads the current compiler descriptor and
runtime type ABI; it reports fixed layout sizes of 1,448 bytes for `reflect`,
40 for `encoding/json`, 96 for `encoding/json/internal`, 288 for
`encoding/json/internal/jsonopts`, 856 for `encoding/json/jsontext`, and 800
for `encoding/json/v2` (3,528 bytes combined). The rest of the retained
difference includes package tables, other metadata, and objects created by
initializers. These import-graph comparisons do not attribute every byte to
one package.

This is a heap floor for the current eager POC, not an RSS or live-workflow
measurement. It makes lazy or shared immutable package state a concrete
density improvement to investigate before claiming the 10,000-instance goal.
