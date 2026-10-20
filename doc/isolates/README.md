# Native Go isolate POC

This directory specifies the current compiler/runtime POC and its integration
with the native Temporal SDK. Start with [the architecture](ISOLATES_DESIGN.md),
then [the implementation guide](IMPLEMENTATION.md) to reproduce the design from
an ordinary Go source tree. [Development](DEVELOPMENT.md) describes building the
existing sources from clean checkouts. All commands name their working directory.

The POC runs statically linked Go functions with private package state, checked
memory ownership, deterministic native concurrency, host-driven time, copied-byte
communication and whole-instance lifecycle control. It uses the shared Go GC and
ordinary Go pointers. It is intended for reviewed workflow code; it does not
provide a process security sandbox or a hard deadline for stopping arbitrary code.

| Document | Contract |
| --- | --- |
| [Architecture](ISOLATES_DESIGN.md) | Components, ownership domains, data flow and implementation limits |
| [Implementation guide](IMPLEMENTATION.md) | Data structures, algorithms, compiler/runtime integration and construction order |
| [Build contract](BUILD_CONTRACT.md) | Generic Go manifest, typed invokers, package selection, tests and cache identity |
| [Package state](PACKAGE_STATE.md) | Global addressing, descriptors, initialization, shared tables and scratch state |
| [Memory ownership](MEMORY_OWNERSHIP.md) | Allocator, access/publication checks, metadata and GC lifetimes |
| [Determinism](DETERMINISM.md) | Dispatch, select, maps, clocks, timers, randomness and replay |
| [Language and effects](ISOLATE_SUBSET.md) | Supported Go operations and restrictions |
| [Host integration](HOST_INTEGRATION.md) | Copied-byte operations and the binary SDK protocol |
| [Lifecycle](LIFECYCLE.md) | Initialization, completion, revocation, pending cleanup and diagnostics |
| [Read-only handlers](READ_ONLY_HANDLERS.md) | Query/validator admission, scratch allocation and captured values |
| [Resource controls](RESOURCE_CONTROLS.md) | Charged memory, goroutines, task/progress watchdogs and observers |
| [Runtime API](RUNTIME_API.md) | Complete exported `isolate` surface and build-report fields |
| [Temporal integration](TEMPORAL_INTEGRATION.md) | SDK registration, task phases, serialization, handlers and host bindings |
| [Memory density](MEMORY_DENSITY.md) | Current cost model, measurement fixture and capacity limits |
| [Development](DEVELOPMENT.md) | Repositories, source pins, bootstrap and local configuration |
| [Validation](VALIDATION.md) | Reproducible compiler, runtime, race, SDK, sample and replay checks |

The SDK's [README](https://github.com/mfateev/sdk-go-poc/blob/task/modify-go-runtime-for-isolates/README.md),
[reference](https://github.com/mfateev/sdk-go-poc/blob/task/modify-go-runtime-for-isolates/SDK_REFERENCE.md)
and [implementation guide](https://github.com/mfateev/sdk-go-poc/blob/task/modify-go-runtime-for-isolates/IMPLEMENTATION.md)
complete the application and Temporal wire contracts. The SDK owns dependency
selection and distribution; the Go core contains no Temporal or protobuf policy.
