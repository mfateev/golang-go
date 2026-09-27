//go:build phase0_e4

package isolateproto

import (
	"testing"
	"unsafe"
)

type e4State struct {
	padding [64]byte
	value   uint64
}

var e4Direct uint64
var e4Current = new(e4State)
var e4Base = unsafe.Pointer(e4Current)

const e4Offset = unsafe.Offsetof(e4State{}.value)

func e4Cur() *e4State { return e4Current }

// These microbenchmarks compare instruction shapes for one mutable global.
// They do not measure generated compiler indirection or per-isolate init.
func BenchmarkE4Direct(b *testing.B) {
	for range b.N {
		e4Direct++
	}
}

func BenchmarkE4Accessor(b *testing.B) {
	for range b.N {
		e4Cur().value++
	}
}

func BenchmarkE4BaseOffset(b *testing.B) {
	for range b.N {
		p := (*uint64)(unsafe.Add(e4Base, e4Offset))
		*p++
	}
}
