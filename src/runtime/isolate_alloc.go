// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/sys"
	"unsafe"
)

// An independent, acyclic lifetime handle lets GC retire the non-GC cache even
// when a group and its transport form a cycle. The padding avoids host tiny
// packing: sharing a block with another live object could retain the finalizer.
type isolateAllocHandle struct {
	cache *mcache
	_     [2]uintptr
}

var isolateAllocRegistry struct {
	lock mutex
	head *mcache
}

func newIsolateAllocHandle() *isolateAllocHandle {
	c := allocmcache()
	lockInit(&c.isolateLock, lockRankIsolateAlloc)
	handle := &isolateAllocHandle{cache: c}
	setFinalizer(handle, retireIsolateAllocCache, sys.GetCallerPC())
	systemstack(func() {
		lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
		// Creation can be preempted through several GC cycles before registration.
		// The new cache is empty; establish its generation at publication.
		c.flushGen.Store(mheap_.sweepgen)
		c.isolateNext = isolateAllocRegistry.head
		isolateAllocRegistry.head = c
		unlock(&isolateAllocRegistry.lock)
	})
	return handle
}

func retireIsolateAllocCache(handle *isolateAllocHandle) {
	c := handle.cache
	resources := c.isolateResources
	systemstack(func() {
		lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
		link := &isolateAllocRegistry.head
		for *link != c {
			if *link == nil {
				throw("isolate: allocator cache missing from registry")
			}
			link = &(*link).isolateNext
		}
		*link = c.isolateNext
		c.isolateNext = nil
		// No member can use this cache: every live member keeps its group and
		// handle reachable. Published objects remain under the ordinary GC.
		freemcache(c)
		unlock(&isolateAllocRegistry.lock)
	})
	handle.cache = nil
	if resources != nil {
		resources.removeMetadata(uint64(unsafe.Sizeof(mcache{})))
		resources.release()
	}
}

// The caller holds its M and drops the cache lock before releasing that M or
// assisting/starting GC. Legacy concurrent groups need the lock too; the
// deterministic dispatch token alone is not an allocator synchronization rule.
func acquireIsolateAllocCache(mp *m) *mcache {
	gp := getg()
	if gp.isolateOwner == 0 || gp.isolateGroup == nil {
		return getMCache(mp)
	}
	c := gp.isolateGroup.alloc.cache
	lock(&c.isolateLock)
	if c.isolateOwner != gp.isolateOwner {
		throw("isolate: allocation owner mismatch")
	}
	c.prepareForSweep()
	return c
}

func releaseIsolateAllocCache(c *mcache) {
	if c.isolateOwner != 0 {
		unlock(&c.isolateLock)
	}
}

// GC and ReadMemStats visit caches while stopped. The registry only contains
// NotInHeap metadata and therefore does not keep any instance or heap alive.
func isolateVisitAllocCaches(visit func(*mcache)) {
	assertWorldStopped()
	lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
	for c := isolateAllocRegistry.head; c != nil; c = c.isolateNext {
		visit(c)
	}
	unlock(&isolateAllocRegistry.lock)
}

// isolateAllocCacheCount is an internal diagnostic for lifecycle stress tests.
// Counting the non-GC registry detects cache leaks that live-heap measurements
// alone cannot see. The registry lock also serializes retirement.
//
//go:linkname isolateAllocCacheCount
func isolateAllocCacheCount() int {
	count := 0
	systemstack(func() {
		lockWithRank(&isolateAllocRegistry.lock, lockRankIsolateAllocRegistry)
		for c := isolateAllocRegistry.head; c != nil; c = c.isolateNext {
			count++
		}
		unlock(&isolateAllocRegistry.lock)
	})
	return count
}
