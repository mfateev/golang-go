// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/isolatepolicy"
)

// ScopeIsolateMetadata runs before inlining and escape analysis. The service
// exit is the first defer, so existing Unlock defers run before it, including
// panic paths. Prevent inlining from exporting a privilege-changing body.
func ScopeIsolateMetadata() {
	if base.Debug.IsolateMetadata == 0 {
		return
	}
	previous := ir.CurFunc
	defer func() { ir.CurFunc = previous }()
	for _, fn := range Target.Funcs {
		pkg, name := base.Ctxt.Pkgpath, ir.FuncName(fn)
		if len(fn.Body) == 0 {
			continue
		}
		ir.CurFunc = fn
		if isolatepolicy.RejectedMetadata(pkg, name) {
			fn.Pragma |= ir.Noinline
			call := Call(fn.Pos(), LookupRuntime("isolateRejectMetadataAPI"), []ir.Node{ir.NewString(fn.Pos(), pkg+"."+name)}, false)
			fn.Body.Prepend(Stmt(call))
		} else if isolatepolicy.MetadataScope(pkg, name) {
			fn.Pragma |= ir.Noinline
			var guards ir.Nodes
			if receiver := fn.Type().Recv(); receiver != nil {
				rcvr := receiver.Nname.(*ir.Name)
				ptr := Expr(ir.NewConvExpr(fn.Pos(), ir.OCONV, types.Types[types.TUNSAFEPTR], rcvr))
				guards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataReceiver"), []ir.Node{ptr}, false)))
				if name == "(*MessageInfo).initOnce" {
					descriptor := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, rcvr, types.LocalPkg.Lookup("Desc")))
					descriptor = AssignConv(descriptor, types.Types[types.TINTER], "metadata descriptor")
					guards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataDescriptor", types.Types[types.TINTER]), []ir.Node{descriptor}, false)))
				}
			} else if name == "needsInitCheck" {
				descriptor := AssignConv(fn.Type().Param(0).Nname.(*ir.Name), types.Types[types.TINTER], "metadata descriptor")
				guards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataDescriptor", types.Types[types.TINTER]), []ir.Node{descriptor}, false)))
			}
			owner := TempAt(fn.Pos(), fn, types.Types[types.TUINTPTR])
			enter := Call(fn.Pos(), LookupRuntime("isolateEnterMetadata"), nil, false)
			assignment := Stmt(ir.NewAssignStmt(fn.Pos(), owner, enter))
			leave := Call(fn.Pos(), LookupRuntime("isolateLeaveMetadata"), []ir.Node{owner}, false)
			deferred := Stmt(ir.NewGoDeferStmt(fn.Pos(), ir.ODEFER, leave))
			guards.Append(assignment, deferred)
			fn.Body = append(guards, fn.Body...)
		}
	}
}
