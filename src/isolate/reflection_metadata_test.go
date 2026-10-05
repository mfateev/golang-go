// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"internal/isolatebridge"
	"reflect"
	"testing"
)

func TestReflectionTypeRegistryAcrossIsolates(t *testing.T) {
	// Repeated pointer construction eventually needs descriptors absent from
	// the binary. The runtime's global offset registry must accept these from
	// several owners without retaining an isolate owner on its mutable maps.
	base := reflect.TypeFor[struct{ Value int }]()
	for instance := 0; instance < 3; instance++ {
		b := isolatebridge.New()
		if err := b.EnableDeterminism(); err != nil {
			t.Fatal(err)
		}
		var local map[string]int
		b.Run(func() {
			current := base
			for depth := 0; depth < 16*(instance+1); depth++ {
				pointer := reflect.PointerTo(current)
				if pointer.Elem() != current {
					t.Fatal("pointer descriptor lost element type")
				}
				current = pointer
			}
			// Check metadata registration restored the caller's map allocation owner.
			local = make(map[string]int)
			local["value"] = instance
		})
		foreign := isolatebridge.New()
		foreign.RunOwner(func() {
			defer func() {
				if recover() == nil {
					t.Error("reflection left process ownership on user map")
				}
			}()
			local["value"] = 99
		})
	}
}
