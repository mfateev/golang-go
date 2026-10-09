// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatepolicy is the pinned compiler/build policy for trusted
// metadata operations. It grants no package-wide allocation or access privilege.
package isolatepolicy

import "strings"

const ProtobufModule = "google.golang.org/protobuf"
const ProtobufVersion = "v1.36.11"

const TemporalAPIModule = "go.temporal.io/api"
const TemporalAPIVersion = "v1.63.6"

const TemporalSDKModule = "go.temporal.io/sdk"
const TemporalSDKVersion = "v1.49.0"

// MetadataScope identifies functions that only construct/cache type descriptions
// or read the built-in registries. Marshal/unmarshal, value allocation, and
// application callbacks are deliberately absent. Private or custom descriptor
// implementations are outside this initial manifest.
func MetadataScope(pkg, function string) bool {
	switch pkg {
	case ProtobufModule + "/internal/impl":
		return function == "(*MessageInfo).init" || function == "(*MessageInfo).initOnce" || function == "(*MessageInfo).Descriptor" || function == "needsInitCheck" || function == "(*ExtensionInfo).lazyInitSlow"
	case ProtobufModule + "/internal/filedesc":
		switch function {
		case "(*File).lazyInitOnce", "(*stringName).lazyInit",
			"(*Message).Fields", "(*Fields).ByName",
			"(*Names).lazyInit", "(*EnumRanges).lazyInit", "(*FieldRanges).lazyInit",
			"(*FieldNumbers).Has", "(*OneofFields).lazyInit", "(*SourceLocations).lazyInit",
			"(*Enums).lazyInit", "(*EnumValues).lazyInit", "(*Messages).lazyInit",
			"(*Fields).lazyInit", "(*Oneofs).lazyInit", "(*Extensions).lazyInit",
			"(*Services).lazyInit", "(*Methods).lazyInit":
			return true
		}
	case ProtobufModule + "/reflect/protoregistry":
		switch function {
		case "(*Files).FindDescriptorByName", "(*Files).FindFileByPath", "(*Files).NumFiles", "(*Files).NumFilesByPackage",
			"(*Types).FindEnumByName", "(*Types).FindMessageByName", "(*Types).FindMessageByURL",
			"(*Types).FindExtensionByName", "(*Types).FindExtensionByNumber",
			"(*Types).NumEnums", "(*Types).NumMessages", "(*Types).NumExtensions", "(*Types).NumExtensionsByMessage":
			return true
		}
	}
	return false
}

// RejectedMetadata preserves host behavior and fails explicitly in an isolate.
// Registry visitors hold a process lock while invoking caller code, so granting
// the whole method a service scope would give that caller metadata privileges.
// Lazy option decoders and legacy descriptor hooks need a separate audit.
func RejectedMetadata(pkg, function string) bool {
	switch pkg {
	case ProtobufModule + "/reflect/protoregistry":
		switch function {
		case "(*Files).RegisterFile", "(*Files).RangeFiles", "(*Files).RangeFilesByPackage",
			"(*Types).RegisterEnum", "(*Types).RegisterMessage", "(*Types).RegisterExtension",
			"(*Types).RangeEnums", "(*Types).RangeMessages", "(*Types).RangeExtensions", "(*Types).RangeExtensionsByMessage":
			return true
		}
	case ProtobufModule + "/internal/impl":
		switch function {
		case "legacyLoadMessageInfo", "legacyLoadMessageDesc", "aberrantLoadMessageDesc":
			return true
		}
	case ProtobufModule + "/internal/filedesc":
		switch function {
		case "(*File).Options", "(*Enum).Options", "(*Message).Options", "(*Field).Options",
			"(*Oneof).Options", "(*Extension).Options", "(*Service).Options", "(*Method).Options", "(*File).OptionImports":
			return true
		}
	}
	return false
}

// These cells hold opaque metadata handles from the pinned process builder.
// Only reads of the cell itself are allowed. This grants no read of arbitrary
// receiver fields, mutation, array resizing, or private-reference publication.
func MetadataGlobal(symbol string) bool {
	// The default failure converter compares this startup-derived type name
	// with reflect.Type.Name. Its backing bytes are immutable type metadata.
	if symbol == TemporalSDKModule+"/internal.goErrType" {
		return true
	}
	if symbol == TemporalSDKModule+"/internal.ErrNoData" || symbol == TemporalSDKModule+"/temporal.ErrNoData" {
		return true
	}
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
	if symbol == ProtobufModule+"/reflect/protoregistry.GlobalTypes" || symbol == ProtobufModule+"/reflect/protoregistry.GlobalFiles" {
		return true
	}
	if !strings.HasPrefix(symbol, TemporalAPIModule+"/") {
		return false
	}
	i := strings.LastIndexByte(symbol, '.')
	return i != -1 && strings.HasPrefix(symbol[i+1:], "file_") && strings.HasSuffix(symbol, "_msgTypes")
}

// The collector calls this implementation with the world stopped. It must not
// allocate, grow a stack, or consult application ownership while retiring the
// process pool lists. Private Put/Get never join those lists.
func RuntimeHook(pkg, function string) bool {
	// The post-fork child cannot grow its stack, allocate, or acquire locks.
	// Instrumenting its local pointer stores violates that runtime contract.
	// Public process/syscall entry points reject isolate execution before this
	// private path; do not grant the exemption to arbitrary //go:norace code.
	if pkg == "syscall" && (function == "forkAndExecInChild" || function == "forkAndExecInChild1") {
		return true
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
// provenance or the pinned module source; matching this name is insufficient.
func MetadataCallbackPackage(pkg string) bool {
	switch pkg {
	case "runtime", "reflect", "sync", "sync/atomic", "bytes", "strings",
		"strconv", "fmt", "errors", "sort", "cmp", "slices", "maps", "iter", "unicode":
		return true
	}
	for _, prefix := range []string{"runtime/", "internal/", "encoding/", "unicode/", ProtobufModule + "/", TemporalAPIModule + "/"} {
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
