// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"crypto/sha256"
	"errors"
	"internal/isolatebridge"
	"maps"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// These privileged fixtures inject foreign objects directly. Real SDK inputs
// use copied bytes; the library checks must also cover indirect method calls.
func TestOwnershipFaultLibraryOperations(t *testing.T) {
	foreign := new(int)
	foreignScalar := new(int64)
	*foreign = 7
	foreignMap := map[string]*int{"value": foreign}
	foreignBytes := []byte{7, 8, 9}
	foreignDigest := sha256.New()
	foreignChan := make(chan int, 1)
	foreignChan <- 7
	var processValue atomic.Value
	processValue.Store(foreign)
	type large struct {
		Padding [32]uintptr
		Pointer *int
	}
	cases := []struct {
		name, reason string
		action       func()
	}{
		{"sha256 input", "isolate: read from foreign heap", func() { _ = sha256.Sum256(foreignBytes) }},
		{"sha256 receiver", "isolate: write to foreign heap", func() { _, _ = foreignDigest.Write([]byte{1}) }},
		{"reflect atomic load", "isolate: read from foreign heap", func() { reflect.ValueOf(atomic.LoadInt64).Call([]reflect.Value{reflect.ValueOf(foreignScalar)}) }},
		{"reflect atomic store", "isolate: write to foreign heap", func() {
			reflect.ValueOf(atomic.StoreInt64).Call([]reflect.Value{reflect.ValueOf(foreignScalar), reflect.ValueOf(int64(9))})
		}},
		{"reflect atomic publication", "isolate: foreign heap reference publication", func() {
			slot := new(unsafe.Pointer)
			reflect.ValueOf(atomic.StorePointer).Call([]reflect.Value{reflect.ValueOf(slot), reflect.ValueOf(unsafe.Pointer(foreign))})
		}},
		{"reflect bulk write", "isolate: write to foreign heap", func() { reflect.ValueOf(&foreignBytes[0]).Elem().Set(reflect.ValueOf(byte(11))) }},
		{"reflect copy read", "isolate: read from foreign heap", func() { reflect.Copy(reflect.ValueOf(make([]byte, 3)), reflect.ValueOf(foreignBytes)) }},
		{"reflect copy write", "isolate: write to foreign heap", func() { reflect.Copy(reflect.ValueOf(foreignBytes), reflect.ValueOf([]byte{11, 12})) }},
		{"reflect map read", "isolate: map read crosses owner boundary", func() { _ = reflect.ValueOf(foreignMap).MapIndex(reflect.ValueOf("absent")) }},
		{"reflect map write", "isolate: map write crosses owner boundary", func() { reflect.ValueOf(foreignMap).SetMapIndex(reflect.ValueOf("new"), reflect.ValueOf(new(int))) }},
		{"reflect map value publication", "isolate: foreign heap reference publication", func() {
			m := make(map[string]*int)
			reflect.ValueOf(m).SetMapIndex(reflect.ValueOf("key"), reflect.ValueOf(foreign))
		}},
		{"reflect channel receive", "isolate: write to foreign heap", func() { _, _ = reflect.ValueOf(foreignChan).Recv() }},
		{"reflect select", "isolate: write to foreign heap", func() {
			_, _, _ = reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(foreignChan)}})
		}},
		{"clone process", "isolate: map clone crosses owner boundary", func() { _ = maps.Clone(foreignMap) }},
		{"clone corrupt direct", "isolate: foreign heap reference publication", func() { _ = maps.Clone(map[int]*int{1: foreign}) }},
		{"clone corrupt indirect key", "isolate: foreign heap reference publication", func() { _ = maps.Clone(map[large]int{{Pointer: foreign}: 1}) }},
		{"clone corrupt indirect value", "isolate: foreign heap reference publication", func() { _ = maps.Clone(map[int]large{1: {Pointer: foreign}}) }},
		{"value method load", "isolate: read from foreign heap", func() { load := processValue.Load; _ = load() }},
		{"value method store", "isolate: write to foreign heap", func() { store := processValue.Store; store(1) }},
		{"value method swap", "isolate: write to foreign heap", func() { swap := processValue.Swap; _ = swap(1) }},
		{"value method cas", "isolate: write to foreign heap", func() { cas := processValue.CompareAndSwap; _ = cas(foreign, 1) }},
		{"value publish", "isolate: foreign heap reference publication", func() { v := new(atomic.Value); v.Store(foreign) }},
		{"value boxed publish", "isolate: foreign heap reference publication", func() { v := new(atomic.Value); v.Swap(large{Pointer: foreign}) }},
	}
	for _, deterministic := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				program := Program{entry: isolatebridge.ProgramEntry{
					NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
					Main:     func() { defer func() { _ = recover(); panic("ownership fault ran application defer") }(); tc.action() },
				}}
				i, err := New(Config{Program: program, Deterministic: deterministic})
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Start(); err != nil {
					t.Fatal(err)
				}
				var fault *OwnershipError
				if err := i.Wait(); !errors.As(err, &fault) || fault.Reason != tc.reason {
					t.Fatalf("fault = %v, want %q", err, tc.reason)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := i.Kill(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	if *foreign != 7 || processValue.Load() != foreign || foreignMap["value"] != foreign {
		t.Fatal("foreign state changed")
	}
}

func TestOwnershipFaultLibraryOwnedValues(t *testing.T) {
	foreign := new(int)
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			p := new(int)
			m := map[*int]*int{p: p}
			copy := maps.Clone(m)
			if copy[p] != p {
				panic("clone changed owned pointers")
			}
			v := new(atomic.Value)
			v.Store(p)
			if v.Load() != p || v.CompareAndSwap(foreign, p) || !v.CompareAndSwap(p, p) || v.Swap(p) != p {
				panic("owned value semantics changed")
			}
		},
	}}
	for _, deterministic := range []bool{false, true} {
		i, err := New(Config{Program: program, Deterministic: deterministic})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		if err := i.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}
