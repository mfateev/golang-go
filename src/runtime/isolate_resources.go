// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/runtime/atomic"
	"internal/runtime/gc"
	"internal/runtime/sys"
	"unsafe"
)

const (
	isolateResourceMemory = iota + 1
	isolateResourceGoroutines
	isolateResourceTaskDuration
	isolateResourceProgress
)

// Spans and caches are not GC roots. This pointer-free, reference-counted
// record can outlive the group without retaining its private heap. The host
// handle, allocator cache and every owned span hold independent references.
type isolateResourceAccount struct {
	_              sys.NotInHeap
	refs           atomic.Int64
	maxMemory      uint64 // Immutable once members are attached.
	maxGoroutines  uint32
	memory         atomic.Uint64
	peakMemory     atomic.Uint64
	heap           atomic.Uint64
	reserved       atomic.Uint64
	stacks         atomic.Uint64
	metadata       atomic.Uint64
	totalAllocated atomic.Uint64
	goroutines     atomic.Uint32
	peakGoroutines atomic.Uint32
	runningNS      atomic.Uint64
	progress       atomic.Uint64
	violation      atomic.Uint32 // 0: none, ^uint32(0): publishing, otherwise resource kind.
	violationLimit uint64
	violationUsage uint64
	baseBytes      uint64
	pcs            [32]uintptr
	pcCount        int
}

type isolateResourceHandle struct {
	account *isolateResourceAccount
	_       [2]uintptr // Do not share a tiny block with another finalizable object.
}

var isolateResourceAllocator fixalloc // Protected by mheap_.lock.
var isolateResourceAccounts atomic.Int64

func newIsolateResources() *isolateResourceHandle {
	var r *isolateResourceAccount
	systemstack(func() {
		lock(&mheap_.lock)
		if isolateResourceAllocator.size == 0 {
			isolateResourceAllocator.init(unsafe.Sizeof(isolateResourceAccount{}), nil, nil, &memstats.other_sys)
		}
		r = (*isolateResourceAccount)(isolateResourceAllocator.alloc())
		unlock(&mheap_.lock)
	})
	r.refs.Store(1)
	r.baseBytes = uint64(unsafe.Sizeof(isolateRevocationGroup{}) + unsafe.Sizeof(isolateResourceHandle{}) + unsafe.Sizeof(isolateAllocHandle{}) + unsafe.Sizeof(isolateResourceAccount{}) + unsafe.Sizeof(isolateOwnershipFault{}))
	r.metadata.Store(r.baseBytes)
	r.memory.Store(r.baseBytes)
	r.peakMemory.Store(r.baseBytes)
	isolateResourceAccounts.Add(1)
	h := &isolateResourceHandle{account: r}
	setFinalizer(h, retireIsolateResources, sys.GetCallerPC())
	return h
}

func retireIsolateResources(h *isolateResourceHandle) { h.account.release(); h.account = nil }

func (r *isolateResourceAccount) release() {
	if r.refs.Add(-1) == 0 {
		systemstack(func() {
			lock(&mheap_.lock)
			isolateResourceAllocator.free(unsafe.Pointer(r))
			isolateResourceAccounts.Add(-1)
			unlock(&mheap_.lock)
		})
	}
}

// The last span may release the last reference while the heap lock is held.
func (r *isolateResourceAccount) releaseLocked() {
	if r.refs.Add(-1) == 0 {
		isolateResourceAllocator.free(unsafe.Pointer(r))
		isolateResourceAccounts.Add(-1)
	}
}

//go:nosplit
func isolateResourcePeak(peak *atomic.Uint64, value uint64) {
	for old := peak.Load(); value > old; old = peak.Load() {
		if peak.CompareAndSwap(old, value) {
			break
		}
	}
}

// Reserve before asking malloc for storage, including rounding and headers.
// Collection refunds only actual swept slots, never a pending reservation.
//
//go:nosplit
func (r *isolateResourceAccount) reserve(bytes uint64) bool {
	for {
		old := r.memory.Load()
		if bytes > ^uint64(0)-old || r.maxMemory != 0 && (old > r.maxMemory || bytes > r.maxMemory-old) {
			return false
		}
		if r.memory.CompareAndSwap(old, old+bytes) {
			isolateResourcePeak(&r.peakMemory, old+bytes)
			return true
		}
	}
}

