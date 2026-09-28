// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase0_e4_compile

// Package e4compiletoy probes compiler redirection of an initialized global.
// Only its global variable is special-cased by -d=isolatee4. This does not
// implement general package-global rewriting.
package e4compiletoy

import (
	"fmt"
	"internal/isolateproto"
	"sync"
	"unsafe"
)

type Graph struct {
	Count  *int
	Values map[string]int
	Read   func() int
}

type State struct {
	Graph
	Epoch int
}

// The compiler probe rewrites accesses to this variable through the current
// goroutine's base. No source-level accessor is used by init or RunCurrent.
var global Graph
var epoch = 41
var processGlobal Graph    // direct-access comparison for the E4 benchmark
var registerOnce sync.Once // host-owned; process-global by design
var Entry isolateproto.Entry

//go:linkname setBase runtime.isolateE4SetBase
func setBase(unsafe.Pointer) unsafe.Pointer

func init() {
	registerOnce.Do(func() {
		Entry = isolateproto.Register("isolateproto.e4compiletoy.entry", func(_ *isolateproto.Task, _ []byte) ([]byte, error) {
			if EpochCurrent() != 1 {
				return nil, fmt.Errorf("unexpected isolate epoch %d", EpochCurrent())
			}
			return []byte(RunCurrent()), nil
		})
	})
	n := new(int)
	global.Count = n
	global.Values = map[string]int{"n": 0}
	global.Read = func() int { return *n }
	epoch -= 40
}

// The opt-in compiler mode keeps statically representable assignments in the
// generated variable initializer so a fresh base receives them before init.0.
//
//go:linkname rerunVarInit internal/isolateproto/testdata/e4compiletoy.init
func rerunVarInit()

//go:linkname rerunInit internal/isolateproto/testdata/e4compiletoy.init.0
func rerunInit()

func New() *State {
	s := new(State)
	WithBase(s, func() {
		rerunVarInit()
		rerunInit()
	})
	return s
}

func WithBase(s *State, fn func()) {
	old := setBase(unsafe.Pointer(s))
	defer setBase(old)
	fn()
}

func RunCurrent() string {
	*global.Count++
	global.Values["n"]++
	return fmt.Sprintf("%d/%d/%d", *global.Count, global.Values["n"], global.Read())
}

func EpochCurrent() int { return epoch }

func StepCurrent() int {
	*global.Count++
	global.Values["n"] += *global.Count
	return global.Values["n"] + global.Read()
}

func StepExplicit(s *State) int {
	*s.Count++
	s.Values["n"] += *s.Count
	return s.Values["n"] + s.Read()
}

func StepProcessGlobal() int {
	*processGlobal.Count++
	processGlobal.Values["n"] += *processGlobal.Count
	return processGlobal.Values["n"] + processGlobal.Read()
}

func Run(s *State) string {
	var out string
	WithBase(s, func() { out = RunCurrent() })
	return out
}
