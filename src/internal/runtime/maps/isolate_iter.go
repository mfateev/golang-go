// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maps

import (
	"internal/abi"
	"unsafe"
)

func isolateCheckKeyKind(kind abi.Kind) {
	switch kind {
	case abi.String, abi.Int, abi.Int8, abi.Int16, abi.Int32, abi.Int64,
		abi.Uint, abi.Uint8, abi.Uint16, abi.Uint32, abi.Uint64:
		return
	default:
		panic("isolate: deterministic map iteration requires integer or string keys")
	}
}

func (it *Iter) initIsolateKeys() {
	// Preserve the normal iterator only for collecting the initial key set.
	// Keys are copied into typed storage so growing or clearing the map cannot
	// change a retained key and the GC continues to trace string backing data.
	raw := *it
	keys := make([]unsafe.Pointer, 0, it.m.used)
	storage := newarray(it.typ.Key, int(it.m.used))
	for raw.Next(); raw.Key() != nil; raw.Next() {
		key := unsafe.Add(storage, uintptr(len(keys))*it.typ.Key.Size_)
		typedmemmove(it.typ.Key, key, raw.Key())
		keys = append(keys, key)
	}
	isolateSortKeys(keys, it.typ.Key.Kind())
	it.isolateKeys = keys
}

// Heap sort avoids importing sort (which depends on the map runtime itself).
func isolateSortKeys(keys []unsafe.Pointer, kind abi.Kind) {
	less := func(i, j int) bool { return isolateKeyLess(kind, keys[i], keys[j]) }
	sift := func(root, end int) {
		for {
			child := root*2 + 1
			if child >= end {
				return
			}
			if child+1 < end && less(child, child+1) {
				child++
			}
			if !less(root, child) {
				return
			}
			keys[root], keys[child] = keys[child], keys[root]
			root = child
		}
	}
	for root := len(keys)/2 - 1; root >= 0; root-- {
		sift(root, len(keys))
	}
	for end := len(keys) - 1; end > 0; end-- {
		keys[0], keys[end] = keys[end], keys[0]
		sift(0, end)
	}
}

func isolateKeyLess(kind abi.Kind, a, b unsafe.Pointer) bool {
	switch kind {
	case abi.String:
		return *(*string)(a) < *(*string)(b)
	case abi.Int:
		return *(*int)(a) < *(*int)(b)
	case abi.Int8:
		return *(*int8)(a) < *(*int8)(b)
	case abi.Int16:
		return *(*int16)(a) < *(*int16)(b)
	case abi.Int32:
		return *(*int32)(a) < *(*int32)(b)
	case abi.Int64:
		return *(*int64)(a) < *(*int64)(b)
	case abi.Uint:
		return *(*uint)(a) < *(*uint)(b)
	case abi.Uint8:
		return *(*uint8)(a) < *(*uint8)(b)
	case abi.Uint16:
		return *(*uint16)(a) < *(*uint16)(b)
	case abi.Uint32:
		return *(*uint32)(a) < *(*uint32)(b)
	case abi.Uint64:
		return *(*uint64)(a) < *(*uint64)(b)
	}
	panic("isolate: unsupported map key kind")
}
