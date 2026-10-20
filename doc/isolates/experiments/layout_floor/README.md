# Package-state footprint fixture

This fixture isolates the cost of prepared package layouts for six import graphs:
empty, time, reflection and the three JSON entry packages. It retains 10,000
instances without starting their entries, forces GC and reports retained/allocated
heap per instance. This excludes execution stacks, subsequent cache population,
RSS and complete Temporal worker history/cache overhead.

With the SDK-built `tgo` on PATH, run from this directory:

```sh
layout_run_dir=$(mktemp -d)
tgo build -o "$layout_run_dir/floor" ./host
for variant in empty withtime withreflect withjsontext withv2 withjson; do
    "$layout_run_dir/floor" "$variant"
done
tgo build -o "$layout_run_dir/inspect" ./inspect
"$layout_run_dir/inspect"
tgo build -o "$layout_run_dir/inspecttime" ./inspecttime
"$layout_run_dir/inspecttime"
```

The inspectors read the current private compiler descriptor/type ABI. Update them
with descriptor changes; they are diagnostics rather than public APIs. Each
variant is a fresh process. Report source pins, toolchain/architecture and the
actual output when evaluating layout changes. For private protobuf and started
workflow measurements use [the SDK fixture](../../MEMORY_DENSITY.md).
