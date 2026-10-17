// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatepolicy defines standard-library metadata boundaries.
// Third-party libraries use explicit host integration APIs.
package isolatepolicy

import (
	"strings"
)

// MetadataGlobal identifies bounded reads of fixed standard-library tables.
func MetadataGlobal(symbol string) bool {
	// SHA backend flags select equivalent implementations. These scalar reads
	// and the fixed round table expose no application object or write privilege.
	switch symbol {
	case "internal/cpu.X86", "internal/cpu.ARM", "internal/cpu.ARM64",
		"internal/cpu.Loong64", "internal/cpu.MIPS64X", "internal/cpu.PPC64", "internal/cpu.S390X":
		// Feature records are fixed during runtime startup. Replayed library
		// initializers (for example math.useFMA) need these bounded reads;
		// writes and reads of other CPU package state remain checked.
		return true
	case "crypto/internal/fips140/sha256.useSHA2", "crypto/internal/fips140/sha256.useAVX2",
		"crypto/internal/fips140/sha256.useSHANI", "crypto/internal/fips140/sha256.useSHA256",
		"crypto/internal/fips140/sha256.ppc64sha2", "crypto/internal/fips140/sha256._K":
		return true
	}
	return false
}

// The collector calls this implementation with the world stopped. It must not
// allocate, grow a stack, or consult application ownership while retiring the
// process pool lists. Private Put/Get never join those lists.
func RuntimeHook(pkg, function string) bool {
	if pkg == "isolate" {
		switch function {
		case "HostContext.Allocator", "HostContext.Reenter", "HostAllocator", "(*Isolate).Allocator", "NewWaiter", "(*Waiter).Park", "(*Waiter).Wake":
			return true
		}
	}

	// The post-fork child cannot grow its stack, allocate, or acquire locks.
	// Instrumenting its local pointer stores violates that runtime contract.
	// Public process/syscall entry points reject isolate execution before this
	// private path; do not grant the exemption to arbitrary //go:norace code.
	if pkg == "syscall" {
		switch function {
		case "forkAndExecInChild", "forkAndExecInChild1",
			// Darwin calls these nosplit libc wrappers between BeforeFork
			// and AfterFork. Their argument-array stores must also remain
			// free of probes. Public syscall wrappers retain their checks.
			"rawSyscall", "rawSyscall6", "rawSyscall9":
			return true
		}
	}
	// Pure manifest queries run from the runtime probes themselves. Adding
	// probes to them would recursively invoke the effect/ownership checker.
	if pkg == "internal/isolatepolicy" {
		return true
	}
	// Metrics descriptions are explicitly copied into the caller's owner.
	if pkg == "runtime/metrics" && function == "All" {
		return true
	}
	if pkg == "sync" && function == "poolCleanup" {
		return true
	}
	// Value validates its receiver and interface contents explicitly before
	// procPin. Compiler checks inside the pinned first-store sequence could
	// allocate or discard a pinned G while handling its internal sentinel.
	if pkg == "sync/atomic" {
		switch function {
		case "(*Value).Load", "(*Value).Store", "(*Value).Swap", "(*Value).CompareAndSwap":
			return true
		}
	}
	return false
}

// MetadataCallbackPackage lists library implementations whose dynamic calls
// may run inside an audited service. The compiler must also verify GOROOT
// provenance; matching this package name is insufficient.
func MetadataCallbackPackage(pkg string) bool {
	switch pkg {
	case "runtime", "reflect", "sync", "sync/atomic", "bytes", "strings",
		"strconv", "fmt", "errors", "sort", "cmp", "slices", "maps", "iter", "unicode", "regexp/syntax":
		return true
	}
	for _, prefix := range []string{"runtime/", "internal/", "encoding/", "unicode/"} {
		if strings.HasPrefix(pkg, prefix) {
			return true
		}
	}
	return false
}

// Lifecycle callbacks belong to the trusted public isolate implementation.
// None invoke application code or the application's state factory.
func MetadataLifecycleCallback(pkg, function string) bool {
	if pkg != "isolate" {
		return false
	}
	switch function {
	case "(*Isolate).complete.func1", "(*Isolate).completeExit.func1",
		"(*Isolate).complete.deferwrap1", "(*Isolate).completeExit.deferwrap1",
		"(*Isolate).finishCleanup", "(*Isolate).finishCleanup-fm",
		"(*initializerCompletion).exit", "(*initializerCompletion).exit-fm",
		"(*initializerCompletion).close.func1":
		return true
	}
	return false
}
