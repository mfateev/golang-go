// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"errors"
	"unsafe"
)

// ResourceStats is copied process data. Quiescent snapshots are exact;
// counters changing concurrently are sampled independently.
type ResourceStats struct {
	MemoryBytes, PeakMemoryBytes                           uint64
	HeapBytes, ReservedHeapBytes, StackBytes, RuntimeBytes uint64
	TotalAllocatedBytes                                    uint64
	LiveGoroutines, PeakGoroutines                         uint32
	// RunningNanoseconds counts completed scheduled intervals, including
	// metadata work and syscalls. It is not hardware CPU time or a replay input.
	RunningNanoseconds, Progress uint64
}

func (b *Boundary) ConfigureResources(memory uint64, goroutines uint32) error {
	if !configureResources(b.group, memory, goroutines) {
		return errors.New("isolate: cannot configure resources after attaching goroutines")
	}
	return nil
}

func (b *Boundary) Resources() (stats ResourceStats) {
	stats.MemoryBytes, stats.PeakMemoryBytes, stats.HeapBytes, stats.ReservedHeapBytes, stats.StackBytes, stats.RuntimeBytes, stats.TotalAllocatedBytes,
		stats.LiveGoroutines, stats.PeakGoroutines, stats.RunningNanoseconds, stats.Progress = readResources(b.group)
	return
}

func (b *Boundary) ResourceFaultDetails() (kind uint32, limit, usage uint64) {
	return resourceFaultDetails(b.group)
}

func (b *Boundary) FailResource(kind uint32, limit, usage uint64) {
	failResource(b.group, kind, limit, usage)
}

//go:linkname configureResources runtime.isolateConfigureResources
func configureResources(p unsafe.Pointer, memory uint64, goroutines uint32) bool

//go:linkname readResources runtime.isolateReadResources
func readResources(p unsafe.Pointer) (memory, peak, heap, reserved, stacks, metadata, allocated uint64, goroutines, peakGoroutines uint32, running, progress uint64)

//go:linkname resourceFaultDetails runtime.isolateResourceFaultDetails
func resourceFaultDetails(p unsafe.Pointer) (kind uint32, limit, usage uint64)

//go:linkname failResource runtime.isolateFailResource
func failResource(p unsafe.Pointer, kind uint32, limit, usage uint64)

func (b *Boundary) ResourceFaultPCs() ([32]uintptr, int) { return resourceFaultPCs(b.group) }

//go:linkname resourceFaultPCs runtime.isolateResourceFaultPCs
func resourceFaultPCs(p unsafe.Pointer) ([32]uintptr, int)
