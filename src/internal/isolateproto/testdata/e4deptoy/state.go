// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency

package e4deptoy

var epoch = 40
var count = new(int)

func init() {
	epoch++
	*count = epoch
}

func Epoch() int { return epoch }

func Step() int {
	*count++
	return *count
}
