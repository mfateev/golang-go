# Explicit isolate build contract

`go build -isolate-config=file.json` and `go test -isolate-config=file.json`
accept an explicit, versioned description of entries and private package state.
The Go command does not scan source directives or select external dependencies.
The SDK's `cmd/tgo` owns those policies. Ordinary `go` commands retain ordinary
behavior, even when source comments contain SDK directives.

The JSON format version is 1:

```json
{
  "format_version": 1,
  "entries": [{
    "package": "example.org/jobs",
    "function": "Entry",
    "value": "entryValue",
    "invoke": "entryInvoke"
  }],
  "sources": {"example.org/jobs": "package jobs\n...typed adapter source..."},
  "state_packages": ["example.org/jobs"],
  "readonly_packages": [],
  "support_packages": []
}
```

The source is compiled in the entry's own package so it can use private types.
It supplies a `func() any` value accessor and an invoker with signature
`func(decode, encode func(...isolate.Value) error) error`. Each accessor/invoker
has a one-argument `//go:linkname` declaration to allow the generated process
entry to link it. These functions receive ordinary application instrumentation;
this contract grants no allocation, reflection, metadata or callback privilege.
The accessor returns the original function; the invoker decodes typed local
slots, calls it directly and encodes the result. Both run within the instance.

Adapters may import the public `isolate` API, `unsafe` for the linkname directive,
and imports already present in the source package. The hook does not permit
external adapters to import privileged GOROOT packages. The toolchain generates
the process entry, references compiler-owned package descriptors, validates
metadata versions and constructs state factories internally. Public handles
expose neither raw descriptors nor a registration callback.

`state_packages` selects private layouts among each entry's original transitive
source imports. `support_packages` adds host-reachable support roots to each
entry's state graph. `readonly_packages` selects layouts to recreate under a
read-only service's scratch owner. It must be a subset of `state_packages`.
These selections control ownership and initialization, not privileged trust.
Standard frozen-library sharing retains its independently audited core policy.
During the metadata migration, the core also retains its previous dependency
source checks: the existing library-specific compiler rewrites still require
them. The SDK performs the same checks before requesting a build.

For test variants, an entry may include `for_test` with the package under test's
import path. Its source key is `package [for_test.test]`, matching `go list -test`
identities. Ordinary adapters remain separate from declarations in `_test.go`;
external test packages and imported test variants retain distinct identities.

The build action hashes adapter source contents and the effective per-package
compiler flags, including state/entry selection in the transitive import graph.
Changing the manifest's pathname alone does not invalidate dependencies. An
unrelated entry does not invalidate a shared dependency; changing its relevant
state selection or imported inline-entry diagnostics does. Mandatory effects
and ownership checks remain enabled when relevant private selection is empty.

`build_isolate_manifest` is the outside-GOROOT regression fixture. The compiler
scripts also cover cache reuse, imported/inlined/generic bodies, internal and
external tests, ownership failures, state initialization and read-only libraries.
