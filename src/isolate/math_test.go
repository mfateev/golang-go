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
	var digests [6]string
	b.Run(func() {
		var data [6][]byte
		state := uint64(1)
		log, exp := math.Log, math.Exp
		for range 65536 {
			state = state*6364136223846793005 + 1442695040888963407
			x := float64(state>>11) * (1.0 / (1 << 53))
			data[0] = binary.LittleEndian.AppendUint64(data[0], math.Float64bits(log(x)))
			data[1] = binary.LittleEndian.AppendUint64(data[1], math.Float64bits(exp(-16*x)))
		}
		r1 := rand.New(rand.NewSource(1))
		r2 := randv2.New(randv2.NewPCG(1, 2))
		for range 65536 {
			data[2] = binary.LittleEndian.AppendUint64(data[2], math.Float64bits(r1.NormFloat64()))
			data[3] = binary.LittleEndian.AppendUint64(data[3], math.Float64bits(r1.ExpFloat64()))
			data[4] = binary.LittleEndian.AppendUint64(data[4], math.Float64bits(r2.NormFloat64()))
			data[5] = binary.LittleEndian.AppendUint64(data[5], math.Float64bits(r2.ExpFloat64()))
		}
		for i := range data {
			digests[i] = fmt.Sprintf("%x", sha256.Sum256(data[i]))
		}
	})
	want := [6]string{
		"36bd2351eef063e50432b6baaa9afddf587041d98385c83b79aa75a2c45793f8",
		"afd93df1bc7bbf01a15af4e2ac9437fe8544ec3ac55868938a3aeb2b9d21d710",
		"6601a048240847633d7008cc5088fe596f48eb9a7204c6fad0ecb9a88649ecec",
		"84002f9ad43630ebf52b0c53e8e6207cef3e0a476ac7ef54d3e1ef72a8df85d8",
		"30798ce660cde1e2823b456a7da8728b8020e7136a42cd0b1654249811085465",
		"9c05980c17394ea19b82a04f745c85dd8007b25ed6d18817fb39ec9942c13573",
	}
	names := [...]string{"Log", "Exp", "rand.NormFloat64", "rand.ExpFloat64", "rand/v2.NormFloat64", "rand/v2.ExpFloat64"}
	for i := range want {
		if digests[i] != want[i] {
			t.Errorf("%s bits changed: got %s want %s", names[i], digests[i], want[i])
		}
	}
}
