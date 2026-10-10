// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import "internal/isolateabi"

const (
	MetadataVersion    = isolateabi.MetadataVersion
	APIVersion         = isolateabi.APIVersion
	DeterminismVersion = isolateabi.DeterminismVersion
)

// Contract identifies independent integration contracts. Versions are exact;
// a future release must explicitly implement an older version to accept it.
// These versions describe compatibility, not a toolchain source revision.
type Contract struct {
	Metadata    uint32 `json:"metadata"`
	API         uint32 `json:"api"`
	Determinism uint32 `json:"determinism"`
}

// CurrentContract returns the contracts implemented by the linked runtime.
func CurrentContract() Contract {
	return Contract{Metadata: MetadataVersion, API: APIVersion, Determinism: DeterminismVersion}
}

// MetadataVersion returns the build tool's metadata version for this function.
func (h Handle) MetadataVersion() uint32 { return h.entry.MetadataVersion }
