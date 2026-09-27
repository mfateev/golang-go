package isolateproto

import (
	"strconv"
	"testing"
)

var mapSink int

// These Phase 0 E2 microbenchmarks measure source-level key sorting only.
// They do not measure a portable runtime hash implementation.
func BenchmarkMapRange1000(b *testing.B) {
	m := make(map[string]int, 1000)
	for n := range 1000 {
		m[strconv.Itoa(n)] = n
	}
	b.ReportAllocs()
	for range b.N {
		sum := 0
		for key := range m {
			sum += m[key]
		}
		mapSink = sum
	}
}

func BenchmarkSortedKeys1000(b *testing.B) {
	m := make(map[string]int, 1000)
	for n := range 1000 {
		m[strconv.Itoa(n)] = n
	}
	b.ReportAllocs()
	for range b.N {
		sum := 0
		for _, key := range SortedKeys(m) {
			sum += m[key]
		}
		mapSink = sum
	}
}
