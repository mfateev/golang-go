// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.isolates

package isolate

import (
	"internal/isolatebridge"
	"sync"
)

// Synthetic factories exercise lifecycle failure paths without making arbitrary
// entry registration or privileged callbacks available to applications.
var programsForTest sync.Map

func RegisterTestProgram(name string, entry isolatebridge.ProgramEntry) {
	programsForTest.Store(name, Program{name: name, entry: entry})
}
func LookupTestProgram(name string) (Program, bool) {
	value, ok := programsForTest.Load(name)
	if !ok {
		return Program{}, false
	}
	return value.(Program), true
}
