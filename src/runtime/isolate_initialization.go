// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/runtime/atomic"
	"unsafe"
)

// Shared-cache initialization is process work, not persistent goroutine state.
// Keep entries only while initializers run, rather than growing every g. The
// allocator reads the list without locking or allocating. Its usual empty-list
// path is one atomic load; concurrent initializers have separate recorders.
//
// Writers serialize insertion/removal without allocating under the lock. Links
// are atomic, and removed entries retain their next link so an in-flight reader
// can finish traversing them. GC keeps such entries alive until readers finish.
// Only the entry's goroutine accesses state and depth; children inherit neither.
type isolateInitializationEntry struct {
	gp    *g
	next  atomic.Pointer[isolateInitializationEntry]
	state *isolateInitialization
	depth uint32
}

var isolateInitializations struct {
	mu   mutex
	head atomic.Pointer[isolateInitializationEntry]
}

//go:nosplit
func isolateInitializationForG(gp *g) *isolateInitializationEntry {
	for entry := isolateInitializations.head.Load(); entry != nil; entry = entry.next.Load() {
		if entry.gp == gp {
			return entry
		}
	}
	return nil
}

//go:nosplit
func isolateCurrentInitialization() *isolateInitialization {
	if entry := isolateInitializationForG(getg()); entry != nil {
		return entry.state
	}
	return nil
}

func isolateBeginInitialization() *isolateInitialization {
	gp := getg()
	entry := isolateInitializationForG(gp)
	var parent *isolateInitialization
	if entry != nil {
		parent = entry.state
		entry.state = nil // Exclude the recorder's own allocations.
	}
	state := &isolateInitialization{parent: parent}
	if entry == nil {
		entry = &isolateInitializationEntry{gp: gp}
		lock(&isolateInitializations.mu)
		entry.next.Store(isolateInitializations.head.Load())
		isolateInitializations.head.Store(entry)
		unlock(&isolateInitializations.mu)
	}
	entry.depth++
	state.entry = entry
	entry.state = state
	return state
}

func isolateEndInitialization(state *isolateInitialization) {
	entry := state.entry
	if entry.gp != getg() || entry.state != state || entry.depth == 0 {
		throw("isolate: cache initialization scope changed")
	}
	entry.state = state.parent
	entry.depth--
	if entry.depth != 0 {
		return
	}
	lock(&isolateInitializations.mu)
	link := &isolateInitializations.head
	for current := link.Load(); current != nil; current = link.Load() {
		if current == entry {
			link.Store(current.next.Load())
			unlock(&isolateInitializations.mu)
			return
		}
		link = &current.next
	}
	throw("isolate: cache initialization entry missing")
}

// Only audited standard-library initializers use this allocation recorder.
type isolateInitializationAllocation struct {
	p    unsafe.Pointer
	size uintptr
	typ  *abi.Type
	next *isolateInitializationAllocation
}
type isolateInitialization struct {
	allocations *isolateInitializationAllocation
	parent      *isolateInitialization
	entry       *isolateInitializationEntry
}

func isolateRecordInitializationAllocation(p unsafe.Pointer, size uintptr, typ *abi.Type) {
	gp := getg()
	state := isolateCurrentInitialization()
	if state == nil || gp.isolateOwner != 0 {
		return
	}
	state.entry.state = nil
	record := &isolateInitializationAllocation{p: p, size: size, typ: typ, next: state.allocations}
	state.allocations = record
	state.entry.state = state
}
