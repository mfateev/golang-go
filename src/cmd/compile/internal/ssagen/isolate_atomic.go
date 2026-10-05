// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Atomic intrinsics and out-of-line assembly bypass ordinary load/store
// instrumentation. Check direct calls at their common compiler boundaries;
// this also covers inlined typed atomic methods and race-intercepted calls.
func (s *state) isolateCheckAtomic(sym *types.Sym, args []*ssa.Value) {
	if base.Debug.IsolateHeap == 0 || base.Flag.CompilingRuntime || sym == nil || sym.Pkg == nil || sym.Pkg.Path != "sync/atomic" {
		return
	}
	name := sym.Name
	var suffix string
	write := true
	publish := -1
	switch {
	case strings.HasPrefix(name, "Load"):
		suffix, write = name[len("Load"):], false
	case strings.HasPrefix(name, "Store"):
		suffix, publish = name[len("Store"):], 1
	case strings.HasPrefix(name, "Swap"):
		suffix, publish = name[len("Swap"):], 1
	case strings.HasPrefix(name, "CompareAndSwap"):
		suffix, publish = name[len("CompareAndSwap"):], 2
	case strings.HasPrefix(name, "Add"):
		suffix = name[len("Add"):]
	case strings.HasPrefix(name, "And"):
		suffix = name[len("And"):]
	case strings.HasPrefix(name, "Or"):
		suffix = name[len("Or"):]
	default:
		return
	}
	var width int64
	switch suffix {
	case "Int32", "Uint32":
		width = 4
	case "Int64", "Uint64":
		width = 8
	case "Uintptr", "Pointer":
		width = s.config.PtrSize
	default:
		return
	}
	s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapAccess"), true, nil,
		args[0], s.constInt(types.Types[types.TUINTPTR], width), s.constBool(write))
	if suffix == "Pointer" && publish >= 0 && base.Debug.IsolateHeap > 1 {
		s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapReference"), true, nil, args[0], args[publish])
	}
}
