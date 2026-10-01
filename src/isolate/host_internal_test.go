// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"internal/isolatebridge"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainExitRevokesUnstartedChildren(t *testing.T) {
	release := make(chan struct{})
	ready := make(chan struct{})
	childExited := make(chan struct{})
	var grandchildRan atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go func() {
				close(ready)
				<-release
				go func() { grandchildRan.Store(true) }()
				close(childExited)
			}()
			<-ready
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-childExited
	deadline := time.Now().Add(time.Second)
	for i.boundary.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := i.boundary.LiveGoroutines(); got != 0 {
		t.Fatalf("after main exit, live goroutines = %d, want 0", got)
	}
	if grandchildRan.Load() {
		t.Fatal("grandchild started after main exit")
	}
}
