// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package isolateproto

import (
	"testing"
	"unsafe"
)

type e4State struct {
	padding [64]byte
	value   uint64
}

var e4Direct uint64
var e4Current = new(e4State)
var e4Base = unsafe.Pointer(e4Current)

type e4WorkflowState struct {
	count  *int
	values map[uint32]int
	read   func() int
}

var e4WorkflowDirectCount *int
var e4WorkflowDirectValues map[uint32]int
var e4WorkflowDirectRead func() int
var e4WorkflowCurrent *e4WorkflowState
var e4WorkflowBase unsafe.Pointer
var e4WorkflowSink int

const (
	e4CountOffset  = unsafe.Offsetof(e4WorkflowState{}.count)
	e4ValuesOffset = unsafe.Offsetof(e4WorkflowState{}.values)
	e4ReadOffset   = unsafe.Offsetof(e4WorkflowState{}.read)
)

func e4NewWorkflowState() *e4WorkflowState {
	n := new(int)
	values := make(map[uint32]int, 16)
	for key := range uint32(16) {
		values[key] = 0
	}
	return &e4WorkflowState{count: n, values: values, read: func() int { return *n }}
}

func e4WorkflowCur() *e4WorkflowState { return e4WorkflowCurrent }

const e4Offset = unsafe.Offsetof(e4State{}.value)

func e4Cur() *e4State { return e4Current }

// These microbenchmarks compare instruction shapes for one mutable global.
// They do not measure generated compiler indirection or per-isolate init.
func BenchmarkE4Direct(b *testing.B) {
	for range b.N {
		e4Direct++
	}
}

func BenchmarkE4Accessor(b *testing.B) {
	for range b.N {
		e4Cur().value++
	}
}

func BenchmarkE4BaseOffset(b *testing.B) {
	for range b.N {
		p := (*uint64)(unsafe.Add(e4Base, e4Offset))
		*p++
	}
}

// The workflow-shaped benchmarks update a small fan-out result map, mutate an
// init-created pointer, and read an init-created closure. They isolate global
// access cost with one state already selected; they do not include a scheduler
// switch, generated compiler indirection, or per-isolate init.
func BenchmarkE4WorkflowDirect(b *testing.B) {
	s := e4NewWorkflowState()
	e4WorkflowDirectCount, e4WorkflowDirectValues, e4WorkflowDirectRead = s.count, s.values, s.read
	b.ResetTimer()
	var sink int
	for j := range b.N {
		key := uint32(j) & 15
		*e4WorkflowDirectCount++
		e4WorkflowDirectValues[key] += *e4WorkflowDirectCount
		sink += e4WorkflowDirectRead()
	}
	e4WorkflowSink = sink
}

func BenchmarkE4WorkflowAccessor(b *testing.B) {
	e4WorkflowCurrent = e4NewWorkflowState()
	b.ResetTimer()
	var sink int
	for j := range b.N {
		s := e4WorkflowCur()
		key := uint32(j) & 15
		*s.count++
		s.values[key] += *s.count
		sink += s.read()
	}
	e4WorkflowSink = sink
}

func BenchmarkE4WorkflowBaseOffset(b *testing.B) {
	e4WorkflowBase = unsafe.Pointer(e4NewWorkflowState())
	b.ResetTimer()
	var sink int
	for j := range b.N {
		count := *(**int)(unsafe.Add(e4WorkflowBase, e4CountOffset))
		values := *(*map[uint32]int)(unsafe.Add(e4WorkflowBase, e4ValuesOffset))
		read := *(*func() int)(unsafe.Add(e4WorkflowBase, e4ReadOffset))
		key := uint32(j) & 15
		*count++
		values[key] += *count
		sink += read()
	}
	e4WorkflowSink = sink
}
