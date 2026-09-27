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
