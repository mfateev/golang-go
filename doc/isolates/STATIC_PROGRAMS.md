# Statically linked isolate programs

The Go fork accepts explicit entries and package-state selection through a
[versioned build manifest](BUILD_CONTRACT.md). `GOEXPERIMENT=isolates` enables the
coordinated compiler/runtime feature; its default is disabled. The SDK-owned
`tgo` driver discovers workflow declarations and generates typed adapters.
Go itself does not discover workflow comments.

A single executable links the original host and its selected entries. Generated
process registration references compiler-owned descriptors and creates private
state factories; external sources cannot import the privileged registration
packages or supply arbitrary descriptor pointers. The public host uses
`isolate.LookupFunction` and `Handle.ProgramWithHandle` to select a dispatcher.
Each `isolate.New` receives independent package state and initialization.

Typed adapters execute inside the instance. Their decode/encode callbacks use
`isolate.Value` argument/result slots and keep Go values behind the copied-byte
boundary. Original functions retain their signatures. The SDK owns converter
policy, workflow protocols and integration dependency versions.

Package selection participates in both compiler invocation and cache identity.
Effective flags include only the package's transitive imports, preserving reuse
across unrelated programs. Adapter contents are hashed separately from the
manifest pathname. Test-only declarations retain distinct source identities for
internal/external test packages and ordinary dependencies.

`internal/isolatestate` validates descriptor versions, keys and dependencies,
orders initialization, and selects runtime package layouts. The runtime keeps a
concrete package-state table on each goroutine, inherits it on child creation,
and resets it on reuse. Read-only services get fresh selected scratch layouts.
Shared standard-library backing data retains independent audited freezing rules.

The generic compiler isolate pass runs before inlining and again for freshly
read generic/imported bodies. Global validation runs after inlining, before
initialization rewriting and layout emission. Escape analysis and SSA follow;
SSA address lowering stays in `ssagen`.

The old cooperative scheduler, single-base probe, toy global/epoch lowering and
phase-specific kill hooks are retired. Native conformance covers initialization,
cache reuse, imports, generics, ownership, service admission and revocation.
Protobuf/OTel metadata adaptation remains a tracked migration item; its existing
source checks stay in place until their privilege mechanisms are replaced.

Dynamic loading remains a [future enhancement](DYNAMIC_LOADING.md).
