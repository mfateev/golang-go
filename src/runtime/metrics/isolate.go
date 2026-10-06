// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package metrics

import _ "unsafe" // for go:linkname

//go:linkname isolateActive runtime.isolateActive
func isolateActive() bool

// All copies process descriptions and their string backing data, so the public
// query cannot retain a process heap string in a private value graph.
//
//go:linkname isolateCopyBoundaryString runtime.isolateCopyBoundaryString
func isolateCopyBoundaryString(string) string
