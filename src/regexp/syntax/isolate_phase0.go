// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package syntax

import _ "unsafe" // for go:linkname

// The alias maps are immutable after construction. Both the host and the
// independent frozen snapshot prepare them before serving regexp operations.
func init() {
	aliases.once.Do(initAliases)
}

// Prog exposes its instructions and rune slices. Preserve writable expression
// values when compilation uses one of the shared default wildcard tables.
//
//go:linkname privateRuneTable runtime.isolatePrivateRuneTable
func privateRuneTable([]rune) []rune
