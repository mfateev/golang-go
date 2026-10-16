// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.isolates

package isolate

import (
	"internal/isolatebridge"
	"strings"
	"testing"
)

func TestProgramMetadataRejectedBeforeInitialization(t *testing.T) {
	for _, version := range []uint32{0, 2, ^uint32(0)} {
		initialized := false
		program := Program{entry: isolatebridge.ProgramEntry{MetadataVersion: version, Main: func() {}, NewState: func() (func(func()), error) { initialized = true; return func(fn func()) { fn() }, nil }}}
		instance, err := New(Config{Program: program})
		if initialized || instance != nil || err == nil || !strings.Contains(err.Error(), "incompatible program metadata version") {
			t.Fatalf("version %d: instance=%v initialized=%v err=%v", version, instance, initialized, err)
		}
	}
}
