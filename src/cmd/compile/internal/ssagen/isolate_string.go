// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssa/ssaop"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

func (s *state) isolateCheckString(value *ssa.Value) {
	if base.Debug.IsolateHeap == 0 || base.Flag.CompilingRuntime {
		return
	}
	ptr := s.newValue1(ssaop.OpStringPtr, s.f.Config.Types.BytePtr, value)
	length := s.newValue1(ssaop.OpStringLen, types.Types[types.TINT], value)
	s.isolateCheckStringBytes(ptr, length)
}

func (s *state) isolateCheckStringBytes(ptr, length *ssa.Value) {
	if base.Debug.IsolateHeap == 0 || base.Flag.CompilingRuntime {
		return
	}
	width := s.newValue1(ssaop.OpCopy, types.Types[types.TUINTPTR], length)
	s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapAccess"), true, nil, ptr, width, s.constBool(false))
}

// String helpers and equality assembly read backing memory without passing
// through a typed load. Check both ordinary calls and intrinsic expansion.
func (s *state) isolateCheckStringCall(sym *types.Sym, args []*ssa.Value) {
	if base.Debug.IsolateHeap == 0 || base.Flag.CompilingRuntime || sym == nil || sym.Pkg != ir.Pkgs.Runtime {
		return
	}
	switch sym.Name {
	case "cmpstring":
		s.isolateCheckString(args[0])
		s.isolateCheckString(args[1])
	case "concatstring2", "concatstring3", "concatstring4", "concatstring5":
		for _, value := range args[1:] { // Argument zero is the optional stack buffer.
			s.isolateCheckString(value)
		}
	case "concatstrings":
		s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapStrings"), true, nil, args[1])
	case "memequal", "memequal0", "memequal8", "memequal16", "memequal32", "memequal64", "memequal128":
		var width *ssa.Value
		if sym.Name == "memequal" {
			width = args[2]
		} else {
			bytes := map[string]int64{"memequal0": 0, "memequal8": 1, "memequal16": 2, "memequal32": 4, "memequal64": 8, "memequal128": 16}[sym.Name]
			width = s.constInt(types.Types[types.TUINTPTR], bytes)
		}
		for _, ptr := range args[:2] {
			s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapAccess"), true, nil, ptr, width, s.constBool(false))
		}
	}
}
