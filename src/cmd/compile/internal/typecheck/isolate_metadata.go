// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typecheck

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/types"
	"cmd/internal/isolatepolicy"
	"strings"
)

// A rewritten generated accessor has a checked native branch, followed by its
// original host body. Effect rejection belongs after that branch; the native
// branch itself contains only compiler-created calls or a verified zero store.
var isolateProtoNativePrefixes = make(map[*ir.Func]bool)

func prependProtoNative(fn *ir.Func, branch ir.Node) {
	fn.Body.Prepend(branch)
	isolateProtoNativePrefixes[fn] = true
}

// ScopeIsolateMetadata runs before inlining and escape analysis. The service
// exit is the first defer, so existing Unlock defers run before it, including
// panic paths. Prevent inlining from exporting a privilege-changing body.
func ScopeIsolateMetadata() {
	if base.Debug.IsolateMetadata == 0 {
		return
	}
	// Identify descriptor initializers by the audited builder call, rather than
	// exempting arbitrary application functions based on a generated filename.
	schemaInit := make(map[*types.Sym]bool)
	for _, candidate := range Target.Funcs {
		name := ir.FuncName(candidate)
		if !strings.HasPrefix(name, "file_") || !strings.HasSuffix(name, "_init") {
			continue
		}
		ir.VisitList(candidate.Body, func(node ir.Node) {
			call, ok := node.(*ir.CallExpr)
			if !ok || len(call.Args) == 0 {
				return
			}
			selector, ok := call.Fun.(*ir.SelectorExpr)
			if !ok || selector.Sel.Name != "Build" {
				return
			}
			receiver := call.Args[0].Type()
			if receiver.IsPtr() {
				receiver = receiver.Elem()
			}
			if symbol := receiver.Sym(); symbol != nil && symbol.Pkg.Path == isolatepolicy.ProtobufModule+"/internal/filetype" && symbol.Name == "Builder" {
				schemaInit[candidate.Nname.Sym()] = true
			}
		})
	}
	previous := ir.CurFunc
	defer func() { ir.CurFunc = previous }()
	for _, fn := range Target.Funcs {
		pkg, name := base.Ctxt.Pkgpath, ir.FuncName(fn)
		if len(fn.Body) == 0 {
			continue
		}
		ir.CurFunc = fn
		if (pkg == isolatepolicy.ProtobufModule+"/encoding/protojson" && (name == "MarshalOptions.marshal" || name == "UnmarshalOptions.unmarshal")) || pkg == isolatepolicy.ProtobufModule+"/proto" && name == "UnmarshalOptions.unmarshal" {
			fn.Pragma |= ir.Noinline
			receiver := fn.Type().Recv().Nname.(*ir.Name)
			resolver := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, receiver, types.LocalPkg.Lookup("Resolver")))
			fn.Body.Prepend(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckProtoResolver", types.Types[types.TINTER]), []ir.Node{AssignConv(resolver, types.Types[types.TINTER], "protobuf resolver")}, false)))
		}

		if pkg == isolatepolicy.ProtobufModule+"/internal/impl" && (name == "Export.MessageDescriptorOf" || name == "Export.EnumDescriptorOf") {
			// Descriptor dependency resolution must never invoke application methods
			// under process privileges. Use the process builder's canonical Go-type map.
			fn.Pragma |= ir.Noinline
			active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, Call(fn.Pos(), LookupRuntime("isolateMetadataBorrowOwner"), nil, false), ir.NewInt(fn.Pos(), 0)))
			active = Expr(ir.NewLogicalExpr(fn.Pos(), ir.OOROR, active, Call(fn.Pos(), LookupRuntime("isolateProtoCacheBuilding"), nil, false)))
			infoType := types.LocalPkg.Lookup("MessageInfo").Def.(*ir.Name).Type()
			if name == "Export.EnumDescriptorOf" {
				infoType = types.LocalPkg.Lookup("EnumInfo").Def.(*ir.Name).Type()
			}
			value := AssignConv(fn.Type().Param(0).Nname.(*ir.Name), types.Types[types.TINTER], "generated descriptor dependency")
			info := Call(fn.Pos(), LookupRuntime("isolateCanonicalProtoInfo", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{value}, false)
			pointer := Expr(ir.NewTypeAssertExpr(fn.Pos(), info, types.NewPtr(infoType)))
			desc := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, pointer, types.LocalPkg.Lookup("Desc")))
			fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{desc}))}, nil)))
		}

		if pkg == isolatepolicy.ProtobufModule+"/internal/impl" && name == "init.func1" && strings.HasSuffix(base.Ctxt.PosTable.Pos(fn.Pos()).Base().Filename(), "/lazy.go") {
			fn.Pragma |= ir.Noinline
			active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, Call(fn.Pos(), LookupRuntime("isolateGetOwner"), nil, false), ir.NewInt(fn.Pos(), 0)))
			fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{ir.NewInt(fn.Pos(), 0)}))}, nil)))
		}
		if pkg == isolatepolicy.ProtobufModule+"/internal/detrand" && name == "binaryHash" {
			fn.Pragma |= ir.Noinline
			active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, Call(fn.Pos(), LookupRuntime("isolateGetOwner"), nil, false), ir.NewInt(fn.Pos(), 0)))
			fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{ir.NewInt(fn.Pos(), 0)}))}, nil)))
		}
		if pkg == isolatepolicy.ProtobufModule+"/internal/detrand" && (name == "Bool" || name == "Intn") {
			// Shared reflection tables must have the same order regardless of
			// whether their first caller is the host or an isolate.
			fn.Pragma |= ir.Noinline
			active := Call(fn.Pos(), LookupRuntime("isolateProtoCacheBuilding"), nil, false)
			zero := ir.NewInt(fn.Pos(), 0)
			if name == "Bool" {
				fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{ir.NewBool(fn.Pos(), false)}))}, nil)))
			} else {
				// Preserve the original nonpositive-bound panic.
				positive := Expr(ir.NewBinaryExpr(fn.Pos(), ir.OGT, fn.Type().Param(0).Nname.(*ir.Name), zero))
				active = Expr(ir.NewLogicalExpr(fn.Pos(), ir.OANDAND, active, positive))
				fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{zero}))}, nil)))
			}
		}
		generated := strings.HasSuffix(base.Ctxt.PosTable.Pos(fn.Pos()).Base().Filename(), ".pb.go")
		if generated {
			var existingGuard ir.Node
			if call, ok := fn.Body[0].(*ir.CallExpr); ok {
				if target := ir.StaticCalleeName(call.Fun); target != nil && target.Sym() == LookupRuntime("isolateRejectEffect").Sym() {
					existingGuard = fn.Body[0]
					fn.Body = fn.Body[1:]
				}
			}
			active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, Call(fn.Pos(), LookupRuntime("isolateGetOwner"), nil, false), ir.NewInt(fn.Pos(), 0)))
			schema := schemaInit[fn.Nname.Sym()]
			if !schema && strings.HasPrefix(name, "init.") && len(fn.Body) == 1 {
				if call, ok := fn.Body[0].(*ir.CallExpr); ok {
					if callee := ir.StaticCalleeName(call.Fun); callee != nil {
						schema = schemaInit[callee.Sym()]
					}
				}
			}
			if fn.Type().Recv() == nil && schema {
				fn.Pragma |= ir.Noinline
				prependProtoNative(fn, Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), nil))}, nil)))
			}
			if fn.Type().Recv() != nil {
				receiver := fn.Type().Recv().Nname.(*ir.Name)
				canonical := func(target *types.Type) ir.Node {
					value := AssignConv(receiver, types.Types[types.TINTER], "generated protobuf receiver")
					info := Call(fn.Pos(), LookupRuntime("isolateCanonicalProtoInfo", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{value}, false)
					return Expr(ir.NewTypeAssertExpr(fn.Pos(), info, target))
				}
				var result ir.Node
				if last, ok := fn.Body[len(fn.Body)-1].(*ir.ReturnStmt); ok && len(last.Results) == 1 {
					if call, ok := last.Results[0].(*ir.CallExpr); ok && len(call.Args) != 0 {
						if selector, ok := call.Fun.(*ir.SelectorExpr); ok && (strings.HasSuffix(name, ").ProtoReflect") && selector.Sel.Name == "MessageOf" || strings.HasSuffix(name, ".Descriptor") && receiver.Type().Kind() == types.TINT32 && selector.Sel.Name == "Descriptor") {
							if strings.HasSuffix(name, ").ProtoReflect") {
								if len(call.Args) != 2 {
									continue
								}
								arg, ok := call.Args[1].(*ir.ConvExpr)
								if !ok || arg.X != receiver {
									continue
								}
							}
							copy := ir.Copy(call).(*ir.CallExpr)
							copy.Args = append(ir.Nodes(nil), call.Args...)
							copy.Args[0] = canonical(call.Args[0].Type())
							result = copy
						}
					}
					if strings.HasSuffix(name, ".Type") && receiver.Type().Kind() == types.TINT32 {
						if conv, ok := last.Results[0].(*ir.ConvExpr); ok {
							result = AssignConv(canonical(conv.X.Type()), fn.Type().Result(0).Type, "generated enum type")
						}
					}
				}
				if result != nil {
					fn.Pragma |= ir.Noinline
					prependProtoNative(fn, Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{result}))}, nil)))
				}
				if strings.HasSuffix(name, ").Reset") {
					if assign, ok := fn.Body[0].(*ir.AssignStmt); ok && assign.X.Op() == ir.ODEREF {
						star, ok := assign.X.(*ir.StarExpr)
						zero, empty := assign.Y.(*ir.CompLitExpr)
						if !ok || star.X != receiver || !empty || zero.Op() != ir.OSTRUCTLIT || len(zero.List) != 0 {
							continue
						}
						fn.Pragma |= ir.Noinline
						prependProtoNative(fn, Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{ir.Copy(assign), Stmt(ir.NewReturnStmt(fn.Pos(), nil))}, nil)))
					}
				}
			}
			if existingGuard != nil {
				if isolateProtoNativePrefixes[fn] {
					fn.Body = append(fn.Body[:1], append([]ir.Node{existingGuard}, fn.Body[1:]...)...)
				} else {
					fn.Body.Prepend(existingGuard)
				}
			}

		}
		if pkg == isolatepolicy.ProtobufModule+"/internal/impl" && name == "(*MessageInfo).MessageOf" {
			fn.Pragma |= ir.Noinline
			receiver := fn.Type().Recv().Nname.(*ir.Name)
			input := AssignConv(receiver, types.Types[types.TINTER], "protobuf message info")
			shared := Call(fn.Pos(), LookupRuntime("isolateSharedMessageInfo", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{input}, false)
			result := Expr(ir.NewTypeAssertExpr(fn.Pos(), shared, receiver.Type()))
			active := Expr(ir.NewBinaryExpr(fn.Pos(), ir.ONE, Call(fn.Pos(), LookupRuntime("isolateGetOwner"), nil, false), ir.NewInt(fn.Pos(), 0)))
			init := ir.NewSelectorExpr(fn.Pos(), ir.ODOT, receiver, types.LocalPkg.Lookup("init"))
			callInit := Stmt(ir.NewCallExpr(fn.Pos(), ir.OCALL, init, nil))
			// A nil reflective message is a caller-owned wrapper too. Do not
			// allocate it into MessageInfo's separately lazy process nil cache.
			last := fn.Body[len(fn.Body)-1].(*ir.ReturnStmt)
			for _, node := range fn.Body {
				if branch, ok := node.(*ir.IfStmt); ok {
					for _, inner := range branch.Body {
						if ret, ok := inner.(*ir.ReturnStmt); ok && len(ret.Results) == 1 {
							if call, ok := ret.Results[0].(*ir.CallExpr); ok {
								if sel, ok := call.Fun.(*ir.SelectorExpr); ok && sel.Sel.Name == "Init" {
									branch.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), ir.DeepCopy(fn.Pos(), active), []ir.Node{ir.DeepCopy(fn.Pos(), last)}, nil)))
								}
							}
						}
					}
				}
			}
			fn.Body.Prepend(Stmt(ir.NewAssignStmt(fn.Pos(), receiver, result)), Stmt(ir.NewIfStmt(fn.Pos(), active, []ir.Node{callInit}, nil)))
		}
		if pkg == isolatepolicy.ProtobufModule+"/internal/impl" && name == "(*MessageInfo).initOnce" {
			publishSharedProtoCache(fn)
		}
		if pkg == isolatepolicy.ProtobufModule+"/internal/impl" && name == "getMessageInfo" {
			fn.Pragma |= ir.Noinline
			value := AssignConv(fn.Type().Param(0).Nname.(*ir.Name), types.Types[types.TINTER], "protobuf child Go type")
			info := TempAt(fn.Pos(), fn, types.Types[types.TINTER])
			lookup := Call(fn.Pos(), LookupRuntime("isolateProtoInfoForReflectType", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{value}, false)
			empty := Expr(ir.NewBinaryExpr(fn.Pos(), ir.OEQ, info, NodNil()))
			result := Expr(ir.NewTypeAssertExpr(fn.Pos(), info, fn.Type().Result(0).Type))
			body := []ir.Node{
				Stmt(ir.NewAssignStmt(fn.Pos(), info, lookup)),
				Stmt(ir.NewIfStmt(fn.Pos(), empty, []ir.Node{Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{NodNil()}))}, nil)),
				Stmt(ir.NewReturnStmt(fn.Pos(), []ir.Node{result})),
			}
			active := Call(fn.Pos(), LookupRuntime("isolateProtoCacheBuilding"), nil, false)
			fn.Body.Prepend(Stmt(ir.NewIfStmt(fn.Pos(), active, body, nil)))
		}
		if pkg == isolatepolicy.ProtobufModule+"/reflect/protoregistry" && name == "init" {
			fn.Pragma |= ir.Noinline
			for _, symbol := range []string{"GlobalTypes", "GlobalFiles"} {
				value := AssignConv(types.LocalPkg.Lookup(symbol).Def.(*ir.Name), types.Types[types.TINTER], "protobuf registry")
				publish := Stmt(Call(fn.Pos(), LookupRuntime("isolatePublishProtoRegistry", types.Types[types.TINTER]), []ir.Node{value}, false))
				if last, ok := fn.Body[len(fn.Body)-1].(*ir.ReturnStmt); ok {
					last.PtrInit().Append(publish)
				} else {
					fn.Body.Append(publish)
				}
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
			privateCache := pkg == isolatepolicy.ProtobufModule+"/internal/impl" && (name == "(*MessageInfo).init" || name == "(*MessageInfo).initOnce" || name == "(*MessageInfo).Descriptor") || pkg == isolatepolicy.ProtobufModule+"/internal/filedesc" && fn.Type().Recv() != nil
			var receiverPointer ir.Node
			if receiver := fn.Type().Recv(); receiver != nil {
				rcvr := receiver.Nname.(*ir.Name)
				ptr := Expr(ir.NewConvExpr(fn.Pos(), ir.OCONV, types.Types[types.TUNSAFEPTR], rcvr))
				receiverPointer = ptr
				if !privateCache {
					guards.Append(Stmt(Call(fn.Pos(), LookupRuntime("isolateCheckMetadataReceiver"), []ir.Node{ptr}, false)))
				}
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
			if fn.Type().NumResults() == 1 && (fn.Type().Result(0).Type.Kind() == types.TSTRING || fn.Type().Result(0).Type.Kind() == types.TINTER || pkg == isolatepolicy.ProtobufModule+"/internal/filedesc" && name == "(*Field).Default") {
				var copyReturns func(ir.Node) ir.Node
				copyReturns = func(node ir.Node) ir.Node {
					if _, closure := node.(*ir.ClosureExpr); closure {
						return node
					}
					if ret, ok := node.(*ir.ReturnStmt); ok && len(ret.Results) == 1 {
						original := ret.Results[0]
						if original.Type().Kind() == types.TSTRING {
							str := Expr(ir.NewConvExpr(original.Pos(), ir.OCONV, types.Types[types.TSTRING], original))
							copied := Call(original.Pos(), LookupRuntime("isolateCopyMetadataReturnString"), []ir.Node{owner, str}, false)
							ret.Results[0] = Expr(ir.NewConvExpr(original.Pos(), ir.OCONV, original.Type(), copied))
						} else if original.Type().Kind() == types.TSTRUCT {
							value := AssignConv(original, types.Types[types.TINTER], "protobuf default value")
							copied := Call(original.Pos(), LookupRuntime("isolateCopyMetadataProtoValue", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{owner, value}, false)
							ret.Results[0] = Expr(ir.NewTypeAssertExpr(original.Pos(), copied, original.Type()))
						} else {
							target := fn.Type().Result(0).Type
							temp := TempAt(original.Pos(), fn, target)
							ret.PtrInit().Append(Stmt(ir.NewAssignStmt(original.Pos(), temp, AssignConv(original, target, "protobuf descriptor result"))))
							value := AssignConv(temp, types.Types[types.TINTER], "protobuf descriptor handle")
							copied := Call(original.Pos(), LookupRuntime("isolatePublishMetadataReturn", types.Types[types.TINTER], types.Types[types.TINTER]), []ir.Node{owner, value}, false)
							ret.PtrInit().Append(Stmt(copied))
							ret.Results[0] = temp
						}
						return ret
					}
					ir.EditChildren(node, copyReturns)
					return node
				}
				for i, node := range fn.Body {
					fn.Body[i] = copyReturns(node)
				}
			}
			enter := Call(fn.Pos(), LookupRuntime("isolateEnterMetadata"), nil, false)
			if privateCache {
				enter = Call(fn.Pos(), LookupRuntime("isolateEnterMessageInfoMetadata"), []ir.Node{receiverPointer}, false)
			}
			assignment := Stmt(ir.NewAssignStmt(fn.Pos(), owner, enter))
			leave := Call(fn.Pos(), LookupRuntime("isolateLeaveMetadata"), []ir.Node{owner}, false)
			if privateCache {
				leave = Call(fn.Pos(), LookupRuntime("isolateLeaveMessageInfoMetadata"), []ir.Node{owner}, false)
			}
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

// Preserve protobuf's initMu/atomic initDone protocol. Freeze only completed
// cache allocations, immediately before publication to concurrent readers.
func publishSharedProtoCache(fn *ir.Func) {
	receiver := fn.Type().Recv().Nname.(*ir.Name)
	value := AssignConv(receiver, types.Types[types.TINTER], "shared protobuf cache")
	token := TempAt(fn.Pos(), fn, types.Types[types.TUNSAFEPTR])
	begin := Call(fn.Pos(), LookupRuntime("isolateBeginProtoInitialization", types.Types[types.TINTER]), []ir.Node{value}, false)
	end := Call(fn.Pos(), LookupRuntime("isolateEndProtoInitialization"), []ir.Node{token}, false)
	fn.Body.Prepend(Stmt(ir.NewAssignStmt(fn.Pos(), token, begin)), Stmt(ir.NewGoDeferStmt(fn.Pos(), ir.ODEFER, end)))
	var edit func(ir.Node) ir.Node
	edit = func(node ir.Node) ir.Node {
		if _, closure := node.(*ir.ClosureExpr); closure {
			return node
		}
		if call, ok := node.(*ir.CallExpr); ok {
			if target := ir.StaticCalleeName(call.Fun); target != nil && target.Sym().Pkg.Path == "sync/atomic" && target.Sym().Name == "StoreUint32" {
				publish := Call(call.Pos(), LookupRuntime("isolatePublishProtoCache", types.Types[types.TINTER]), []ir.Node{value}, false)
				call.PtrInit().Append(Stmt(publish))
			}
		}
		ir.EditChildren(node, edit)
		return node
	}
	for i, node := range fn.Body {
		fn.Body[i] = edit(node)
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
	enums := Expr(ir.NewSelectorExpr(fn.Pos(), ir.ODOT, rcvr, types.LocalPkg.Lookup("EnumInfos")))
	enumValues := AssignConv(enums, types.Types[types.TINTER], "canonical enum infos")
	enumPublish := Stmt(Call(fn.Pos(), LookupRuntime("isolatePublishEnumInfos", types.Types[types.TINTER]), []ir.Node{enumValues}, false))
	last.PtrInit().Append(Stmt(ir.NewIfStmt(fn.Pos(), global, []ir.Node{enumPublish}, nil)))
}
