// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"isolate"
	"runtime"
)

func main() {
	const count = 10000
	program, ok := isolate.LookupProgram("program")
	if !ok {
		panic("missing program")
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	instances := make([]*isolate.Isolate, count)
	for index := range instances {
		instance, err := isolate.New(isolate.Config{Program: program})
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
