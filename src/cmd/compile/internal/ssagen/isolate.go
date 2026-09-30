// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"cmp"
	"slices"
	"strings"

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
		// An importing compiler invocation cannot know this package's
		// layout offsets. Export one process-owned offset symbol per
		// global, including unexported globals referenced by exported
		// generic bodies instantiated in an importing package.
		offsetSym := typecheck.Lookup("isolate$offset$" + n.Sym().Name)
		if offsetSym.Def != nil {
			base.Fatalf("isolate: source declaration conflicts with generated offset symbol")
		}
		offsetName := ir.NewNameAt(base.Pos, offsetSym, types.Types[types.TUINTPTR])
		offsetName.Class = ir.PEXTERN
		offsetSym.Def = offsetName
		offsetLSym := offsetName.Linksym()
		offsetLSym.Set(obj.AttrLinkname, true)
		objw.Uintptr(offsetLSym, 0, uint64(fields[i].Offset))
		objw.Global(offsetLSym, int32(types.PtrSize), obj.RODATA|obj.NOPTR)
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
	lsym.Set(obj.AttrLinkname, true)
	objw.SymPtr(lsym, 0, reflectdata.TypeLinksym(layout), 0)
	objw.Global(lsym, int32(types.PtrSize), obj.RODATA|obj.NOPTR)

	// Use a package-specific symbol as the tagged runtime probe's key.
	// Equal layout types may be deduplicated by the linker, so the type
	// pointer itself cannot identify the owning package.
	keySym := typecheck.Lookup("isolateLayoutKey")
	if keySym.Def != nil {
		base.Fatalf("isolate: source declaration conflicts with generated layout key symbol")
	}
	keyName := ir.NewNameAt(base.Pos, keySym, types.Types[types.TUINT8])
	keyName.Class = ir.PEXTERN
	keySym.Def = keyName
	keyLSym := keyName.Linksym()
	keyLSym.Set(obj.AttrLinkname, true)
	objw.Uint8(keyLSym, 0, 0)
	objw.Global(keyLSym, 1, obj.RODATA|obj.NOPTR)
}

func isolateGlobalOffset(n *ir.Name) (int64, bool) {
	if n.Sym().Pkg != types.LocalPkg {
		return 0, false
	}
	off, ok := isolateLayoutOffsets[n.Sym()]
	return off, ok
}

func isolatePackageKey() *obj.LSym {
	return typecheck.Lookup("isolateLayoutKey").Def.(*ir.Name).Linksym()
}

func isolateImportedGlobal(n *ir.Name) bool {
	pkg := n.Sym().Pkg
	// Generic dictionaries are immutable compiler metadata, not source
	// package variables. They have no slot in the selected package layout.
	return pkg != nil && pkg != types.LocalPkg && !strings.HasPrefix(n.Sym().Name, ".dict.") && base.IsolateImportSelected(pkg.Path)
}

func isolateImportedKey(n *ir.Name) *obj.LSym {
	return n.Sym().Pkg.Lookup("isolateLayoutKey").Linksym()
}

func isolateImportedOffset(n *ir.Name) *obj.LSym {
	return n.Sym().Pkg.Lookup("isolate$offset$" + n.Sym().Name).Linksym()
}
