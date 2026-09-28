// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout

// Package e4layouttoy probes generated layout and GC metadata for all mutable
// globals in an opt-in package. It contains no process-owned registration.
package e4layouttoy

import "fmt"

var count = new(int)
var values = map[string]int{"n": 0}
var read = func() int { return *count }
var epoch = 41

func init() { epoch -= 40 }

func SetEpoch(n int) { epoch = n }

func RunCurrent() string {
	*count++
	values["n"]++
	return fmt.Sprintf("%d/%d/%d/%d", *count, values["n"], read(), epoch)
}
