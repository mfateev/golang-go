// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math_test

import (
	. "math"
	"math/big"
	"runtime"
	"testing"
)

func TestIsolateRandFMA32(t *testing.T) {
	// The product is just below half a float32 ulp. Adding in float64 loses
	// the distinction and round-to-even would choose the wrong float32.
	x := float32(1 + 1.0/(1<<23))
	y := float32((1 - 1.0/(1<<23)) / (1 << 24))
	z := float32(1 + 1.0/(1<<23))
	if got := IsolateRandFMA32(x, y, z); got != z {
		t.Fatalf("float32 double rounding: got %x want %x", Float32bits(got), Float32bits(z))
	}
	if naive := float32(FMA(float64(x), float64(y), float64(z))); naive == z {
		t.Fatal("fixture does not exercise double rounding")
	}
}

func TestIsolateRandFMA32Exact(t *testing.T) {
	// Cover the finite positive interpolation domain using exact rational
	// arithmetic, independently of either floating-point FMA implementation.
	state := uint64(1)
	next := func() float32 {
		state = state*6364136223846793005 + 1442695040888963407
		return float32(float64(state>>11) * (1.0 / (1 << 53)))
	}
	for range 4096 {
		x, y, z := next(), next(), float32(0.001)+next()
		product := new(big.Rat).Mul(new(big.Rat).SetFloat64(float64(x)), new(big.Rat).SetFloat64(float64(y)))
		exact := product.Add(product, new(big.Rat).SetFloat64(float64(z)))
		want, _ := exact.Float32()
		if got := IsolateRandFMA32(x, y, z); got != want {
			t.Fatalf("FMA32(%g, %g, %g): got %x want %x", x, y, z, Float32bits(got), Float32bits(want))
		}
	}
}

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
