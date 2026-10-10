// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package math

import _ "unsafe" // for go:linkname

//go:linkname isolateDeterministic runtime.isolateDeterministic
func isolateDeterministic() bool

// isolateLog fixes the rounding operations used by the original arm64 replay
// corpus. Architecture-specific Log implementations and optional compiler
// fusion otherwise change the low bits of random distribution tails. Explicit
// FMA has the same IEEE rounding on hardware and the software fallback.
func isolateLog(x float64) float64 {
	const (
		Ln2Hi = 6.93147180369123816490e-01
		Ln2Lo = 1.90821492927058770002e-10
		L1    = 6.666666666666735130e-01
		L2    = 3.999999999940941908e-01
		L3    = 2.857142874366239149e-01
		L4    = 2.222219843214978396e-01
		L5    = 1.818357216161805012e-01
		L6    = 1.531383769920937332e-01
		L7    = 1.479819860511658591e-01
	)
	switch {
	case IsNaN(x) || IsInf(x, 1):
		return x
	case x < 0:
		return NaN()
	case x == 0:
		return Inf(-1)
	}
	f1, ki := Frexp(x)
	if f1 < Sqrt2/2 {
		f1 *= 2
		ki--
	}
	f, k := f1-1, float64(ki)
	s := f / (2 + f)
	s2 := s * s
	s4 := s2 * s2
	p := FMA(s4, FMA(s4, FMA(s4, L7, L5), L3), L1)
	q := FMA(s4, FMA(s4, L6, L4), L2)
	r := FMA(s2, p, float64(s4*q))
	half := 0.5 * f
	a := FMA(s, FMA(half, f, r), float64(k*Ln2Lo))
	b := float64(FMA(half, f, -a) - f)
	return FMA(k, Ln2Hi, -b)
}

// isolateExp specifies the fused operations of the original arm64 Exp path.
// Explicit conversions prevent additional implicit fusion on other targets.
func isolateExp(x float64) float64 {
	const (
		Ln2Hi     = 6.93147180369123816490e-01
		Ln2Lo     = 1.90821492927058770002e-10
		Log2e     = 1.44269504088896338700e+00
		Overflow  = 7.09782712893383973096e+02
		Underflow = -7.45133219101941108420e+02
		NearZero  = 1.0 / (1 << 28)
		P1        = 1.66666666666666657415e-01
		P2        = -2.77777777770155933842e-03
		P3        = 6.61375632143793436117e-05
		P4        = -1.65339022054652515390e-06
		P5        = 4.13813679705723846039e-08
	)
	switch {
	case IsNaN(x):
		return x
	case x > Overflow:
		return Inf(1)
	case x < Underflow:
		return 0
	case -NearZero < x && x < NearZero:
		return 1 + x
	}
	var k int
	if x < 0 {
		k = int(FMA(Log2e, x, -0.5))
	} else {
		k = int(FMA(Log2e, x, 0.5))
	}
	hi := FMA(-float64(k), Ln2Hi, x)
	lo := float64(k) * Ln2Lo
	r := hi - lo
	t := r * r
	p := FMA(t, FMA(t, FMA(t, FMA(t, P5, P4), P3), P2), P1)
	c := FMA(-t, p, r)
	y := 1 - (float64(lo-float64(float64(r*c)/(2-c))) - hi)
	return Ldexp(y, k)
}
