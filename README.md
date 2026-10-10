# The Go Programming Language

Go is an open source programming language that makes it easy to build simple,
reliable, and efficient software.

## Isolate fork

This fork adds statically linked isolates with private package state and optional
deterministic execution. With `isolate.Config.Deterministic`, native goroutines
use FIFO dispatch, supported map keys iterate in canonical order, and `select`
shares a replay-seeded ChaCha8 stream with top-level `math/rand`, `math/rand/v2`
and `crypto/rand` APIs. Set `isolate.Config.RandomSeed` before initialization to
provide a stable 32-byte execution seed; the default is all zeros. Read-only
services use scratch random state and cannot advance workflow randomness.

Deterministic `math.Log` and `math.Exp` use a fixed sequence of IEEE fused
operations, including the software fallback when hardware FMA is unavailable.
This preserves the original arm64 distribution-tail observations across supported
CPUs. Other floating-point expressions and unreviewed math APIs still require
portability review; ordinary Go permits architecture-dependent implicit fusion.
Normal and exponential distributions also pin fused rounding in tail returns
and sample-rejection thresholds.

**Security:** `crypto/rand` inside deterministic isolates produces predictable
replay data, not cryptographic entropy. Do not use it for encryption keys,
passwords, authentication tokens, cryptographic secrets or encryption nonces
that require freshness on replay. Identical seeds and call sequences repeat
identical bytes. Use ordinary host code for security-sensitive randomness;
host `crypto/rand` retains OS entropy. `crypto/rand.Prime` and cryptographic key
generation remain unsupported inside isolates. This POC changes its previous
random/select sequences; replay compatibility with those sequences is deferred.

![Gopher image](https://golang.org/doc/gopher/fiveyears.jpg)
*Gopher image by [Renee French][rf], licensed under [Creative Commons 4.0 Attribution license][cc4-by].*

Our canonical Git repository is located at https://go.googlesource.com/go.
There is a mirror of the repository at https://github.com/golang/go.

Unless otherwise noted, the Go source files are distributed under the
BSD-style license found in the LICENSE file.

### Download and Install

#### Binary Distributions

Official binary distributions are available at https://go.dev/dl/.

After downloading a binary release, visit https://go.dev/doc/install
for installation instructions.

#### Install From Source

If a binary distribution is not available for your combination of
operating system and architecture, visit
https://go.dev/doc/install/source
for source installation instructions.

### Contributing

Go is the work of thousands of contributors. We appreciate your help!

To contribute, please read the contribution guidelines at https://go.dev/doc/contribute.

Note that the Go project uses the issue tracker for bug reports and
proposals only. See https://go.dev/wiki/Questions for a list of
places to ask questions about the Go language.

[rf]: https://reneefrench.blogspot.com/
[cc4-by]: https://creativecommons.org/licenses/by/4.0/
