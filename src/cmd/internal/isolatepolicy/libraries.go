// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatepolicy

import runtimepolicy "internal/isolatepolicy"

func FrozenLibraryPackage(path string) bool { return runtimepolicy.FrozenLibraryPackage(path) }
