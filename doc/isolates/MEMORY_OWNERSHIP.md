# Memory ownership

## Allocation and GC

Every instance receives a nonzero numeric owner ID, a revocation group and an
owner-specific `mcache`. Host allocation uses owner zero and ordinary per-P
caches. Small and large application spans record one owner; a goroutine moving
between processors continues using its group's cache. Tiny allocations must not
pack objects from different owners into one block. Read-only services use a
second scratch cache with a distinct owner and the same resource account.

Cache access is locked even for deterministic groups: concurrent mode and GC
still require synchronization. Never hold the cache lock across GC assists,
stack growth or a call that can park. The global cache registry holds non-GC
metadata, is visited while the world is stopped, and participates in sweep
generation changes, cache flushing and accounting.

A GC-visible acyclic lifetime handle owns each non-GC cache. Live group members
keep it reachable. A finalizer retires an unreachable cache without keeping the
instance alive or freeing objects still reachable by Go's collector. Numeric
owner/resource metadata must not contain private pointers. Span/cache/account
references retire independently. Suspended state stays rooted and traced; there
is no isolate-specific collector or bulk heap release.

## Access matrix

| Execution | Private workflow object | Scratch object | Host mutable object | Approved immutable storage |
| --- | --- | --- | --- | --- |
| Workflow | Read/write, same owner | Denied | Denied | Read |
| Read-only service | Read | Read/write, same scratch owner | Denied | Read |
| Instrumented host | Denied for foreign private ownership | Denied | Ordinary host access | Read |

The current goroutine's stack is valid storage. Another goroutine's stack,
runtime-managed memory and unclassified addresses are not private application
storage. Zero-sized and nil operations preserve Go nil/bounds behavior.
Owner zero is not permission to read arbitrary host data.

Validate complete ranges, overflow and every crossed span. Shared reads require
exact linker read-only bounds, canonical immutable metadata ranges, or audited
frozen-table extents. Mutable global data and BSS are denied to private code.
Keep range classification distinct from reference publication: a permitted
scalar read must not authorize storing a foreign pointer elsewhere.

## Compiler and runtime checks

All manifest builds force level-two heap checks across the linked graph, even
for host-initialized dependencies or an empty private package selection. Caller
`-gcflags` cannot disable this. Ordinary builds keep the regular Go path.

Before an operation, validate:

- Typed loads/stores, pointer-bearing assignment, aggregate moves and interfaces.
- String/slice headers and backing storage, concatenation, append/copy/clear,
  closures and boxed values.
- Map headers, keys and retained references, native/reflected lookup/assignment,
  iteration, delete/clear/clone and `len`.
- Channels, send/receive/select operands, element publication and close. Check
  every select operand before locking or queueing, even an unchosen case.
- Reflection calls, collection operations, actual dynamic call targets and
  defer/go targets before registering them.
- Direct, reflected and indirect atomics. `atomic.Value` has explicit checks
  before its pinned first-store sentinel sequence.

Reference publication recursively checks pointer-containing type fields so the
destination owner can retain every referenced object. Scratch can retain
read-only workflow references; workflows cannot retain scratch objects. Reads
through interfaces/strings cannot hide foreign backing storage.

Probes can allocate and move stacks. Copy ABI results out before probing; check
publication before destination liveness makes uninitialized pointers visible;
derive interface method addresses from a checked itab SSA value. Preserve nil
panic ordering and leave ordinary uninstrumented devirtualization intact.

## Standard-library services

`sync.Pool` is empty inside an isolate: `Put` drops values and `Get` calls `New`
or returns nil. It must never publish private values into process per-P lists.

Reflection type constructors have narrowly audited canonical metadata scopes.
They may borrow caller values for reads, publish only validated immutable type
metadata and return owned strings/copies where necessary. Application callbacks
cannot inherit that privilege. Provenance is attached to declarations and
survives inlining, export/import and generic instantiation; a matching package
name is insufficient. `reflect` assembly invocation checks the actual target.

Bounded CPU-feature cells and SHA-256 backend/table reads have precise approved
extents. Other mutable process caches are not implicitly shared. The authoritative
GOROOT policy is `src/cmd/internal/isolatepolicy/metadata.go` together with
`src/internal/isolatepolicy`. The core grants no protobuf/OTel service authority;
those dependencies execute with ordinary private state and SDK source audits.

Frozen-library initialization records temporary allocation extents through a
runtime side table keyed by the initializing goroutine. Records are not inherited
by children and are removed on return/unwind. Publication must expose a complete
graph atomically. Regexp instructions copy wildcard rune tables when exposing
writable state.

## Fault and cleanup

A normal cross-owner violation publishes a permanent `OwnershipError`, revokes
the whole instance and prevents application defers/recover from continuing it.
Trusted runtime/metadata cleanup must finish and release process locks before
discard. Read-only writes to workflow/frozen state instead panic within that
handler before mutation, allowing the SDK to return a query/validator error.

Retained diagnostics contain copied text/stacks, not arbitrary private objects.
GC refunds allocation charges at sweep; successful cleanup releases group roots,
stacks, queued waiters and cache handles. Test reclamation with weak references,
cache/account counts, retained completed handles and late callbacks, rather than
expecting RSS to fall immediately.

## Source map

- Compiler: `ssagen/ssa.go`, `ssagen/isolate_{map,string,atomic}.go`, `walk/builtin.go`.
- Allocator: `runtime/isolate_alloc.go`, `malloc.go`, `mcache.go`, `mheap.go`,
  `mgc*.go`, `mstats.go` and `isolate_resources.go`.
- Checks: `runtime/isolate_heap.go`, `isolate_equal.go`, `isolate_atomic.go`,
  `isolate_readonly.go`, `isolate_metadata.go`, `isolate_itab.go`.
- Collections/calls: `internal/runtime/maps`, `runtime/chan.go`, `select.go`,
  `reflect`, `sync/atomic`, `runtime/panic.go` and goroutine admission.

Paths in this section are relative to `src/`.
