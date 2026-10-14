// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

func TestIsolateInitializationNestedScopes(t *testing.T) {
	runtime.IsolateInitializationScopeForTest(func() {
		outer := runtime.IsolateMetadataBytesForTest(257)
		p := unsafe.Pointer(&outer[0])
		runtime.IsolateInitializationScopeForTest(func() {
			if runtime.IsolateInitializationRecordedForTest(p) {
				t.Error("nested scope inherited outer allocations")
			}
			inner := runtime.IsolateMetadataBytesForTest(257)
			if !runtime.IsolateInitializationRecordedForTest(unsafe.Pointer(&inner[0])) {
				t.Error("nested allocation was not recorded")
			}
		})
		// Publication pauses recording but keeps the entry. A nested initializer
		// during that pause must not remove the outer entry when it returns.
		runtime.IsolateInitializationPauseForTest(func() {
			runtime.IsolateInitializationScopeForTest(func() {})
			if !runtime.IsolateInitializationActiveForTest() {
				t.Error("nested scope removed the paused outer entry")
			}
		})
		if !runtime.IsolateInitializationRecordedForTest(p) {
			t.Error("outer recorder was not restored")
		}
	})
	if runtime.IsolateInitializationActiveForTest() {
		t.Error("completed initializer retained its entry")
	}
}

func TestIsolateInitializationConcurrentScopes(t *testing.T) {
	const workers = 32
	ready := make(chan []byte, workers)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range 20 {
				runtime.IsolateInitializationScopeForTest(func() {
					bytes := runtime.IsolateMetadataBytesForTest(257)
					ready <- bytes
					<-release
					if !runtime.IsolateInitializationRecordedForTest(unsafe.Pointer(&bytes[0])) {
						t.Error("own allocation missing after concurrent initialization/GC")
					}
				})
				if runtime.IsolateInitializationActiveForTest() {
					t.Error("completed scope retained its entry")
				}
			}
		})
	}
	for range workers {
		bytes := <-ready
		if runtime.IsolateInitializationRecordedForTest(unsafe.Pointer(&bytes[0])) {
			t.Error("ordinary host goroutine used another goroutine's recorder")
		}
	}
	if n := runtime.IsolateInitializationEntriesForTest(); n != workers {
		t.Errorf("active entries = %d, want %d", n, workers)
	}
	runtime.GC()
	close(release)
	// Drain subsequent rounds so the workers can finish while insertion and
	// removal race with allocator lookups in other goroutines.
	for range workers * 19 {
		<-ready
	}
	wg.Wait()
	if n := runtime.IsolateInitializationEntriesForTest(); n != 0 {
		t.Errorf("entries after initialization = %d, want 0", n)
	}
}

func TestIsolateInitializationUnwind(t *testing.T) {
	func() {
		defer func() {
			if got := recover(); got != "initializer panic" {
				t.Errorf("panic = %v", got)
			}
		}()
		runtime.IsolateInitializationScopeForTest(func() {
			panic("initializer panic")
		})
	}()
	if runtime.IsolateInitializationActiveForTest() {
		t.Error("panicking initializer retained its entry")
	}
	done := make(chan struct{})
	runtime.IsolateInitializationScopeForTest(func() {
		go func() {
			defer close(done)
			if runtime.IsolateInitializationActiveForTest() {
				t.Error("child inherited initialization scope")
			}
			defer func() {
				if runtime.IsolateInitializationActiveForTest() {
					t.Error("Goexit retained initialization scope")
				}
			}()
			runtime.IsolateInitializationScopeForTest(runtime.Goexit)
		}()
		<-done
	})
	if n := runtime.IsolateInitializationEntriesForTest(); n != 0 {
		t.Errorf("entries after unwind = %d, want 0", n)
	}
}
