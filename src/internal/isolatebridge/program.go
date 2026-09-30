// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import "sync"

// The static build's generated main registers program entries before it
// invokes the host main. Entries are process-owned code pointers.
var programs = struct {
	sync.RWMutex
	byName map[string]func()
}{byName: make(map[string]func())}

// RegisterProgram is called only by the generated static-build main.
func RegisterProgram(name string, entry func()) {
	if name == "" || entry == nil {
		panic("isolate: empty program name or nil entry")
	}
	programs.Lock()
	defer programs.Unlock()
	if _, ok := programs.byName[name]; ok {
		panic("isolate: duplicate program " + name)
	}
	programs.byName[name] = entry
}

// LookupProgram resolves an entry from the generated static-build table.
func LookupProgram(name string) (func(), bool) {
	programs.RLock()
	defer programs.RUnlock()
	entry, ok := programs.byName[name]
	return entry, ok
}
