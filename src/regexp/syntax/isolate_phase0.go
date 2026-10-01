// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package syntax

// The alias maps are immutable after construction and shared by the process.
// In the static isolate probe, prepare them at process startup so the first
// isolate to parse a Unicode class does not populate process state with its
// own allocations. A general immutable-state classification is still needed.
func init() {
	aliases.once.Do(initAliases)
}
