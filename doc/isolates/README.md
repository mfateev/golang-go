# Go isolates design work

This directory contains the design and feasibility record for the experimental
isolates branch. The current implementation is a Phase 1 reference model in
`src/internal/isolateproto` plus tagged runtime experiments. The ordinary-Go
runtime implementation is not complete.

- [Task status](./TASK_STATUS.md) — current progress and open work
- [Implementation plan](./IMPLEMENTATION_PLAN.md) — phase gates and acceptance
- [Design definition](./ISOLATES_DESIGN.md) — goals and decisions
- [Isolate API](./ISOLATE_API.md) — proposed host and isolate surfaces
- [Language and library subset](./ISOLATE_SUBSET.md) — proposed restrictions
- [Determinism](./DETERMINISM.md) — time, maps, scheduling, and replay
- [Alternatives](./ALTERNATIVES.md) — fork and external instrumentation analysis
- [Phase 0 results](./PHASE0_RESULTS.md) — experiments and measurements
- [Phase 2B progress](./PHASE2B_PROGRESS.md) — compiler/runtime slices and remaining invariants

Run shell commands in these documents from the repository root unless a
different working directory is stated.
