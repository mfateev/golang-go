// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/goexperiment"
	"internal/runtime/atomic"
	"unsafe"
)

// Execution membership stays on g. These records select allocation/access and
// package state independently, without a foreign stack or a syscall transition.
type isolateHostContext struct {
	role         isolateExecutionRole
	scratch      *isolateScratchLifetime
	previous     *isolateHostContext
	construction *isolateConstruction
	state        *isolatePackageState
	owner        uintptr
	caller       guintptr
	goid         uint64
	application  bool
	active       bool
}

type isolateScratchLifetime struct{ closed atomic.Bool }

type isolateAllocator struct {
	group   *isolateRevocationGroup
	alloc   *isolateAllocHandle
	scratch *isolateScratchLifetime
	owner   uintptr
}

type isolateConstruction struct {
	previous    *isolateConstruction
	allocator   *isolateAllocator
	state       *isolatePackageState
	owner       uintptr
	caller      guintptr
	allocations *isolateConstructionAllocation
}

type isolateConstructionAllocation struct {
	next *isolateConstructionAllocation
	base unsafe.Pointer
	size uintptr
}

type isolateConstructionRevoked struct{}

//go:nosplit
func isolateHostExecution(gp *g) bool {
	return gp.isolateHost != nil && !gp.isolateHost.application
}

//go:nosplit
func isolateAllocationGroup(gp *g) *isolateRevocationGroup {
	if scope := gp.isolateConstruction; scope != nil && gp.isolateOwner != 0 {
		return scope.allocator.group
	}
	return gp.isolateGroup
}

//go:linkname isolateCheckHostRegistration
func isolateCheckHostRegistration(fn any) {
	if !goexperiment.Isolates {
		panic("isolate: requires GOEXPERIMENT=isolates")
	}
	gp := getg()
	if gp.isolateGroup != nil || gp.isolateOwner != 0 || gp.isolateConstruction != nil {
		panic("isolate: host functions must be registered by the host")
	}
	f := efaceOf(&fn)
	if f._type == nil || f._type.Kind() != abi.Func || f.data == nil {
		panic("isolate: nil host function")
	}
	if span := spanOfHeap(uintptr(f.data)); span != nil && span.isolateAllocOwner != 0 {
		panic("isolate: registration cannot promote a private closure")
	}
}

//go:linkname isolateEnterHost
func isolateEnterHost() unsafe.Pointer {
	gp := getg()
	if gp.isolateGroup == nil || gp.isolateOwner == 0 || isolateHostExecution(gp) || gp.isolateConstruction != nil || gp.isolateMetadataDepth != 0 {
		panic("isolate: host function requires an isolate caller")
	}
	isolateDiscardIfRevoked()
	owner, state := gp.isolateOwner, gp.isolateState
	// Allocate the control record under the process owner and mask revocation
	// until it is installed. No application code runs during this transition.
	gp.isolateMetadataDepth++
	gp.isolateOwner, gp.isolateState = 0, nil
	c := &isolateHostContext{previous: gp.isolateHost, construction: gp.isolateConstruction,
		role: isolateGetExecutionRole(gp), scratch: gp.isolateScratchLifetime, owner: owner, state: state, caller: guintptr(unsafe.Pointer(gp)), goid: gp.goid, active: true}
	gp.isolateHost = c
	gp.isolateMetadataDepth--
	gp.isolateGroup.resources.account.hostIntegration.Store(true)
	gp.isolateGroup.hostCalls.Add(1)
	return unsafe.Pointer(c)
}

func isolateValidateHostContext(p unsafe.Pointer) *isolateHostContext {
	c := (*isolateHostContext)(p)
	gp := getg()
	if c == nil || c.caller.ptr() != gp || c.goid != gp.goid || !c.active || gp.isolateHost != c || c.application {
		panic("isolate: invalid or expired HostContext")
	}
	return c
}

