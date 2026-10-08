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
		if pkg == isolatepolicy.TemporalSDKModule+"/internal" && name == "init" {
			fn.Pragma |= ir.Noinline
			value := AssignConv(types.LocalPkg.Lookup("ErrNoData").Def.(*ir.Name), types.Types[types.TINTER], "SDK error sentinel")
			publish := Stmt(Call(fn.Pos(), LookupRuntime("isolatePublishErrorSentinel", types.Types[types.TINTER]), []ir.Node{value}, false))
			if last, ok := fn.Body[len(fn.Body)-1].(*ir.ReturnStmt); ok {
				last.PtrInit().Append(publish)
			} else {
				fn.Body.Append(publish)
			}
		}
		if pkg == isolatepolicy.ProtobufModule+"/proto" && name == "Clone" {
			cloneIsolateProto(fn)
			continue
		}
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

// The shared coder tables are mutable during lazy initialization. For the
// pinned generated value representation, clone exported fields and unknown
// bytes under the caller's owner, without invoking metadata or user methods.
// Ordinary host execution retains protobuf's original implementation.
func cloneIsolateProto(fn *ir.Func) {
	fn.Pragma |= ir.Noinline
	input := AssignConv(fn.Type().Param(0).Nname.(*ir.Name), types.Types[types.TINTER], "protobuf clone input")
	clone := Call(fn.Pos(), LookupRuntime("isolateCloneProto", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{input}, false)
	result := Expr(ir.NewTypeAssertExpr(fn.Pos(), clone, fn.Type().Result(0).Type))
	ret := ir.NewReturnStmt(fn.Pos(), []ir.Node{result})
	// A nil interface needs the ordinary nil return, rather than an assertion.
	nilInput := Expr(ir.NewBinaryExpr(fn.Pos(), ir.OEQ, fn.Type().Param(0).Nname.(*ir.Name), NodNil()))
	nilReturn := ir.NewReturnStmt(fn.Pos(), []ir.Node{NodNil()})
	body := []ir.Node{Stmt(ir.NewIfStmt(fn.Pos(), nilInput, []ir.Node{Stmt(nilReturn)}, nil)), Stmt(ret)}
	owner := Call(fn.Pos(), LookupRuntime("isolateGetOwner"), nil, false)
	active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, owner, ir.NewInt(fn.Pos(), 0)))
	fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, body, nil)))
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