func (r *isolateResourceAccount) addMetadata(bytes uint64) {
	r.metadata.Add(int64(bytes))
	isolateResourcePeak(&r.peakMemory, r.memory.Add(int64(bytes)))
}

func (r *isolateResourceAccount) removeMetadata(bytes uint64) {
	r.metadata.Add(-int64(bytes))
	r.memory.Add(-int64(bytes))
}

func isolateSpanResources(s *mspan, r *isolateResourceAccount) {
	if s.isolateResources == r {
		return
	}
	if old := s.isolateResources; old != nil {
		old.reserved.Add(-int64(s.npages * pageSize))
		old.removeMetadata(uint64(unsafe.Sizeof(mspan{})))
		old.release()
	}
	s.isolateResources = r
	if r != nil {
		r.refs.Add(1)
		r.reserved.Add(int64(s.npages * pageSize))
		r.addMetadata(uint64(unsafe.Sizeof(mspan{})))
	}
}

// Called by freeSpanLocked; the span no longer has live objects or cache users.
func isolateReleaseSpanResources(s *mspan) {
	if r := s.isolateResources; r != nil {
		s.isolateResources = nil
		r.reserved.Add(-int64(s.npages * pageSize))
		r.removeMetadata(uint64(unsafe.Sizeof(mspan{})))
		r.releaseLocked()
	}
}

func isolateReserveHeap(size uintptr, typ *_type) {
	gp := getg()
	if gp.isolateOwner == 0 || gp.isolateGroup == nil {
		return
	}
	if isolateResourceCanDiscard(gp) {
		isolateDiscardIfRevoked()
	}
	group := gp.isolateGroup
	r := group.resources.account
	noscan := typ == nil || !typ.Pointers()
	bytes := roundupsize(size, noscan)
	if !noscan && size <= maxSmallSize-gc.MallocHeaderSize && !heapBitsInSpan(size) {
		bytes += gc.MallocHeaderSize
	}
	if !isolateChargeHeapReservation(gp, uint64(bytes), false) {
		isolateResourceViolation(group, isolateResourceMemory, r.maxMemory, r.memory.Load()+uint64(bytes))
		if isolateResourceCanDiscard(gp) {
			isolateNotifyResourceViolation(group)
			isolateDiscardIfRevoked()
			throw("isolate: resource allocation rejection returned")
		}
		// Internal allocation must finish its pinned wait/service cleanup before
		// discard. Account the temporary excess and keep the durable fence.
		isolateChargeHeapReservation(gp, uint64(bytes), true)
	}
}

// The reservation must remain attached to its G across GC assists and stack
// growth. Discard before malloc materializes the slot refunds this credit;
// normal allocation clears it while mallocing still prevents stack discard.
//
//go:nosplit
func isolateChargeHeapReservation(gp *g, bytes uint64, excess bool) bool {
	r := gp.isolateGroup.resources.account
	if gp.isolateHeapReservation != 0 {
		throw("isolate: nested private heap reservation")
	}
	if !excess && !r.reserve(bytes) {
		return false
	}
	if excess {
		isolateResourcePeak(&r.peakMemory, r.memory.Add(int64(bytes)))
	}
	gp.isolateHeapReservation = bytes
	r.heap.Add(int64(bytes))
	r.totalAllocated.Add(int64(bytes))
	return true
}

//go:nosplit
func isolateCompleteHeapReservation() { getg().isolateHeapReservation = 0 }

// This path performs no allocation or application callback. In particular it
// is safe during stack growth on g0. Notification is deferred to a safe worker.
func isolateResourceViolation(group *isolateRevocationGroup, kind uint32, limit, usage uint64) {
	r := group.resources.account
	if r.violation.CompareAndSwap(0, ^uint32(0)) {
		r.violationLimit, r.violationUsage = limit, usage
		// Save numeric PCs without allocating while we still own the stack.
		// Formatting happens later on the host, after fault publication.
		systemstack(func() {
			gp := getg().m.curg
			if gp != nil && gp.isolateGroup == group {
				r.pcCount = gcallers(gp, 0, r.pcs[:])
			}
		})
		if raceenabled {
			gp := getg()
			if gp == gp.m.g0 && gp.m.curg != nil {
				gp = gp.m.curg
			}
			// park_m can notify after dropg, so goroutine creation cannot inherit
			// the publishing G's race clock. Pair explicitly with the reporter.
			racereleaseg(gp, unsafe.Pointer(&group.resourceNotified))
		}
		r.violation.Store(kind)
		// The value is already strongly reachable through group.resourceFault;
		// this additional alias does not need a write barrier on g0.
		group.ownershipFault.CompareAndSwapNoWB(nil, group.resourceFault)
	}
	group.markRevoked()
}

