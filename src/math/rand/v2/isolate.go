// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rand

import _ "unsafe" // for go:linkname

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

//go:linkname isolateFMA32 math.isolateRandFMA32
func isolateFMA32(x, y, z float32) float32