//go:linkname isolateLeaveHost
func isolateLeaveHost(p unsafe.Pointer) {
	c := isolateValidateHostContext(p)
	gp := getg()
	c.active = false
	gp.isolateOwner, gp.isolateState = c.owner, c.state
	gp.isolateConstruction = c.construction
	isolateSetExecutionRole(gp, c.role)
	gp.isolateHost = c.previous
	gp.isolateGroup.hostCalls.Add(-1)
	// Drop retained private state even if an application incorrectly saves c.
	c.state, c.construction, c.previous, c.scratch = nil, nil, nil, nil
	isolateDiscardIfRevoked()
}

//go:linkname isolateHostPanic
func isolateHostPanic(p unsafe.Pointer, value any) {
	isolateValidateHostContext(p)
	if getg().isolateGroup.ownershipFault.Load() == nil {
		if _, revoked := value.(isolateConstructionRevoked); !revoked {
			isolateReportPanic(value, "host operation")
		}
	}
	// The deferred Leave runs even if reporting panics during fatal unwinding.
}

//go:linkname isolateHostReenter
func isolateHostReenter(p unsafe.Pointer, fn func()) {
	c := isolateValidateHostContext(p)
	gp := getg()
	if gp.isolateGroup.admission.Load()&isolateRevokedBit != 0 {
		panic(isolateConstructionRevoked{})
	}
	scope := gp.isolateConstruction
	owner, state := gp.isolateOwner, gp.isolateState
	role := isolateGetExecutionRole(gp)
	depth, metadataOwner := gp.isolateMetadataDepth, gp.isolateMetadataOwner
	// Install restoration before changing context: even preparation can grow a
	// stack and observe a resource fault. No host metadata permission is inherited.
	defer func() {
		gp.isolateOwner, gp.isolateState = owner, state
		gp.isolateConstruction = scope
		isolateSetExecutionRole(gp, role)
		gp.isolateMetadataDepth, gp.isolateMetadataOwner = depth, metadataOwner
		c.application = false
	}()
	isolateSetExecutionRole(gp, c.role)
	c.application = true
	gp.isolateConstruction = nil
	gp.isolateOwner, gp.isolateState = c.owner, c.state
	gp.isolateMetadataDepth, gp.isolateMetadataOwner = 0, 0
	isolateDiscardIfRevoked()
	closure := *(*unsafe.Pointer)(unsafe.Pointer(&fn))
	isolateCheckHostClosure(closure)
	if closure != nil {
		// A runtime callback is not an audited application caller. In particular,
		// an assembly entry or unsafe reflection accessor gets no runtime authority.
		isolateCheckReflectEffectCall((*funcval)(closure).fn)
	}
	fn()
}

//go:linkname isolateHostContextAllocator
func isolateHostContextAllocator(p unsafe.Pointer) unsafe.Pointer {
	c := isolateValidateHostContext(p)
	gp := getg()
	scope, owner := gp.isolateConstruction, gp.isolateOwner
	gp.isolateConstruction, gp.isolateOwner = nil, 0
	defer func() { gp.isolateConstruction, gp.isolateOwner = scope, owner }()
	alloc := gp.isolateGroup.alloc
	var scratch *isolateScratchLifetime
	if c.role.readOnlyOwner != 0 {
		alloc = gp.isolateGroup.readOnlyAlloc
		scratch = c.scratch
	}
	return unsafe.Pointer(&isolateAllocator{group: gp.isolateGroup, alloc: alloc, scratch: scratch, owner: c.owner})
}

//go:linkname isolateGroupAllocator
func isolateGroupAllocator(p unsafe.Pointer, owner uintptr) unsafe.Pointer {
	if isolateActive() || getg().isolateConstruction != nil {
		panic("isolate: Allocator is a host operation")
	}
	group := (*isolateRevocationGroup)(p)
	// Serialize with final release, including a controller asking after completion.
	lockWithRank(&group.parkLock, lockRankIsolatePark)
	alloc := group.alloc
	unlock(&group.parkLock)
	if group.admission.Load()&isolateRevokedBit != 0 {
		alloc = nil
	}
	group.resources.account.hostIntegration.Store(true)
	return unsafe.Pointer(&isolateAllocator{group: group, alloc: alloc, owner: owner})
}

