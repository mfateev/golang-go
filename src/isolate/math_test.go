// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"internal/isolatebridge"
	"math"
	"math/rand"
	randv2 "math/rand/v2"
	"testing"
)

// Pin the floating-point bits, rather than comparing only two runs on the
// same CPU. Native CI also runs this with CPU features disabled, exercising
// software FMA on amd64. Indirect calls must use the same deterministic path.
func TestDeterministicMathBitPatterns(t *testing.T) {
	b := isolatebridge.New()
	if err := b.EnableDeterminism(); err != nil {
		t.Fatal(err)
	}
	var digest string
	b.Run(func() {
		var data []byte
		state := uint64(1)
		log, exp := math.Log, math.Exp
		for range 65536 {
			state = state*6364136223846793005 + 1442695040888963407
			x := float64(state>>11) * (1.0 / (1 << 53))
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(log(x)))
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(exp(-16*x)))
		}
		r1 := rand.New(rand.NewSource(1))
		r2 := randv2.New(randv2.NewPCG(1, 2))
		for range 65536 {
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(r1.NormFloat64()))
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(r1.ExpFloat64()))
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(r2.NormFloat64()))
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(r2.ExpFloat64()))
		}
		digest = fmt.Sprintf("%x", sha256.Sum256(data))
	})
	const want = "25967177104fa031316d9d051dacd6f09a91d5b57a8edc1eca997c5fff695f71"
	if digest != want {
		t.Fatalf("deterministic math bits changed: got %s want %s", digest, want)
	}
}
