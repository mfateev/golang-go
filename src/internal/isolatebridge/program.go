// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"internal/isolateabi"
	"strconv"
	"sync"
)

// ProgramEntry is the code and state factory wrapped by the public isolate
// Program handle. Compiler-discovered workflow builds create these entries
// from generated FunctionEntry metadata.
type ProgramEntry struct {
	MetadataVersion uint32
	Main            func()
	NewState        func() (func(func()), error)
}

// This low-level registry supports internal runtime tests and adapters.
// Workflow builds register FunctionEntry metadata instead.
var programs = struct {
	sync.RWMutex
	byName map[string]ProgramEntry
}{byName: make(map[string]ProgramEntry)}

// RegisterProgram adds a low-level named entry to the internal registry.
func RegisterProgram(name string, entry ProgramEntry) {
	if entry.MetadataVersion != isolateabi.MetadataVersion {
		panic("isolate: incompatible program metadata version " + strconv.FormatUint(uint64(entry.MetadataVersion), 10) + ", runtime requires " + strconv.Itoa(isolateabi.MetadataVersion) + "; rebuild with a compatible toolchain")
	}
	if name == "" || entry.Main == nil || entry.NewState == nil {
		panic("isolate: empty program name or incomplete entry")
	}
	programs.Lock()
	defer programs.Unlock()
	if _, ok := programs.byName[name]; ok {
		panic("isolate: duplicate program " + name)
	}
	programs.byName[name] = entry
}

// LookupProgram resolves an entry from the generated static-build table.
func LookupProgram(name string) (ProgramEntry, bool) {
	programs.RLock()
	defer programs.RUnlock()
	entry, ok := programs.byName[name]
	return entry, ok
}