//go:linkname isolateHostAllocator
func isolateHostAllocator() unsafe.Pointer {
	if !goexperiment.Isolates {
		panic("isolate: requires GOEXPERIMENT=isolates")
	}
	gp := getg()
	if isolateActive() || gp.isolateMetadataDepth != 0 {
		panic("isolate: HostAllocator is a host operation")
	}
	scope, owner := gp.isolateConstruction, gp.isolateOwner
	gp.isolateConstruction, gp.isolateOwner = nil, 0
	defer func() { gp.isolateConstruction, gp.isolateOwner = scope, owner }()
	return unsafe.Pointer(new(isolateAllocator))
}

// Host work admission and final drain share dispatchLock. A lease cannot
// appear after a revoked group has reported zero and released its cache.
func isolateAcquireHostWork(group *isolateRevocationGroup) bool {
	lock(&group.dispatchLock)
	ok := group.admission.Load()&isolateRevokedBit == 0
	if ok {
		group.hostWork.Add(1)
	}
	unlock(&group.dispatchLock)
	return ok
}

func isolateReleaseHostWork(group *isolateRevocationGroup) {
	if raceenabled {
		racereleasemerge(unsafe.Pointer(group))
	}
	if group.hostWork.Add(-1) < 0 {
		throw("isolate: negative host work count")
	}
	group.notifyDrained()
}

//go:linkname isolateBeginConstruction
func isolateBeginConstruction(p unsafe.Pointer) unsafe.Pointer {
	gp := getg()
	if isolateActive() || gp.isolateMetadataDepth != 0 {
		panic("isolate: Allocate is a host operation")
	}
	a := (*isolateAllocator)(p)
	if a == nil {
		panic("isolate: invalid Allocator")
	}
	if a.group != nil {
		if !isolateAcquireHostWork(a.group) {
			return nil
		}
		if a.alloc == nil || a.scratch != nil && a.scratch.closed.Load() {
			isolateReleaseHostWork(a.group)
			return nil
		}
	}
	// Scope metadata is always host-owned, even for nested constructors.
	previous, owner := gp.isolateConstruction, gp.isolateOwner
	gp.isolateConstruction, gp.isolateOwner = nil, 0
	s := &isolateConstruction{previous: previous, allocator: a, owner: owner, state: gp.isolateState,
		caller: guintptr(unsafe.Pointer(gp))}
	gp.isolateConstruction = s
	gp.isolateOwner, gp.isolateState = a.owner, nil
	return unsafe.Pointer(s)
}

//go:linkname isolateEndConstruction
func isolateEndConstruction(p unsafe.Pointer) bool {
	s := (*isolateConstruction)(p)
	gp := getg()
	if s == nil || s.caller.ptr() != gp || gp.isolateConstruction != s {
		panic("isolate: invalid construction scope")
	}
	a := s.allocator
	gp.isolateConstruction = s.previous
	gp.isolateOwner, gp.isolateState = s.owner, s.state
	revoked := a.group != nil && (a.group.admission.Load()&isolateRevokedBit != 0 || a.scratch != nil && a.scratch.closed.Load())
	if a.group != nil {
		isolateReleaseHostWork(a.group)
	}
	return !revoked
}

// Record new extents, not their reachable graph. An allocator cannot authorize
// access to preexisting private objects. Go-visible pointers keep in-progress
// construction alive through concurrent GC.
func isolateRecordConstructionAllocation(p unsafe.Pointer, size uintptr) {
	gp := getg()
	scope := gp.isolateConstruction
	if scope == nil || gp.isolateOwner == 0 {
		return
	}
	owner := gp.isolateOwner
	gp.isolateConstruction, gp.isolateOwner = nil, 0
	gp.isolateMetadataDepth++
	node := &isolateConstructionAllocation{next: scope.allocations, base: p, size: size}
	scope.allocations = node
	gp.isolateMetadataDepth--
	gp.isolateConstruction, gp.isolateOwner = scope, owner
}

