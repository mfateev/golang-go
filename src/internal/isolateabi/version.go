// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolateabi defines the contracts shared by the build tools and runtime.
// Versions change independently: a metadata layout change does not by itself
// change scheduling or the copied-byte API. Zero is never a valid version.
package isolateabi

const (
	MetadataVersion = 1
	APIVersion      = 1
	// Version 1 uses one deterministic random stream for select and random APIs.
	// Incompatible changes to observable scheduling, time, maps or randomness
	// require a new version and old-history replay/rollback validation.
	DeterminismVersion = 1
)
