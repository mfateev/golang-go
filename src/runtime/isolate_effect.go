// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import (
	"internal/abi"
	"internal/isolatepolicy"
	"internal/runtime/sys"
	"unsafe"
)

//go:linkname isolateRejectEffect
//go:nosplit
func isolateRejectEffect(operation string) {
	if !isolateActive() {
		return
	}
	isolateRejectEffectSlow(operation)
}

func isolateRejectEffectSlow(operation string) {
	isolateReportFault("isolate: forbidden operation "+operation, operation)
}

// Function values, reflection and assembly wrappers must obey the same policy
// as ordinary calls. The compiler's metadata flag carries verified provenance;
// an external assembly function cannot grant itself that flag.
//
//go:linkname isolateCheckEffectCall
//go:nosplit
func isolateCheckEffectCall(pc uintptr) {
	if !isolateActive() || pc == 0 {
		return
	}
	isolateCheckEffectCallSlow(pc, sys.GetCallerPC())
}

func isolateCheckEffectCallSlow(pc, caller uintptr) {
	fn := findfunc(pc)
	if !fn.valid() {
		isolateRejectEffect("unclassified native call")
		return
	}
	name := funcname(fn)
	if isolatepolicy.UnsafeReflectionSymbol(name) {
		calling := findfunc(caller)
		if !calling.valid() || calling.flag&(abi.FuncFlagIsolateEffectAudited|abi.FuncFlagIsolateMetadataTrusted) == 0 {
			isolateRejectEffect(name)
		}
	}
	if isolatepolicy.ForbiddenSymbol(name) || fn.flag&abi.FuncFlagIsolateEffectForbidden != 0 || fn.flag&abi.FuncFlagAsm != 0 && fn.flag&abi.FuncFlagIsolateEffectAudited == 0 {
		isolateRejectEffect(name)
	}
}

//go:nosplit
func isolateCheckEffectClosure(fn unsafe.Pointer) {
	if !isolateActive() || fn == nil {
		return
	}
	isolateCheckEffectClosureSlow(fn, sys.GetCallerPC())
}

func isolateCheckEffectClosureSlow(fn unsafe.Pointer, caller uintptr) {
	// Compiler ownership probes validate application closure objects. Trusted
	// entry/transport runners may hold a host-owned closure by their audited
	// contract; effect classification reads only its immutable code pointer.
	isolateCheckEffectCallSlow(*(*uintptr)(fn), caller)
}

// Capture only the offending goroutine, under the reporting allocation owner.
// Calling public Stack here would itself be a forbidden workflow operation.
func isolateEffectStack() string {
	gp := getg()
	sp, pc := sys.GetCallerSP(), sys.GetCallerPC()
	buf := make([]byte, 64<<10)
	n := 0
	systemstack(func() {
		g0 := getg()
		g0.m.traceback = 1
		g0.writebuf = buf[:0:len(buf)]
		goroutineheader(gp)
		traceback(pc, sp, 0, gp)
		g0.m.traceback = 0
		n = len(g0.writebuf)
		g0.writebuf = nil
	})
	return string(buf[:n])
}

//go:linkname isolateEffectFaultDetails
func isolateEffectFaultDetails(p unsafe.Pointer) (operation, stack string) {
	if fault := (*isolateRevocationGroup)(p).ownershipFault.Load(); fault != nil {
		return fault.operation, fault.stack
	}
	return "", ""
}

// Reflection dispatches a supplied callback, so the reflect package's own
// provenance must not authorize an application unsafe accessor.
//
//go:linkname isolateCheckReflectEffectCall
//go:nosplit
func isolateCheckReflectEffectCall(pc uintptr) {
	if isolateActive() && pc != 0 {
		isolateCheckEffectCallSlow(pc, 0)
	}
}
