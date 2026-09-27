// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase0_e4_compile

package e4compiletoy

import (
	"context"
	"internal/isolateproto"
	"sync"
	"testing"
	"unsafe"
)

var benchmarkSink int

func TestCompilerRedirectsInitializedGlobal(t *testing.T) {
	a, b := New(), New()
	if got := unsafe.Offsetof(State{}.Epoch); got != 3*unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("epoch offset = %d, compiler toy expects three pointer words", got)
	}
	if a.Count == b.Count || a.Values == nil || b.Values == nil {
		t.Fatal("initialized object graphs overlap or are incomplete")
	}
	if a.Epoch != 1 || b.Epoch != 1 {
		t.Fatalf("initializer epochs: a=%d b=%d", a.Epoch, b.Epoch)
	}
	WithBase(a, func() { epoch = 7 })
	if a.Epoch != 7 || b.Epoch != 1 {
		t.Fatalf("epoch global shared: a=%d b=%d", a.Epoch, b.Epoch)
	}
	for _, step := range []struct {
		state *State
		want  string
	}{
		{a, "1/1/1"}, {b, "1/1/1"},
		{a, "2/2/2"}, {b, "2/2/2"},
	} {
		if got := Run(step.state); got != step.want {
			t.Fatalf("Run=%q, want %q", got, step.want)
		}
	}
}

func TestCompilerRedirectsConcurrentChildren(t *testing.T) {
	a, b := New(), New()
	var wg sync.WaitGroup
	for _, s := range []*State{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			WithBase(s, func() {
				for range 1000 {
					RunCurrent()
				}
			})
		}()
	}
	wg.Wait()
	if got := Run(a); got != "1001/1001/1001" {
		t.Fatalf("a after concurrent execution: %q", got)
	}
	if got := Run(b); got != "1001/1001/1001" {
		t.Fatalf("b after concurrent execution: %q", got)
	}
	WithBase(a, func() {
		result := make(chan string, 1)
		go func() { result <- RunCurrent() }()
		if got := <-result; got != "1002/1002/1002" {
			t.Fatalf("child did not inherit a's base: %q", got)
		}
	})
	if got := Run(b); got != "1002/1002/1002" {
		t.Fatalf("b changed through a's child: %q", got)
	}
}

func TestConcurrentInitialization(t *testing.T) {
	const instances = 64
	states := make(chan *State, instances)
	var wg sync.WaitGroup
	for range instances {
		wg.Add(1)
		go func() {
			defer wg.Done()
			states <- New()
		}()
	}
	wg.Wait()
	close(states)
	seen := make(map[*int]bool)
	for s := range states {
		if s.Count == nil || seen[s.Count] || s.Epoch != 1 || Run(s) != "1/1/1" {
			t.Fatalf("concurrent init shared or lost state: %+v", s)
		}
		seen[s.Count] = true
	}
	if len(seen) != instances {
		t.Fatalf("created %d instances, want %d", len(seen), instances)
	}
}

func TestRegisteredEntryUsesSelectedBase(t *testing.T) {
	for _, s := range []*State{New(), New()} {
		iso, err := isolateproto.New(isolateproto.Config{Entry: Entry})
		if err != nil {
			t.Fatal(err)
		}
		WithBase(s, func() {
			state, _, runErr := iso.Resume(context.Background(), nil)
			if runErr != nil || state != isolateproto.Completed {
				t.Errorf("Resume state=%v err=%v", state, runErr)
			}
		})
		result, err := iso.Result()
		if err != nil || string(result) != "1/1/1" {
			t.Fatalf("isolate result=%q err=%v", result, err)
		}
	}
}

func BenchmarkExplicitState(b *testing.B) {
	s := New()
	b.ResetTimer()
	var result int
	for range b.N {
		result = StepExplicit(s)
	}
	benchmarkSink = result
}

func BenchmarkProcessGlobal(b *testing.B) {
	processGlobal = New().Graph
	b.ResetTimer()
	var result int
	for range b.N {
		result = StepProcessGlobal()
	}
	benchmarkSink = result
}

func BenchmarkCompilerGlobalBase(b *testing.B) {
	s := New()
	var result int
	WithBase(s, func() {
		b.ResetTimer()
		for range b.N {
			result = StepCurrent()
		}
	})
	benchmarkSink = result
}