// Called without allocator, stack or service locks. Report only once; a host
// can already read the immutable resource diagnostic before notification.
func isolateNotifyResourceViolation(group *isolateRevocationGroup) {
	if fault := group.ownershipFault.Load(); fault == group.resourceFault && group.resourceNotified.CompareAndSwap(0, 1) {
		gp := getg()
		owner, depth := gp.isolateOwner, gp.isolateMetadataDepth
		gp.isolateOwner, gp.isolateMetadataDepth = 0, depth+1
		go isolateReportOwnershipFault(group, fault)
		gp.isolateOwner, gp.isolateMetadataDepth = owner, depth
	}
}

func isolateCheckResourceMemory(group *isolateRevocationGroup) {
	r := group.resources.account
	if usage := r.memory.Load(); r.maxMemory != 0 && usage > r.maxMemory {
		isolateResourceViolation(group, isolateResourceMemory, r.maxMemory, usage)
	}
}

func isolateCheckAllocationResources() {
	gp := getg()
	if gp.isolateOwner != 0 && gp.isolateGroup != nil && isolateResourceCanDiscard(gp) {
		isolateCheckResourceMemory(gp.isolateGroup)
		isolateNotifyResourceViolation(gp.isolateGroup)
		isolateDiscardIfRevoked()
	}
}

func isolateAdmitResourceChild(group *isolateRevocationGroup) {
	r := group.resources.account
	for {
		old := r.goroutines.Load()
		if old == ^uint32(0) || r.maxGoroutines != 0 && old >= r.maxGoroutines {
			isolateResourceViolation(group, isolateResourceGoroutines, uint64(r.maxGoroutines), uint64(old)+1)
			isolateNotifyResourceViolation(group)
			isolateDiscardIfRevoked()
			throw("isolate: goroutine rejection returned")
		}
		if r.goroutines.CompareAndSwap(old, old+1) {
			break
		}
	}
	isolatePeakResourceGoroutines(r)
}

func isolateResourceCanDiscard(gp *g) bool {
	return gp.m.locks == 0 && gp.m.mallocing == 0 && gp.isolateMetadataDepth == 0 && gp.waiting == nil && gp.isolateParkState == 0 && gp.isolatePollDesc == nil
}

func isolateAttachChildResourceStack(gp *g) {
	r := gp.isolateGroup.resources.account
	bytes := uint64(gp.stack.hi - gp.stack.lo)
	r.stacks.Add(int64(bytes))
	isolateResourcePeak(&r.peakMemory, r.memory.Add(int64(bytes)))
	r.addMetadata(uint64(unsafe.Sizeof(g{})))
	isolateCheckResourceMemory(gp.isolateGroup)
}

func isolatePeakResourceGoroutines(r *isolateResourceAccount) {
	value := r.goroutines.Load()
	for old := r.peakGoroutines.Load(); value > old; old = r.peakGoroutines.Load() {
		if r.peakGoroutines.CompareAndSwap(old, value) {
			break
		}
	}
}

func isolateAttachResources(gp *g) {
	r := gp.isolateGroup.resources.account
	live := r.goroutines.Add(1)
	isolatePeakResourceGoroutines(r)
	bytes := uint64(gp.stack.hi - gp.stack.lo)
	r.stacks.Add(int64(bytes))
	isolateResourcePeak(&r.peakMemory, r.memory.Add(int64(bytes)))
	r.addMetadata(uint64(unsafe.Sizeof(g{})))
	isolateResourceRunStart(gp)
	if r.maxGoroutines != 0 && live > r.maxGoroutines {
		isolateResourceViolation(gp.isolateGroup, isolateResourceGoroutines, uint64(r.maxGoroutines), uint64(live))
	}
	isolateCheckResourceMemory(gp.isolateGroup)
}

func isolateDetachResources(gp *g) {
	r := gp.isolateGroup.resources.account
	isolateResourceRunStop(gp)
	if bytes := gp.isolateHeapReservation; bytes != 0 {
		r.heap.Add(-int64(bytes))
		r.memory.Add(-int64(bytes))
		gp.isolateHeapReservation = 0
	}
	bytes := uint64(gp.stack.hi - gp.stack.lo)
	r.stacks.Add(-int64(bytes))
	r.memory.Add(-int64(bytes))
	r.goroutines.Add(-1)
	r.removeMetadata(uint64(unsafe.Sizeof(g{})))
}

