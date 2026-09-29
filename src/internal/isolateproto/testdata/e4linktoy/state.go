// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency && phase2b_linkcheck

package e4linktoy

import "internal/isolateproto/testdata/e4deptoy"

var observed = e4deptoy.EpochNoInline()

func Observed() int { return observed }
