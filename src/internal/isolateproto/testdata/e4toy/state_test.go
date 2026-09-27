// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4

package e4toy

import (
	"sync"
	"testing"
)

func TestRerunInitWithDifferentGlobalBase(t *testing.T) {
	a, b := New(), New()
	if a.Count == b.Count || a.Values == nil || b.Values == nil {
		t.Fatal("initialized object graphs overlap or are incomplete")
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

func TestConcurrentBasesAndGoInheritance(t *testing.T) {
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

	result := make(chan bool, 1)
	go func() { result <- getBase() != nil }()
	if <-result {
		t.Fatal("unassociated goroutine inherited a stale base")
	}
}
