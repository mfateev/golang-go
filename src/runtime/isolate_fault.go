// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

// The record is allocated under the process owner and published once. Runtime
// probes without a managed host handler retain their diagnostic panic behavior.
type isolateOwnershipFault struct {
	reason string
}

//go:linkname isolateSetOwnershipFaultHandler
func isolateSetOwnershipFaultHandler(p unsafe.Pointer, handler func(string)) {
	group := (*isolateRevocationGroup)(p)
	if handler == nil || group.ownershipHandler != nil || group.live.Load() != 0 {
		throw("isolate: invalid ownership fault handler")
	}
	group.ownershipHandler = handler
}

//go:linkname isolateOwnershipViolation
func isolateOwnershipViolation(reason string) {
	gp := getg()
	group := gp.isolateGroup
	if group == nil || group.ownershipHandler == nil {
		if gp.isolateOwner == 0 {
			panic(reason)
		}
		// Deliberately privileged diagnostic probes recover on the host after
		// restoring its owner. Copy the string and interface box there so the
		// helper's new parameter boxing does not expose a private allocation.
		owner := gp.isolateOwner
		gp.isolateOwner = 0
		text, data := rawstring(len(reason))
		copy(data, reason)
		var diagnostic any = text
		gp.isolateOwner = owner
		panic(diagnostic)
	}
	owner, depth := gp.isolateOwner, gp.isolateMetadataDepth
	if depth == ^uint32(0) {
		throw("isolate: ownership reporting nesting overflow")
	}
	// Reporting must survive revocation by a simultaneous fault. Only this
	// trusted copy/worker creation uses the temporary process allocation scope.
	gp.isolateMetadataDepth++
	gp.isolateOwner = 0
	text, data := rawstring(len(reason))
	copy(data, reason)
	fault := &isolateOwnershipFault{reason: text}
	first := group.ownershipFault.CompareAndSwap(nil, fault)
	group.markRevoked()
	if first {
		go isolateReportOwnershipFault(group, fault)
	}
	gp.isolateOwner, gp.isolateMetadataDepth = owner, depth
	if depth != 0 {
		// Recover is disabled for the failed group. Service cleanup defers run
		// until outermost Leave, which discards the G before application defers.
		panic(reason)
	}
	isolateDiscardIfRevoked()
	throw("isolate: ownership violation returned after revocation")
}

// Runtime system goroutines inherit no group or private allocator. Publish the
// error, close Call, and scan wait queues from the process, outside service locks
// and the deterministic token held by the failing application goroutine.
func isolateReportOwnershipFault(group *isolateRevocationGroup, fault *isolateOwnershipFault) {
	group.ownershipHandler(fault.reason)
}

// The immutable record also lets host completion paths report the cause before
// the asynchronous waiter scan/reporter has had a chance to run.
//
//go:linkname isolateOwnershipFaultReason
func isolateOwnershipFaultReason(p unsafe.Pointer) string {
	if fault := (*isolateRevocationGroup)(p).ownershipFault.Load(); fault != nil {
		return fault.reason
	}
	return ""
}
