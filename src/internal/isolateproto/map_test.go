// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolateproto

import (
	"reflect"
	"testing"
)

func TestRangeMapMutation(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2, "c": 3}
	var got []string
	RangeMap(m, func(key string, value int) bool {
		got = append(got, key)
		if key == "a" {
			delete(m, "b")
			m["c"] = 30
			m["d"] = 4
		}
		if key == "c" && value != 30 {
			t.Fatalf("updated value = %d, want 30", value)
		}
		return true
	})
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("visited keys = %v, want [a c]", got)
	}
}

func TestRangeMapOrderedKeyTypes(t *testing.T) {
	type namedInt int64
	ints := map[namedInt]string{9: "nine", -2: "minus two", 0: "zero"}
	var got []namedInt
	RangeMap(ints, func(key namedInt, _ string) bool {
		got = append(got, key)
		return true
	})
	if !reflect.DeepEqual(got, []namedInt{-2, 0, 9}) {
		t.Fatalf("signed key order = %v", got)
	}

	unsigned := map[uint64]bool{1<<63 + 1: true, 2: true, 0: true}
	var gotUnsigned []uint64
	RangeMap(unsigned, func(key uint64, _ bool) bool {
		gotUnsigned = append(gotUnsigned, key)
		return len(gotUnsigned) < 2
	})
	if !reflect.DeepEqual(gotUnsigned, []uint64{0, 2}) {
		t.Fatalf("unsigned key order or early stop = %v", gotUnsigned)
	}
}
