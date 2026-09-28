// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout

package e4layouttoy_test

import (
	"internal/isolateproto/testdata/e4layouttoy"
	"sync"
	"testing"
	"unsafe"
)

//go:linkname layoutType internal/isolateproto/testdata/e4layouttoy.isolateLayoutType
var layoutType unsafe.Pointer

//go:linkname newState runtime.isolateE4NewState
func newState(unsafe.Pointer) unsafe.Pointer

//go:linkname setBase runtime.isolateE4SetBase
func setBase(unsafe.Pointer) unsafe.Pointer

//go:linkname rerunVarInit internal/isolateproto/testdata/e4layouttoy.init
func rerunVarInit()

//go:linkname rerunInit internal/isolateproto/testdata/e4layouttoy.init.0
func rerunInit()

func withBase(base unsafe.Pointer, fn func()) {
	old := setBase(base)
	defer setBase(old)
	fn()
}

func newInstance(t *testing.T) unsafe.Pointer {
	t.Helper()
	if layoutType == nil {
		t.Fatal("compiler did not emit a runtime type for the isolate layout")
	}
	state := newState(layoutType)
	withBase(state, func() {
		rerunVarInit()
		rerunInit()
	})
	return state
}

func TestGeneratedLayoutSeparatesInitializedGlobals(t *testing.T) {
	a, b := newInstance(t), newInstance(t)
	if a == b {
		t.Fatal("instances share their global storage")
	}
	for _, step := range []struct {
		state unsafe.Pointer
		want  string
	}{
		{a, "1/1/1/1"}, {b, "1/1/1/1"},
		{a, "2/2/2/1"}, {b, "2/2/2/1"},
	} {
		withBase(step.state, func() {
			if got := e4layouttoy.RunCurrent(); got != step.want {
				t.Errorf("RunCurrent=%q, want %q", got, step.want)
			}
		})
	}
}

func TestGeneratedLayoutInheritsBase(t *testing.T) {
	state := newInstance(t)
	withBase(state, func() {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := e4layouttoy.RunCurrent(); got != "1/1/1/1" {
				t.Errorf("child RunCurrent=%q", got)
			}
		}()
		wg.Wait()
	})
}
