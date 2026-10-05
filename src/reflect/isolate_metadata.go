// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect

import (
	"internal/abi"
	_ "unsafe" // for go:linkname
)

// Only type construction uses the process metadata scope. Value allocation,
// reflective calls, frame pools, and caller-provided predicates stay under the
// caller's owner. Type constructors copy input descriptions into immutable
// metadata; they never publish input slices or decoded application values.
//
//go:linkname isolateEnterMetadata runtime.isolateEnterMetadata
func isolateEnterMetadata() uintptr

//go:linkname isolateLeaveMetadata runtime.isolateLeaveMetadata
func isolateLeaveMetadata(uintptr)

// Record only the canonical result, after cache insertion and shared-lock
// cleanup. Borrowed input descriptions are never registered.
//
//go:linkname isolatePublishType runtime.isolatePublishType
func isolatePublishType(*abi.Type)
