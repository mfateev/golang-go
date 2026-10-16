// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"isolate"
	"os"
	"runtime"

	"example.org/isolate-layout-measure/empty"
	"example.org/isolate-layout-measure/withjson"
	"example.org/isolate-layout-measure/withjsontext"
	"example.org/isolate-layout-measure/withreflect"
	"example.org/isolate-layout-measure/withtime"
	"example.org/isolate-layout-measure/withv2"
)

func dispatch(handle isolate.Handle) {
	if err := handle.Invoke(func(...isolate.Value) error { return nil }, func(...isolate.Value) error { return nil }); err != nil {
		panic(err)
	}
}

func program(name string) isolate.Program {
	var fn any
	switch name {
	case "empty":
		fn = empty.Entry
	case "withtime":
		fn = withtime.Entry
	case "withreflect":
		fn = withreflect.Entry
	case "withjsontext":
		fn = withjsontext.Entry
	case "withv2":
		fn = withv2.Entry
	case "withjson":
		fn = withjson.Entry
	default:
		panic("unknown variant " + name)
	}
	handle, ok := isolate.LookupFunction(fn)
	if !ok {
		panic("missing workflow " + name)
	}
	return handle.ProgramWithHandle(dispatch)
}

func main() {
	const count = 10000
	if len(os.Args) != 2 {
		panic("pass one variant name")
	}
	selected := program(os.Args[1])
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	instances := make([]*isolate.Isolate, count)
	for index := range instances {
		instance, err := isolate.New(isolate.Config{Program: selected})
		if err != nil {
			panic(err)
		}
		instances[index] = instance
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	fmt.Printf("count=%d retained=%d bytes_per=%.1f objects=%d alloc=%d alloc_per=%.1f\n",
		count,
		after.HeapAlloc-before.HeapAlloc,
		float64(after.HeapAlloc-before.HeapAlloc)/count,
		after.HeapObjects-before.HeapObjects,
		after.TotalAlloc-before.TotalAlloc,
		float64(after.TotalAlloc-before.TotalAlloc)/count,
	)
	runtime.KeepAlive(instances)
}