// Host operations borrow only their original domain. Constructors may also
// initialize new extents. Workflow borrowing remains read-only in queries.
func isolateHostAccess(p unsafe.Pointer, size uintptr, owner uintptr, write bool) (handled, allowed bool) {
	gp := getg()
	scope := gp.isolateConstruction
	if !isolateHostExecution(gp) && scope == nil {
		return false, false
	}
	if owner == 0 {
		return true, true
	}
	if uintptr(p) >= gp.stack.lo && uintptr(p)+size <= gp.stack.hi {
		return true, true
	}
	if c := gp.isolateHost; c != nil && !c.application {
		if owner == c.owner {
			return true, true
		}
		if owner == c.role.readOnlyOwner {
			return true, !write
		}
	}
	for scope != nil {
		if owner != scope.allocator.owner {
			scope = scope.previous
			continue
		}
		for node := scope.allocations; node != nil; node = node.next {
			start, addr := uintptr(node.base), uintptr(p)
			if addr >= start && addr-start < node.size && size <= node.size-(addr-start) {
				return true, true
			}
		}
		scope = scope.previous
	}
	return true, false
}

//go:linkname isolateCheckMapHostAccess
func isolateCheckMapHostAccess(p unsafe.Pointer, owner uintptr, write bool) bool {
	handled, allowed := isolateHostAccess(p, 1, owner, write)
	if handled && !allowed {
		if write && getg().isolateReadOnlyOwner != 0 && owner == getg().isolateReadOnlyOwner {
			panic("isolate: read-only handler cannot mutate workflow map")
		}
		isolateOwnershipViolation("isolate: host map access crosses owner boundary")
	}
	return handled
}

// A verified entry exposes only a callable code word, never its host captures.
func isolateHostEntry(p unsafe.Pointer) bool {
	if p == nil {
		return false
	}
	s := spanOfHeap(uintptr(p))
	if s == nil || s.isolateAllocOwner != 0 || isolateTypeObjectBase(p) != uintptr(p) {
		return false
	}
	fn := findfunc(*(*uintptr)(p))
	return fn.valid() && fn.flag&abi.FuncFlagIsolateHostEntry != 0
}

//go:linkname isolateCheckHostClosure
func isolateCheckHostClosure(p unsafe.Pointer) {
	if isolateHostExecution(getg()) || getg().isolateConstruction != nil {
		if span := spanOfHeap(uintptr(p)); span != nil && span.isolateAllocOwner != 0 && !isolateConstructionContains(p, 1) {
			isolateReportFault("isolate: application callback requires HostContext.Reenter", "application callback without HostContext.Reenter")
		}
	}
	if isolateHostEntry(p) {
		return
	}
	isolateCheckHeapAccess(p, unsafe.Sizeof(uintptr(0)), false)
}

// Trusted sharing changes reference publication, not memory access. Once the
// host exports an allocator or enters an operation, opaque host references may
// be retained by that instance and host control records may root its graph.
// Other instances retain the original publication enforcement.
func isolateTrustedHostReference(value unsafe.Pointer, owner uintptr) bool {
	gp := getg()
	if isolateHostExecution(gp) || gp.isolateConstruction != nil {
		return true
	}
	source := spanOfHeap(uintptr(value))
	if source == nil {
		return false
	}
	if source.isolateAllocOwner == 0 {
		return owner != 0 && gp.isolateGroup != nil && gp.isolateGroup.resources.account.hostIntegration.Load() || isolateHostEntry(value)
	}
	return owner == 0 && source.isolateResources != nil && source.isolateResources.hostIntegration.Load()
}

//go:linkname isolatePendingHostCalls
func isolatePendingHostCalls(p unsafe.Pointer) int64 {
	return (*isolateRevocationGroup)(p).hostCalls.Load()
}

//go:linkname isolatePendingHostWork
func isolatePendingHostWork(p unsafe.Pointer) int32 {
	return (*isolateRevocationGroup)(p).hostWork.Load()
}

