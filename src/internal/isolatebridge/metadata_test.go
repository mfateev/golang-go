// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"fmt"
	"internal/isolateabi"
	"reflect"
	"strings"
	"testing"
)

func TestMetadataRejectsBeforeRegistration(t *testing.T) {
	fn := func() {}
	for _, version := range []uint32{0, 2, ^uint32(0)} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			defer func() {
				if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "incompatible function metadata version") {
					t.Fatalf("invalid metadata outcome: %v", p)
				}
				if _, ok := LookupFunction(fn); ok {
					t.Fatal("published incompatible function")
				}
			}()
			RegisterFunction(FunctionEntry{MetadataVersion: version, Name: "incompatible", Function: fn})
		})
	}

}

func TestMetadataRegistersCurrentVersion(t *testing.T) {
	fn := func() {}
	t.Cleanup(func() {
		functions.Lock()
		delete(functions.byPC, reflect.ValueOf(fn).Pointer())
		functions.Unlock()
	})
	state := func() (func(func()), error) { return func(fn func()) { fn() }, nil }
	RegisterFunction(FunctionEntry{MetadataVersion: isolateabi.MetadataVersion, Name: "metadata-current", Function: fn, NewState: state, Invoke: func(_, _ func(...Value) error) error { return nil }})
	entry, ok := LookupFunction(fn)
	if !ok || entry.MetadataVersion != isolateabi.MetadataVersion {
		t.Fatal("missing current function metadata")
	}
}
