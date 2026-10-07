// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ir

import (
	"cmd/compile/internal/base"
	"cmd/internal/isolatepolicy"
	"cmd/internal/obj"
	"internal/abi"
	"strings"
)

// InitLSym defines f's obj.LSym and initializes it based on the
// properties of f. This includes setting the symbol flags and ABI and
// creating and initializing related DWARF symbols.
//
// InitLSym must be called exactly once per function and must be
// called for both functions with bodies and functions without bodies.
// For body-less functions, we only create the LSym; for functions
// with bodies call a helper to setup up / populate the LSym.
func InitLSym(f *Func, hasBody bool) {
	if f.LSym != nil {
		base.FatalfAt(f.Pos(), "InitLSym called twice on %v", f)
	}

	if nam := f.Nname; !IsBlank(nam) {
		f.LSym = nam.LinksymABI(f.ABI)
		if f.Pragma&Systemstack != 0 {
			f.LSym.Set(obj.AttrCFunc, true)
		}
	}
	if hasBody {
		setupTextLSym(f, 0)
	}
}

// setupTextLSym initializes the LSym for a with-body text symbol.
func setupTextLSym(f *Func, flag int) {
	if f.Dupok() {
		flag |= obj.DUPOK
	}
	if f.Wrapper() {
		flag |= obj.WRAPPER
	}
	if f.ABIWrapper() {
		flag |= obj.ABIWRAPPER
	}
	if f.Needctxt() {
		flag |= obj.NEEDCTXT
	}
	if f.Pragma&Nosplit != 0 {
		flag |= obj.NOSPLIT
	}
	if f.IsPackageInit() {
		flag |= obj.PKGINIT
	}

	// Clumsy but important.
	// For functions that could be on the path of invoking a deferred
	// function that can recover (runtime.reflectcall, reflect.callReflect,
	// and reflect.callMethod), we want the panic+recover special handling.
	// See test/recover.go for test cases and src/reflect/value.go
	// for the actual functions being considered.
	//
	// runtime.reflectcall is an assembly function which tailcalls
	// WRAPPER functions (runtime.callNN). Its ABI wrapper needs WRAPPER
	// flag as well.
	fnname := f.Sym().Name
	if base.Ctxt.Pkgpath == "runtime" && fnname == "reflectcall" {
		flag |= obj.WRAPPER
	} else if base.Ctxt.Pkgpath == "reflect" {
		switch fnname {
		case "callReflect", "callMethod":
			flag |= obj.WRAPPER
		}
	}

	base.Ctxt.InitTextSym(f.LSym, flag, f.Pos())
	if base.Flag.Std || f.Pragma&IsolateEffectAudited != 0 {
		f.LSym.Func().FuncFlag |= abi.FuncFlagIsolateEffectAudited
	}
	if f.ABIWrapper() && !base.Flag.Std {
		f.LSym.Func().FuncFlag |= abi.FuncFlagIsolateEffectForbidden
	}
	// Record source provenance before names or linkname aliases reach the linker.
	// Standard callbacks are trusted only when compiled from GOROOT. Module
	// callbacks require the source-pinned metadata build selected by cmd/go.
	pkg, name := f.Sym().Pkg.Path, f.Sym().Name
	trusted := f.Pragma&IsolateMetadataTrusted != 0 || IsolateMetadataSourceTrusted(pkg, name)
	// Pure equality/hash algorithms are compiler generated, including those
	// for application types. They cannot call application-defined operations.
	if pkg == "type" && (strings.HasPrefix(name, ".eq.") || strings.HasPrefix(name, ".hash.")) {
		trusted = true
	}
	if trusted {
		f.LSym.Func().FuncFlag |= abi.FuncFlagIsolateMetadataTrusted
	}

}

// IsolateMetadataSourceTrusted is evaluated when the source declaration is
// compiled/exported. Generic instantiations may be emitted in application
// packages; their serialized provenance, not the importing package or a source
// filename/line directive, determines callback trust.
func IsolateMetadataSourceTrusted(pkg, name string) bool {
	if base.Flag.Std && (isolatepolicy.MetadataCallbackPackage(pkg) || isolatepolicy.MetadataLifecycleCallback(pkg, name)) {
		return true
	}
	return base.Debug.IsolateMetadata != 0 && (strings.HasPrefix(pkg, isolatepolicy.ProtobufModule+"/") || strings.HasPrefix(pkg, isolatepolicy.TemporalAPIModule+"/"))
}

// IsolateEscapeHelper identifies the standard compiler escape-analysis stub.
// Its sole store is under internal/abi.alwaysFalse, which is never changed.
// Imported generic instantiations retain verified standard-library provenance.
// This permits no caller callback or process allocation service.
func IsolateEscapeHelper(fn *Func) bool {
	return fn.Pragma&IsolateEffectAudited != 0 && fn.Sym().Pkg.Path == "internal/abi" && strings.HasPrefix(fn.Sym().Name, "Escape[")
}