// A compiler-typed function load may import a registered handle from a host
// binding cell. Only that function word is accessible; neither adjacent state
// nor the registered closure's captures receive ordinary read permission.
func isolateCheckHostFuncCell(p unsafe.Pointer) {
	if p != nil && getg().isolateOwner != 0 {
		end := uintptr(p) + unsafe.Sizeof(uintptr(0)) - 1
		host := false
		if s := spanOfHeap(uintptr(p)); s != nil {
			host = s.isolateAllocOwner == 0 && end < s.limit
		} else if known, _ := isolateStaticRange(uintptr(p), end); known {
			host = true
		}
		if host && isolateHostEntry(*(*unsafe.Pointer)(p)) {
			return
		}
	}
	isolateCheckHeapAccess(p, unsafe.Sizeof(uintptr(0)), false)
}

// Compiler function-value materialization preserves domain provenance even for
// noncapturing callbacks and private globals initialized with static functions.
// The code PC is unchanged (including reflect.Value.Pointer). Host values and
// registered entries retain their original representation.
func isolateBindFunction(p unsafe.Pointer) unsafe.Pointer {
	if p == nil || !isolateActive() || getg().isolateMetadataDepth != 0 || getg().isolateOwner == 0 || spanOfHeap(uintptr(p)) != nil {
		return p
	}
	// Stack closures also have no heap span. They may contain captures and
	// must keep their complete environment; only immutable static funcvals are
	// eligible for a one-word domain box.
	if known, readonly := isolateStaticRange(uintptr(p), uintptr(p)+unsafe.Sizeof(uintptr(0))-1); !known || !readonly {
		return p
	}
	f := new(funcval)
	f.fn = (*funcval)(p).fn
	return unsafe.Pointer(f)
}

func isolateConstructionContains(p unsafe.Pointer, size uintptr) bool {
	for scope := getg().isolateConstruction; scope != nil; scope = scope.previous {
		for node := scope.allocations; node != nil; node = node.next {
			if uintptr(p) >= uintptr(node.base) && uintptr(p)-uintptr(node.base) < node.size && size <= node.size-(uintptr(p)-uintptr(node.base)) {
				return true
			}
		}
	}
	return false
}

//go:linkname isolateCheckConstructor
func isolateCheckConstructor(fn any) {
	f := efaceOf(&fn)
	if f._type == nil || f._type.Kind() != abi.Func {
		panic("isolate: invalid constructor")
	}
	isolateCheckHostClosure(f.data)
}

func newIsolateScratchLifetime() *isolateScratchLifetime {
	gp := getg()
	owner := gp.isolateOwner
	gp.isolateMetadataDepth++
	gp.isolateOwner = 0
	lifetime := new(isolateScratchLifetime)
	gp.isolateOwner = owner
	gp.isolateMetadataDepth--
	return lifetime
}

// Host helpers may temporarily change service/spawn state. Reentry and waiter
// admission use the original application role, independently of those scopes.
type isolateExecutionRole struct {
	readOnlyOwner                                 uintptr
	readOnlyService, inboundService, stackService bool
	priority, spawnPriority                       uint8
}

func isolateGetExecutionRole(gp *g) isolateExecutionRole {
	return isolateExecutionRole{readOnlyOwner: gp.isolateReadOnlyOwner, readOnlyService: gp.isolateReadOnlyService,
		inboundService: gp.isolateInboundService, stackService: gp.isolateStackService,
		priority: gp.isolatePriority, spawnPriority: gp.isolateSpawnPriority}
}

func isolateSetExecutionRole(gp *g, role isolateExecutionRole) {
	gp.isolateReadOnlyOwner, gp.isolateReadOnlyService = role.readOnlyOwner, role.readOnlyService
	gp.isolateInboundService, gp.isolateStackService = role.inboundService, role.stackService
	gp.isolatePriority, gp.isolateSpawnPriority = role.priority, role.spawnPriority
}
