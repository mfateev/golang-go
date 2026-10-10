// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math_test

import (
	. "math"
	"runtime"
	"testing"
)

// The version-one fixtures were recorded on arm64. Check that pinning the
// evaluation order preserves that baseline, including distribution tail inputs.
func TestIsolateMathOriginalRounding(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("original corpus used arm64 math implementations")
	}
	state := uint64(1)
	for range 65536 {
		state = state*6364136223846793005 + 1442695040888963407
		x := float64(state>>11) * (1.0 / (1 << 53))
		if got, want := Float64bits(IsolateLog(x)), Float64bits(Log(x)); got != want {
			t.Fatalf("Log(%g): got %x want %x", x, got, want)
		}
		x = -16 * x
		if got, want := Float64bits(IsolateExp(x)), Float64bits(Exp(x)); got != want {
			t.Fatalf("Exp(%g): got %x want %x", x, got, want)
		}
	}
}
