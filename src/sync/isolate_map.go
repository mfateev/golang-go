// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sync

import (
	"internal/abi"
	"unsafe"
)

func (m *Map) isolateRange(f func(key, value any) bool) {
	// The hash trie remains optimized for lookup. Only the observable range
	// order changes: snapshot keys of one supported concrete type, sort them,
	// then load current values so callback deletes, updates, and Clear work.
	var keys []any
	var typ *abi.Type
	m.m.Range(func(key, _ any) bool {
		t := abi.TypeOf(key)
		if t == nil {
			panic("isolate: deterministic sync.Map.Range requires one integer or string key type")
		}
		switch t.Kind() {
		case abi.Int, abi.Int8, abi.Int16, abi.Int32, abi.Int64,
			abi.Uint, abi.Uint8, abi.Uint16, abi.Uint32, abi.Uint64, abi.String:
		default:
			panic("isolate: deterministic sync.Map.Range requires one integer or string key type")
		}
		if typ == nil {
			typ = t
		} else if typ != t {
			panic("isolate: deterministic sync.Map.Range requires one integer or string key type")
		}
		keys = append(keys, key)
		return true
	})
	if len(keys) == 0 {
		return
	}
	kind := typ.Kind()
	less := func(a, b any) bool {
		return isolateMapCompare(kind, (*abi.EmptyInterface)(unsafe.Pointer(&a)).Data, (*abi.EmptyInterface)(unsafe.Pointer(&b)).Data) < 0
	}
	// Heap sort keeps sync below iter/slices and reflection in Go's dependency
	// layering. No callback or user synchronization occurs during the snapshot.
	sift := func(root, end int) {
		for {
			child := root*2 + 1
			if child >= end {
				return
			}
			if child+1 < end && less(keys[child], keys[child+1]) {
				child++
			}
			if !less(keys[root], keys[child]) {
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
	for _, key := range keys {
		if value, ok := m.Load(key); ok && !f(key, value) {
			return
		}
	}
}

func isolateMapCompare(kind abi.Kind, a, b unsafe.Pointer) int {
	var ai, bi int64
	var au, bu uint64
	switch kind {
	case abi.String:
		as, bs := *(*string)(a), *(*string)(b)
		if as < bs {
			return -1
		}
		if as > bs {
			return 1
		}
		return 0
	case abi.Int:
		ai, bi = int64(*(*int)(a)), int64(*(*int)(b))
	case abi.Int8:
		ai, bi = int64(*(*int8)(a)), int64(*(*int8)(b))
	case abi.Int16:
		ai, bi = int64(*(*int16)(a)), int64(*(*int16)(b))
	case abi.Int32:
		ai, bi = int64(*(*int32)(a)), int64(*(*int32)(b))
	case abi.Int64:
		ai, bi = *(*int64)(a), *(*int64)(b)
	case abi.Uint:
		au, bu = uint64(*(*uint)(a)), uint64(*(*uint)(b))
	case abi.Uint8:
		au, bu = uint64(*(*uint8)(a)), uint64(*(*uint8)(b))
	case abi.Uint16:
		au, bu = uint64(*(*uint16)(a)), uint64(*(*uint16)(b))
	case abi.Uint32:
		au, bu = uint64(*(*uint32)(a)), uint64(*(*uint32)(b))
	case abi.Uint64:
		au, bu = *(*uint64)(a), *(*uint64)(b)
	}
	if ai < bi || au < bu {
		return -1
	}
	if ai > bi || au > bu {
		return 1
	}
	return 0
}
