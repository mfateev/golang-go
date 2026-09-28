// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"cmp"
	"slices"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/objw"
	"cmd/compile/internal/reflectdata"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
	"cmd/internal/obj"
)

// isolateLayoutOffsets is immutable while the SSA backend compiles functions.
// The generated type gives the current GC a precise pointer map for the
// package's isolate-owned globals. Package selection is opt-in for this probe.
var isolateLayoutOffsets map[*types.Sym]int64

func InitIsolateLayout() {
	if base.Debug.IsolateGlobals == 0 {
		return
	}
	var globals []*ir.Name
	for _, n := range typecheck.Target.Externs {
		if n.Op() == ir.ONAME && n.Class == ir.PEXTERN && n.Sym().Pkg == types.LocalPkg {
			globals = append(globals, n)
		}
	}
	slices.SortFunc(globals, func(a, b *ir.Name) int {
		return cmp.Compare(a.Sym().Name, b.Sym().Name)
	})
	fields := make([]*types.Field, len(globals))
	for i, n := range globals {
		// Use an unspellable field name to avoid colliding with source names.
		fields[i] = types.NewField(n.Pos(), types.LocalPkg.Lookup("isolate$"+n.Sym().Name), n.Type())
	}
	layout := types.NewStruct(fields)
	types.CalcSize(layout)
	isolateLayoutOffsets = make(map[*types.Sym]int64, len(globals))
	for i, n := range globals {
		isolateLayoutOffsets[n.Sym()] = fields[i].Offset
	}

	// The opt-in package exposes the exact runtime type to its host-side
	// allocator. The type's GC metadata describes the generated layout.
	sym := typecheck.Lookup("isolateLayoutType")
	if sym.Def != nil {
		base.Fatalf("isolate: source declaration conflicts with generated layout type symbol")
	}
	name := ir.NewNameAt(base.Pos, sym, types.Types[types.TUNSAFEPTR])
	name.Class = ir.PEXTERN
	sym.Def = name
	lsym := name.Linksym()
	objw.SymPtr(lsym, 0, reflectdata.TypeLinksym(layout), 0)
	objw.Global(lsym, int32(types.PtrSize), obj.RODATA|obj.NOPTR)
}

func isolateGlobalOffset(n *ir.Name) (int64, bool) {
	if n.Sym().Pkg != types.LocalPkg {
		return 0, false
	}
	off, ok := isolateLayoutOffsets[n.Sym()]
	return off, ok
}
