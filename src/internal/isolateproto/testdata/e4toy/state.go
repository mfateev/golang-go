// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

// Package e4toy tests whether Go's compiler-generated init function can be
// rerun with a different global-access base. This is a narrow feasibility
// probe, not an implementation of per-isolate globals.
package e4toy

import (
	"fmt"
	"unsafe"
)

type State struct {
	Count  *int
	Values map[string]int
	Read   func() int
}

var bootstrap State

//go:linkname setBase runtime.isolateE4SetBase
func setBase(unsafe.Pointer) unsafe.Pointer

//go:linkname getBase runtime.isolateE4GetBase
func getBase() unsafe.Pointer

func selected() *State {
	if base := getBase(); base != nil {
		return (*State)(base)
	}
	return &bootstrap
}

func init() {
	n := new(int)
	s := selected()
	s.Count = n
	s.Values = map[string]int{"n": 0}
	s.Read = func() int { return *n }
}

// rerunInit calls this package's compiler-generated init function. Go's
// process-wide initTask remains done; this experiment calls the function
// directly while a different state base is selected.
//
//go:linkname rerunInit internal/isolateproto/testdata/e4toy.init.0
func rerunInit()

func New() *State {
	s := new(State)
	WithBase(s, rerunInit)
	return s
}

func WithBase(s *State, fn func()) {
	old := setBase(unsafe.Pointer(s))
	defer setBase(old)
	fn()
}

func RunCurrent() string {
	s := selected()
	*s.Count++
	s.Values["n"]++
	return fmt.Sprintf("%d/%d/%d", *s.Count, s.Values["n"], s.Read())
}

func Run(s *State) string {
	var out string
	WithBase(s, func() { out = RunCurrent() })
	return out
}
