// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssagen

import (
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/ssa"
	"cmd/compile/internal/ssa/ssaop"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Map ownership alone does not validate the key copied or hashed by the helper.
// Keep these checks before the helper can acquire locks or mutate its slots.
func (s *state) isolateCheckMapKey(name string, args []*ssa.Value, assign bool) {
	publish := assign && base.Debug.IsolateHeap > 1
	switch {
	case strings.HasSuffix(name, "_faststr"):
		key := args[2]
		ptr := s.newValue1(ssaop.OpStringPtr, s.f.Config.Types.BytePtr, key)
		length := s.newValue1(ssaop.OpStringLen, types.Types[types.TINT], key)
		width := s.newValue1(ssaop.OpCopy, types.Types[types.TUINTPTR], length)
		s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapAccess"), true, nil, ptr, width, s.constBool(false))
		if publish {
			s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapReference"), true, nil, args[1], ptr)
		}
	case strings.HasSuffix(name, "_fast32ptr"), strings.HasSuffix(name, "_fast64ptr"):
		if publish {
			s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapReference"), true, nil, args[1], args[2])
		}
	case !strings.Contains(name, "_fast"):
		s.rtcall(typecheck.LookupRuntimeFunc("isolateCheckHeapMapKey"), true, nil, args[0], args[1], args[2], s.constBool(publish))
	}
}
