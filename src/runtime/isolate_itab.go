// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/goarch"
	"unsafe"
)

type isolateItabRange struct{ start, end uintptr }

// Compiler checks can run while sync primitives have disabled race sync
// tracking. A Go map would still emit memory events there, losing the lock's
// happens-before edge. Use runtime-managed immutable entries, like itabs
// themselves, under the same real lock. Entries retain no Go heap pointers.
type isolateItabEntry struct {
	word   uintptr
	bounds isolateItabRange
	next   *isolateItabEntry
}

var isolateItabWords [1024]*isolateItabEntry

func isolateItabBucket(word uintptr) uintptr {
	return (word/goarch.PtrSize ^ word>>12) & (uintptr(len(isolateItabWords)) - 1)
}

// Only the runtime publishes completed persistent interface tables. The data
// receiver of an interface is never part of its table; these records contain
// canonical type metadata and immutable code pointers only.
func isolatePublishItab(m *itab) {
	if getg().isolateOwner != 0 {
		throw("isolate: private interface table publication")
	}
	start := uintptr(unsafe.Pointer(m))
	end := start + unsafe.Sizeof(itab{}) + uintptr(len(m.Inter.Methods)-1)*goarch.PtrSize
	reflectOffsLock()
	defer reflectOffsUnlock()
	for addr := start; addr < end; addr += goarch.PtrSize {
		bucket := isolateItabBucket(addr)
		entry := (*isolateItabEntry)(persistentalloc(unsafe.Sizeof(isolateItabEntry{}), goarch.PtrSize, &memstats.other_sys))
		entry.word, entry.bounds = addr, isolateItabRange{start, end}
		entry.next = isolateItabWords[bucket]
		isolateItabWords[bucket] = entry
	}
}

func isolateReadOnlyItabRange(addr, end uintptr) bool {
	reflectOffsLock()
	defer reflectOffsUnlock()
	word := addr &^ (goarch.PtrSize - 1)
	for entry := isolateItabWords[isolateItabBucket(word)]; entry != nil; entry = entry.next {
		if entry.word == word {
			return addr >= entry.bounds.start && end < entry.bounds.end
		}
	}
	return false
}
