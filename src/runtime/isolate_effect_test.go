// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/abi"
	"os"
	"reflect"
	"runtime"
	"testing"
	_ "unsafe"
)

// The linker classifies the final runtime name, even when a declaration uses
// an alias or the compiler emits a generic or method-value wrapper.
//
//go:linkname isolateLinkedNanotime runtime.nanotime
func isolateLinkedNanotime() int64

func TestIsolateLinkedEffectFlags(t *testing.T) {
	for _, tt := range []struct {
		name string
		fn   any
		want abi.FuncFlag
	}{
		{"process effect", os.Getenv, abi.FuncFlagIsolateEffectForbidden},
		{"runtime effect", runtime.GC, abi.FuncFlagIsolateEffectForbidden},
		{"generic effect", runtime.AddCleanup[int, int], abi.FuncFlagIsolateEffectForbidden},
		{"linkname effect", isolateLinkedNanotime, abi.FuncFlagIsolateEffectForbidden},
		{"unsafe constructor", reflect.NewAt, abi.FuncFlagIsolateUnsafeReflection},
		{"unsafe slice", reflect.SliceAt, abi.FuncFlagIsolateUnsafeReflection},
		{"unsafe method", reflect.Value.UnsafePointer, abi.FuncFlagIsolateUnsafeReflection},
		{"unsafe method value", reflect.ValueOf(new(int)).UnsafePointer, abi.FuncFlagIsolateUnsafeReflection},
		{"allowed reflection", reflect.ValueOf, 0},
		{"allowed process value", os.IsNotExist, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pc := reflect.ValueOf(tt.fn).Pointer()
			mask := abi.FuncFlagIsolateEffectForbidden | abi.FuncFlagIsolateUnsafeReflection
			if got := runtime.IsolateEffectFlagsForTest(pc) & mask; got != tt.want {
				t.Fatalf("%s flags = %#x, want %#x", runtime.FuncForPC(pc).Name(), got, tt.want)
			}
		})
	}
}
