# Go isolates design work

This directory contains the design and feasibility record for the experimental
isolates branch. The compiler discovers `//go:isolate` functions and statically
links their programs into one process. The runtime supplies private package
state, memory ownership checks, deterministic scheduling and time, copied-byte
host calls, effect restrictions, and whole-group lifecycle management. The
Temporal SDK POC uses these mechanisms for native Go workflows.

Productization is ongoing; see the acceptance status and remaining features in
[the productization plan](./PRODUCTIZATION_PLAN.md). `src/internal/isolateproto`
is an earlier reference model, not the implementation used by the SDK.

- [Reliable lifecycle](./LIFECYCLE_PLAN.md) — whole-group termination and SDK cleanup
- [Task status](./TASK_STATUS.md) — current progress and open work
- [Implementation plan](./IMPLEMENTATION_PLAN.md) — phase gates and acceptance
- [Design definition](./ISOLATES_DESIGN.md) — goals and decisions
- [Static programs](./STATIC_PROGRAMS.md) — per-directory config and one-binary build contract
- [Dynamic loading](./DYNAMIC_LOADING.md) — deferred plugin-based enhancement
- [Isolate API](./ISOLATE_API.md) — proposed host and isolate surfaces
- [Language and library subset](./ISOLATE_SUBSET.md) — proposed restrictions
- [Determinism](./DETERMINISM.md) — time, maps, scheduling, and replay
- [Alternatives](./ALTERNATIVES.md) — fork and external instrumentation analysis
- [Phase 0 results](./PHASE0_RESULTS.md) — experiments and measurements
- [Phase 2B progress](./PHASE2B_PROGRESS.md) — compiler/runtime slices and remaining invariants
- [Development and recovery](./DEVELOPMENT.md) — checkout, build, and damaged-container recovery
- [Temporal POC](./TEMPORAL_POC.md) — trusted SDK integration and steel-thread gates

Run shell commands in these documents from the repository root unless a
different working directory is stated.
