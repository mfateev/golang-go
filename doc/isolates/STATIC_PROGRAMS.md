# Statically linked isolate programs

Status: **experimental static build**. Marked workflow functions and their
package-state factories are linked into the host executable. Broad standard
library ownership and effect classification and the native scheduler remain
under development. Dynamic loading is a
[future enhancement](./DYNAMIC_LOADING.md).

## Function entry points

Mark ordinary Go functions with `//temporal:workflow`. A normal `tgo build`
discovers marked functions in the host's import graph and generates typed
invokers, function handles, and package-state factories. A package may contain
multiple marked functions and ordinary functions; functions in different
packages can also be linked into the same executable.

```go
//temporal:workflow
func Orders(input OrderRequest) (OrderResult, error) {
    // Ordinary Go workflow code and SDK-mediated host operations.
}
```

The marker leaves the function signature unchanged. The host uses
`isolate.LookupFunction(Orders)` to get a handle. An SDK can pass
`Handle.ProgramWithHandle(dispatch)` to `isolate.New`; the dispatcher decodes
copied request bytes, invokes the function through typed argument and result
slots, and returns copied bytes. Go values stay inside the isolate.

The generated function table is available after imported package
initializers, so lookup and worker registration belong in host `main`, not
package `init`. Each `isolate.New` call creates an instance with the selected
function's package state and its own runtime configuration.

Marked functions must be concrete, top-level functions with a body. Supported
results are no values, `error`, or `(result, error)`. Methods, generics,
variadic arguments, cgo source, dot imports, and Temporal
`workflow.Context` arguments are rejected. Build integration currently
requires one host executable using the default gc build mode; `-n` and
`-cover` are not supported.

State selection is currently conservative at package level. A marked
function's package and dependency graph receive instance state under the
current ownership policy. Functions in one package share that package's
compiled definition and state layout; separate isolate instances still get
separate package state. Function-level graph trimming and complete effect
validation remain future work.

`-isolate-report=ownership.json` writes each marked function's reachable
packages and their current state selection. Process-wide standard packages in
the report have **not** been certified isolate-safe.

## Dependency versions

A single executable contains one compiled definition for each Go import path,
using the version selected by its module graph. Distinct major-version import
paths, such as `example.org/dep` and `example.org/dep/v2`, can coexist if
they pass the same state and subset checks. This follows Go's
[major-version import-path rule](https://go.dev/ref/mod#major-version-suffixes).

Two versions behind the same import path are not supported. Coexisting versions
would require rewriting package paths or another namespace mechanism, affecting
type identity, shared dependencies, linking, and ownership checks. Separate
`go.mod` files do not make same-path versions coexist in one executable.

## Current limitations

The compiler links all discovered entry functions statically into the host
binary. Runtime loading and unloading are not implemented. Package ownership
is selected at package level, the standard library audit is incomplete, and
the POC is not a security boundary for arbitrary untrusted Go code. See the
[productization plan](./PRODUCTIZATION_PLAN.md) for the remaining gates.
