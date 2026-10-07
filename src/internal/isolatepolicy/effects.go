// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatepolicy describes effects denied to workflow execution.
// It has no imports: both the compiler and the core runtime use this manifest.
package isolatepolicy

// Forbidden identifies an operation before it can read machine state or perform
// an external effect. Pure value operations are intentionally kept available.
// Runtime membership, rather than allocator ownership, determines enforcement.
func Forbidden(pkg, name string) bool {
	// Generic top-level implementations carry shape arguments in pclntab.
	for i := range len(name) {
		if name[i] == '[' {
			if name[len(name)-1] == ']' {
				name = name[:i]
			}
			break
		}
	}
	if prefix(name, "init") {
		return false
	}
	switch pkg {
	case "isolate", "internal/isolatebridge":
		return name == "New"
	case "syscall", "golang.org/x/sys/unix", "golang.org/x/sys/windows":
		switch name {
		case "Exit", "ByteSliceFromString", "BytePtrFromString", "StringByteSlice", "StringBytePtr", "UTF16FromString", "UTF16PtrFromString", "UTF16ToString", "StringToUTF16", "StringToUTF16Ptr", "(*Errno).Error", "Errno.Error", "Errno.Is", "Errno.Timeout", "Errno.Temporary":
			return false
		}
		return true
	case "os":
		// Exit is already redirected to instance termination. Error predicates
		// and value formatting do not consult the OS.
		switch name {
		case "Exit", "IsExist", "IsNotExist", "IsPermission", "IsTimeout", "NewSyscallError", "FileMode.String", "FileMode.IsDir", "FileMode.IsRegular", "FileMode.Perm", "FileMode.Type", "(*PathError).Error", "(*PathError).Unwrap", "(*PathError).Timeout", "(*SyscallError).Error", "(*SyscallError).Unwrap", "(*SyscallError).Timeout":
			return false
		}
		return exported(name)
	case "os/exec", "os/signal", "os/user", "plugin":
		return exported(name)
	case "net":
		return prefix(name, "Dial") || prefix(name, "Listen") || prefix(name, "Lookup") || prefix(name, "Resolve") || prefix(name, "Interfaces") || prefix(name, "InterfaceBy") || name == "FileConn" || name == "FileListener" || name == "FilePacketConn" || name == "Pipe" || prefix(name, "(*Resolver).") || prefix(name, "(*Dialer).") || prefix(name, "(*ListenConfig).") || prefix(name, "(*netFD).")
	case "net/http":
		return name == "Get" || name == "Head" || name == "Post" || name == "PostForm" || prefix(name, "ListenAndServe") || prefix(name, "Serve") || prefix(name, "(*Client).") || prefix(name, "(*Transport).") || prefix(name, "(*Server).")
	case "crypto/rand":
		return name == "Read" || name == "Text" || name == "Int" || name == "Prime" || name == "(*reader).Read"
	case "runtime":
		switch name {
		case "SetFinalizer", "AddCleanup", "GC", "ReadMemStats", "NumGoroutine", "NumCPU", "NumCgoCall", "GOMAXPROCS", "SetDefaultGOMAXPROCS", "MemProfile", "BlockProfile", "MutexProfile", "ThreadCreateProfile", "GoroutineProfile", "SetBlockProfileRate", "SetMutexProfileFraction", "SetCPUProfileRate", "CPUProfile", "Stack", "Breakpoint", "LockOSThread", "UnlockOSThread", "cgocall", "nanotime", "walltime", "time_now", "getRandomData":
			return true
		}
	case "runtime/metrics":
		return name == "Read"
	case "runtime/debug", "runtime/pprof", "runtime/trace", "runtime/coverage", "runtime/cgo", "testing", "testing/synctest", "unique", "weak":
		return exported(name)
	case "expvar":
		switch name {
		case "Publish", "Get", "NewInt", "NewFloat", "NewMap", "NewString", "Do", "Handler":
			return true
		}
	case "flag":
		// Local FlagSets are value parsers. Process CommandLine aliases have
		// explicit receiver guards as well as these top-level diagnostics.
		return exported(name) && !prefix(name, "(*FlagSet).") && name != "NewFlagSet" && name != "UnquoteUsage"
	case "time":
		return name == "LoadLocation" || name == "tzset" || name == "NewTicker" || name == "Tick"
	}
	return false
}

// ForbiddenSymbol also recognizes method values and ABI wrappers.
func ForbiddenSymbol(symbol string) bool {
	pkg, name := splitSymbol(symbol)
	return Forbidden(pkg, name)
}

// UnsafeReflection is permitted only within audited implementations, never as
// an application callback. Value.Pointer remains available for function metadata
// used by typed activity references; application uintptr-to-pointer conversion
// is independently prohibited.
func UnsafeReflection(pkg, name string) bool {
	if pkg != "reflect" {
		return false
	}
	switch name {
	case "NewAt", "SliceAt", "Value.UnsafeAddr", "(*Value).UnsafeAddr", "Value.UnsafePointer", "(*Value).UnsafePointer":
		return true
	}
	return false
}
func UnsafeReflectionSymbol(symbol string) bool {
	pkg, name := splitSymbol(symbol)
	return UnsafeReflection(pkg, name)
}

func splitSymbol(symbol string) (string, string) {
	lastSlash := -1
	for i := range len(symbol) {
		if symbol[i] == '[' || symbol[i] == '(' {
			break
		}
		if symbol[i] == '/' {
			lastSlash = i
		}
	}
	for i := lastSlash + 1; i < len(symbol); i++ {
		if symbol[i] != '.' {
			continue
		}
		name := symbol[i+1:]
		for _, suffix := range []string{"-fm", ".abi0", ".abiinternal"} {
			if len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix {
				name = name[:len(name)-len(suffix)]
			}
		}
		return symbol[:i], name
	}
	return "", ""
}

func prefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func exported(name string) bool {
	depth, start := 0, 0
	for i := range len(name) {
		switch name[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				start = i + 1
			}
		}
	}
	name = name[start:]
	return len(name) != 0 && name[0] >= 'A' && name[0] <= 'Z'
}
