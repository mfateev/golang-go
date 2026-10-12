// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolateabi defines the contracts shared by the build tools and runtime.
// The POC implements one current interface. These numeric markers are sanity
// checks for its metadata, API and deterministic behavior; they can change freely
// during development and do not promise compatibility with earlier revisions.
// They do not identify source revisions. Zero is never a valid marker.
package isolateabi

const (
	MetadataVersion = 1
	APIVersion      = 1
	// Version 1 uses one deterministic random stream for select and random APIs.
	// This describes the current POC behavior.
	DeterminismVersion = 1
)
