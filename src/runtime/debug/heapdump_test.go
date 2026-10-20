// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package debug_test

import (
	"bytes"
	"os"
	"reflect"
	"runtime"
	. "runtime/debug"
	"testing"
)

func TestWriteHeapDumpNonempty(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skipf("WriteHeapDump is not available on %s.", runtime.GOOS)
	}
	f, err := os.CreateTemp("", "heapdumptest")
	if err != nil {
		t.Fatalf("TempFile failed: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	WriteHeapDump(f.Fd())
	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	const minSize = 1
	if size := fi.Size(); size < minSize {
		t.Fatalf("Heap dump size %d bytes, expected at least %d bytes", size, minSize)
	}
}

type Obj struct {
	x, y int
}

func objfin(x *Obj) {
	//println("finalized", x)
}

func TestWriteHeapDumpFinalizers(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skipf("WriteHeapDump is not available on %s.", runtime.GOOS)
	}
	f, err := os.CreateTemp("", "heapdumptest")
	if err != nil {
		t.Fatalf("TempFile failed: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	// bug 9172: WriteHeapDump couldn't handle more than one finalizer
	println("allocating objects")
	x := &Obj{}
	runtime.SetFinalizer(x, objfin)
	y := &Obj{}
	runtime.SetFinalizer(y, objfin)

	// Trigger collection of x and y, queueing of their finalizers.
	println("starting gc")
	runtime.GC()

	// Make sure WriteHeapDump doesn't fail with multiple queued finalizers.
	println("starting dump")
	WriteHeapDump(f.Fd())
	println("done dump")
}

type G[T any] struct{}
type I interface {
	M()
}

//go:noinline
func (g G[T]) M() {}

var dummy I = G[int]{}
var dummy2 I = G[G[int]]{}

func TestWriteHeapDumpTypeName(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skipf("WriteHeapDump is not available on %s.", runtime.GOOS)
	}
	f, err := os.CreateTemp("", "heapdumptest")
	if err != nil {
		t.Fatalf("TempFile failed: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	WriteHeapDump(f.Fd())
	dummy.M()
	dummy2.M()
}

func TestWriteHeapDumpReflectType(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skipf("WriteHeapDump is not available on %s.", runtime.GOOS)
	}
	// Converting a reflected type with an embedded method to I creates a runtime
	// itab. Dumping it resolves a heap-allocated type name through reflectOffs,
	// including lock annotations and map reads in race builds, on the system stack.
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "G", Type: reflect.TypeFor[G[int]](), Anonymous: true},
		{Name: "Value", Type: reflect.TypeFor[string](), Tag: `json:"heapdump-reflect-type"`},
	})
	value := reflect.New(typ).Elem().Interface().(I)
	name := typ.String()
	f, err := os.CreateTemp(t.TempDir(), "heapdump")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	WriteHeapDump(f.Fd())
	value.M()
	runtime.KeepAlive(value)
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(name)) {
		t.Fatalf("heap dump is missing reflected type %q", name)
	}
}
