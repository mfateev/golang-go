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
		for _, kind := range []string{"function", "program"} {
			t.Run(fmt.Sprintf("%s/%d", kind, version), func(t *testing.T) {
				defer func() {
					if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "incompatible "+kind+" metadata version") {
						t.Fatalf("invalid metadata outcome: %v", p)
					}
					if _, ok := LookupFunction(fn); ok {
						t.Fatal("published incompatible function")
					}
					if _, ok := LookupProgram("incompatible"); ok {
						t.Fatal("published incompatible program")
					}
				}()
				if kind == "function" {
					RegisterFunction(FunctionEntry{MetadataVersion: version, Name: "incompatible", Function: fn})
				} else {
					RegisterProgram("incompatible", ProgramEntry{MetadataVersion: version})
				}
			})
		}
	}
}

func TestMetadataRegistersCurrentVersion(t *testing.T) {
	fn := func() {}
	t.Cleanup(func() {
		functions.Lock()
		delete(functions.byPC, reflect.ValueOf(fn).Pointer())
		functions.Unlock()
		programs.Lock()
		delete(programs.byName, "metadata-current")
		programs.Unlock()
	})
	state := func() (func(func()), error) { return func(fn func()) { fn() }, nil }
	RegisterFunction(FunctionEntry{MetadataVersion: isolateabi.MetadataVersion, Name: "metadata-current", Function: fn, NewState: state, Invoke: func(_, _ func(...Value) error) error { return nil }})
	entry, ok := LookupFunction(fn)
	if !ok || entry.MetadataVersion != isolateabi.MetadataVersion {
		t.Fatal("missing current function metadata")
	}
	RegisterProgram("metadata-current", ProgramEntry{MetadataVersion: isolateabi.MetadataVersion, Main: fn, NewState: state})
	program, ok := LookupProgram("metadata-current")
	if !ok || program.MetadataVersion != isolateabi.MetadataVersion {
		t.Fatal("missing current program metadata")
	}
}
