// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"internal/isolatepolicy"
	"strings"
)

// Run before inlining: a guarded callee must carry its guard into importers.
// Dynamic calls still need runtime checks, including reflection and native code.
var isolateEffectsScoped = make(map[*ir.Func]bool)

// Run both for initial bodies and newly instantiated/imported generic bodies.
func ScopeIsolateEffects() {
	for _, fn := range Target.Funcs {
		ScopeIsolateEffectsInBody(fn, fn)
	}
}

// source carries the original declaration/provenance when Unified IR reads a
// fresh body for inlining. Guards must survive that reread as well.
func ScopeIsolateEffectsInBody(fn, source *ir.Func) {
	if base.Debug.IsolateEffects == 0 || base.Flag.CompilingRuntime || len(fn.Body) == 0 || isolateEffectsScoped[fn] {
		return
	}
	isolateEffectsScoped[fn] = true
	previous := ir.CurFunc
	defer func() { ir.CurFunc = previous }()
	entries := strings.Split(base.Debug.IsolateEffectEntries, ":")
	ir.CurFunc = fn
	pkg, name := source.Sym().Pkg.Path, ir.FuncName(source)
	full := pkg + "." + name
	entry := false
	for _, e := range entries {
		if e == full {
			entry = true
		}
	}
	audited := source.Pragma&(ir.IsolateEffectAudited|ir.IsolateMetadataTrusted) != 0 || base.Flag.Std && source.Sym().Pkg.Path == base.Ctxt.Pkgpath
	reason := ""
	if isolatepolicy.Forbidden(pkg, name) {
		reason = full
	}
	ir.VisitList(fn.Body, func(n ir.Node) {
		if !audited && n.Op() == ir.ONAME {
			decl := n.(*ir.Name)
			if decl.Class == ir.PFUNC && decl.Sym().Pkg != nil {
				sym := decl.Sym()
				alias := sym.Linkname
				if alias != "" && alias != sym.Pkg.Path+"."+sym.Name && !isolateEntryAlias(alias) {
					if reason == "" {
						reason = "go:linkname " + alias
					}
					if entry {
						base.ErrorfAt(n.Pos(), 0, "isolate: forbidden go:linkname target %s", alias)
					}
				}
			}
		}
		var reference *types.Sym
		switch n.Op() {
		case ir.ONAME:
			if decl := n.(*ir.Name); decl.Class == ir.PFUNC {
				reference = decl.Sym()
			}
		case ir.ODOTMETH, ir.OMETHVALUE, ir.OMETHEXPR:
			sel := n.(*ir.SelectorExpr)
			reference, _ = ir.MethodSym(sel.X.Type(), sel.Selection)
		}
		if reference != nil && reference.Pkg != nil && isolatepolicy.UnsafeReflection(reference.Pkg.Path, reference.Name) {
			fn.Pragma |= ir.Noinline // retain the audited caller's provenance
			if !audited {
				if reason == "" {
					reason = "unsafe reflection in " + full
				}
				if entry {
					base.ErrorfAt(n.Pos(), 0, "isolate: forbidden unsafe reflection in entry %s", full)
				}
			}
		}
		if !audited && unsafeIsolateOperation(n) {
			if reason == "" {
				reason = "unsafe operation in " + full
			}
			if entry {
				base.ErrorfAt(n.Pos(), 0, "isolate: forbidden unsafe operation in entry %s", full)
			}
		}
		if !entry {
			return
		}
		if n.Op() == ir.OCALLFUNC || n.Op() == ir.OCALLMETH {
			call := n.(*ir.CallExpr)
			if target := ir.StaticCalleeName(call.Fun); target != nil && target.Sym().Pkg != nil {
				sym := target.Sym()
				if isolatepolicy.Forbidden(sym.Pkg.Path, sym.Name) {
					base.ErrorfAt(n.Pos(), 0, "isolate: forbidden operation %s.%s in entry %s", sym.Pkg.Path, sym.Name, full)
				}
			}
		}
	})
	if reason != "" {
		fn.Pragma |= ir.Noinline | ir.IsolateEffectGuarded
		fn.Body.Prepend(Stmt(Call(fn.Pos(), LookupRuntime("isolateRejectEffect"), []ir.Node{ir.NewString(fn.Pos(), reason)}, false)))
	}
}

func unsafeIsolateOperation(n ir.Node) bool {
	switch n.Op() {
	case ir.OUNSAFEADD, ir.OUNSAFESLICE, ir.OUNSAFESTRING, ir.OUNSAFESLICEDATA, ir.OUNSAFESTRINGDATA:
		return true
	case ir.OCONV, ir.OCONVNOP:
		conv := n.(*ir.ConvExpr)
		// Only the generated entry may turn its typed descriptor addresses into
		// opaque state handles. Application conversions can feed reflect.NewAt
		// or other type-punning APIs and are rejected even without uintptr.
		return conv.X.Type().IsUnsafePtr() || conv.Type().IsUnsafePtr() &&
			!(base.Debug.IsolateEntryAliases != "" && conv.X.Type().IsPtr())
	}
	return false
}

func isolateEntryAlias(alias string) bool {
	for _, target := range strings.Split(base.Debug.IsolateEntryAliases, ":") {
		if target != "" && target == alias {
			return true
		}
	}
	return false
}
