// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package atomic

import "unsafe"

// Value hides its receiver accesses, interface copying and equality behind
// unsafe and assembly. Check in the implementation before procPin or mutation,
// including calls through method values and uninstrumented dependencies.
//
//go:linkname isolateCheckAtomicValue runtime.isolateCheckAtomicValue
func isolateCheckAtomicValue(unsafe.Pointer, any, bool)

//go:linkname isolateCheckAtomicCompare runtime.isolateCheckAtomicCompare
func isolateCheckAtomicCompare(any)
