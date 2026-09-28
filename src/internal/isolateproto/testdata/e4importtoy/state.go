// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency

package e4importtoy

import "internal/isolateproto/testdata/e4deptoy"

var observed = e4deptoy.Epoch()
var count = new(int)
var read = func() int { return *count }

func init() { *count = observed }

func Run() (int, int, int, int) {
	*count++
	return observed, *count, read(), e4deptoy.Step()
}