// Stack copying must finish before the member can be discarded. Charge growth
// on g0 and publish revocation; schedule notification after copying is safe.
func isolateResizeResourceStack(gp *g, old, new uintptr) {
	if gp.isolateGroup == nil {
		return
	}
	r := gp.isolateGroup.resources.account
	delta := uint64(new) - uint64(old)
	r.stacks.Add(int64(delta))
	value := r.memory.Add(int64(delta))
	if new > old {
		isolateResourcePeak(&r.peakMemory, value)
		isolateCheckResourceMemory(gp.isolateGroup)
	}
}

//go:nosplit
func isolateResourceRunStart(gp *g) {
	if gp.isolateResourceRunStart == 0 {
		gp.isolateResourceRunStart = nanotime()
	}
}

//go:nosplit
func isolateResourceRunStop(gp *g) {
	if start := gp.isolateResourceRunStart; start != 0 {
		gp.isolateGroup.resources.account.runningNS.Add(nanotime() - start)
		gp.isolateResourceRunStart = 0
	}
}

//go:linkname isolateConfigureResources
func isolateConfigureResources(p unsafe.Pointer, memory uint64, goroutines uint32) bool {
	if isolateActive() {
		isolateRejectEffect("configure isolate resources")
	}
	group := (*isolateRevocationGroup)(p)
	if group.live.Load() != 0 {
		return false
	}
	r := group.resources.account
	r.maxMemory, r.maxGoroutines = memory, goroutines
	return true
}

// Return POD fields across the internal host ABI. Quiescent snapshots are exact;
// concurrently changing counters are independently sampled, never STW.
//
//go:linkname isolateReadResources
func isolateReadResources(p unsafe.Pointer) (memory, peak, heap, reserved, stacks, metadata, allocated uint64, goroutines, peakGoroutines uint32, running, progress uint64) {
	if isolateActive() && getg().isolateMetadataDepth == 0 {
		isolateRejectEffect("read isolate resources")
	}
	group := (*isolateRevocationGroup)(p)
	r := group.resources.account
	memory, peak, heap, reserved, stacks, metadata, allocated = r.memory.Load(), r.peakMemory.Load(), r.heap.Load(), r.reserved.Load(), r.stacks.Load(), r.metadata.Load(), r.totalAllocated.Load()
	goroutines, peakGoroutines, running, progress = r.goroutines.Load(), r.peakGoroutines.Load(), r.runningNS.Load(), r.progress.Load()
	KeepAlive(group.resources)
	return
}

//go:linkname isolateResourceFaultDetails
func isolateResourceFaultDetails(p unsafe.Pointer) (kind uint32, limit, usage uint64) {
	group := (*isolateRevocationGroup)(p)
	if group.ownershipFault.Load() != group.resourceFault {
		return
	}
	r := group.resources.account
	kind, limit, usage = r.violation.Load(), r.violationLimit, r.violationUsage
	KeepAlive(group.resources)
	return
}

// Host watchdogs share the native immutable fault and revocation fence.
//
//go:linkname isolateFailResource
func isolateFailResource(p unsafe.Pointer, kind uint32, limit, usage uint64) {
	if isolateActive() {
		isolateRejectEffect("host resource watchdog")
	}
	if kind != isolateResourceTaskDuration && kind != isolateResourceProgress {
		panic("isolate: invalid watchdog resource")
	}
	group := (*isolateRevocationGroup)(p)
	isolateResourceViolation(group, kind, limit, usage)
	isolateNotifyResourceViolation(group)
}

//go:linkname isolateResourceAccountCount
func isolateResourceAccountCount() int64 { return isolateResourceAccounts.Load() }

//go:linkname isolateResourceFaultPCs
func isolateResourceFaultPCs(p unsafe.Pointer) (pcs [32]uintptr, count int) {
	if isolateActive() && getg().isolateMetadataDepth == 0 {
		isolateRejectEffect("read isolate resource diagnostics")
	}
	group := (*isolateRevocationGroup)(p)
	r := group.resources.account
	if kind := r.violation.Load(); kind != 0 && kind != ^uint32(0) {
		pcs, count = r.pcs, r.pcCount
	}
	KeepAlive(group.resources)
	return
}
