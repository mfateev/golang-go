// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolateproto

import "sort"

// OrderedKey is the Phase 1 map-iteration contract. Pointer, interface,
// floating-point, and composite keys are excluded. Ordinary map range is
// nondeterministic and remains outside the prototype's contract.
type OrderedKey interface {
	~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// SortedKeys returns a stable ascending snapshot of a map's keys. Mutating
// the map while consuming this snapshot follows ordinary lookup semantics.
func SortedKeys[K OrderedKey, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a] < keys[b] })
	return keys
}

// RangeMap visits the keys present at the start of the range in ascending
// order. It looks up each value immediately before calling yield, so a deleted
// key is skipped and an updated value is observed. Keys added after the range
// starts are not visited. This is one deterministic choice allowed by Go's
// map range rules, for the restricted OrderedKey set.
func RangeMap[K OrderedKey, V any](m map[K]V, yield func(K, V) bool) {
	for _, key := range SortedKeys(m) {
		value, ok := m[key]
		if ok && !yield(key, value) {
			return
		}
	}
}
