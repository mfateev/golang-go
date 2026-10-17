# Go isolates design work

This directory contains the design and feasibility record for the experimental
isolates branch. The Go fork accepts an [explicit build manifest](BUILD_CONTRACT.md)
and supplies private package state, ownership checks, deterministic scheduling,
generic time/timers, copied-byte transport and lifecycle enforcement.
The SDK owns workflow discovery, typed entry generation, package selection and
toolchain distribution. The old cooperative reference model and phase probes
have been retired; current package state lives in `internal/isolatestate`.

See [migration evidence](MINIMAL_GO_CORE_STATUS.md) for completed changes,
verification and remaining protobuf/OTel integration work. Earlier phase logs
preserve findings and rejected approaches; their retired commands are historical.

- [Resource controls](./RESOURCE_CONTROLS_PLAN.md) — accounting, limits and worker watchdogs
- [Minimal Go core plan](./MINIMAL_GO_CORE_PLAN.md) — move SDK policy and toolchain ownership out of Go and retire superseded experiments
- [Reliable lifecycle](./LIFECYCLE_PLAN.md) — whole-group termination and SDK cleanup
- [Task status](./TASK_STATUS.md) — current progress and open work
- [Implementation plan](./IMPLEMENTATION_PLAN.md) — phase gates and acceptance
- [Design definition](./ISOLATES_DESIGN.md) — goals and decisions
- [Static programs](./STATIC_PROGRAMS.md) — generic entries and one-binary linking
- [Dynamic loading](./DYNAMIC_LOADING.md) — deferred plugin-based enhancement
- [Isolate API](./ISOLATE_API.md) — proposed host and isolate surfaces
- [Host integration](./HOST_INTEGRATION.md) — typed Go host functions, isolate-owned construction, callback reentry, and explicit parking; existing Call remains unchanged
- [Language and library subset](./ISOLATE_SUBSET.md) — proposed restrictions
- [Determinism](./DETERMINISM.md) — time, maps, scheduling, and replay
- [Alternatives](./ALTERNATIVES.md) — fork and external instrumentation analysis
- [Phase 0 results](./PHASE0_RESULTS.md) — experiments and measurements
- [Phase 2B progress](./PHASE2B_PROGRESS.md) — compiler/runtime slices and remaining invariants
- [Development and recovery](./DEVELOPMENT.md) — checkout, build, and damaged-container recovery
- [Temporal POC](./TEMPORAL_POC.md) — trusted SDK integration and steel-thread gates

Run shell commands in these documents from the repository root unless a
different working directory is stated.
## Cached isolate memory

See [memory density](MEMORY_DENSITY.md) for measured startup and retained-memory
costs, read-only library sharing, and future span/ownership/GC designs.
