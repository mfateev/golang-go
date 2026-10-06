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
		if pkg == isolatepolicy.ProtobufModule+"/internal/filetype" && name == "Builder.Build" {
			publishIsolateMessageInfos(fn)
			continue
		}
		if isolatepolicy.RejectedMetadata(pkg, name) {
			fn.Pragma |= ir.Noinline
			call := Call(fn.Pos(), LookupRuntime("isolateRejectMetadataAPI"), []ir.Node{ir.NewString(fn.Pos(), pkg+"."+name)}, false)
			fn.Body.Prepend(Stmt(call))
		} else if isolatepolicy.MetadataScope(pkg, name) {
			fn.Pragma |= ir.Noinline
			var guards ir.Nodes
			var descriptorGuards ir.Nodes
			if receiver := fn.Type().Recv(); receiver != nil {
				rcvr := receiver.Nname.(*ir.Name)
				ptr := Expr(ir.NewConvExpr(fn.Pos(), ir.OCONV, types.Types[types.TUNSAFEPTR], rcvr))
				guards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataReceiver"), []ir.Node{ptr}, false)))
				if name == "(*MessageInfo).init" || name == "(*MessageInfo).initOnce" || name == "(*MessageInfo).Descriptor" {
					descriptor := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, rcvr, types.LocalPkg.Lookup("Desc")))
					descriptor = AssignConv(descriptor, types.Types[types.TINTER], "metadata descriptor")
					// The receiver guard precedes service entry. Its shared Desc
					// field must be read after entry, with Leave already deferred.
					descriptorGuards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataDescriptor", types.Types[types.TINTER]), []ir.Node{descriptor}, false)))
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
			guards = append(guards, descriptorGuards...)
			fn.Body = append(guards, fn.Body...)
		}
	}
}

// The pinned builder populates and registers MessageInfos before its final
// return. Only its default process registry grants canonical sharing. Private
// builders and custom registries do not gain this privilege.
func publishIsolateMessageInfos(fn *ir.Func) {
	fn.Pragma |= ir.Noinline
	rcvr := fn.Type().Recv().Nname.(*ir.Name)
	registry := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, rcvr, types.LocalPkg.Lookup("TypeRegistry")))
	global := TempAt(fn.Pos(), fn, types.Types[types.TBOOL])
	assignment := Stmt(ir.NewAssignStmt(fn.Pos(), global, Expr(ir.NewBinaryExpr(fn.Pos(), ir.OEQ, registry, NodNil()))))
	reject := Stmt(Call(fn.Pos(), LookupRuntime("isolateRejectMetadataAPI"), []ir.Node{ir.NewString(fn.Pos(), "google.golang.org/protobuf/internal/filetype.Builder.Build")}, false))
	fn.Body.Prepend(reject, assignment)
	last, ok := fn.Body[len(fn.Body)-1].(*ir.ReturnStmt)
	if !ok {
		base.FatalfAt(fn.Pos(), "isolate: audited protobuf builder must finish with a return")
	}
	infos := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, rcvr, types.LocalPkg.Lookup("MessageInfos")))
	value := AssignConv(infos, types.Types[types.TINTER], "canonical message infos")
	publish := Stmt(Call(fn.Pos(), LookupRuntime("isolatePublishMessageInfos", types.Types[types.TINTER]), []ir.Node{value}, false))
	conditional := ir.NewIfStmt(fn.Pos(), global, []ir.Node{publish}, nil)
	last.PtrInit().Append(Stmt(conditional))
}
