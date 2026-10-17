// errorcheck -0 -m

//go:build !nacl && !386 && !wasm && !arm && !gcflags_noopt && goexperiment.isolates

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Check sync inlining with the isolate experiment. RWMutex read operations
// include mandatory read-only gates and exceed the inline budget; Mutex and
// Once retain their ordinary fast-path inlining.
// Compiles but does not run.

// FIXME: This test is disabled on architectures where atomic operations
// are function calls rather than intrinsics, since this prevents inlining
// of the sync fast paths. This test should be re-enabled once the problem
// is solved.

package foo

import (
	"sync"
)

var mutex *sync.Mutex

func small5() { // ERROR "can inline small5"
	// the Unlock fast path should be inlined
	mutex.Unlock() // ERROR "inlining call to sync\.\(\*Mutex\)\.Unlock"
}

func small6() { // ERROR "can inline small6"
	// the Lock fast path should be inlined
	mutex.Lock() // ERROR "inlining call to sync\.\(\*Mutex\)\.Lock"
}

var once *sync.Once

func small7() { // ERROR "can inline small7"
	// the Do fast path should be inlined
	once.Do(small5) // ERROR "inlining call to sync\.\(\*Once\)\.Do" "inlining call to atomic\.\(\*Bool\)\.Load"
}

var rwmutex *sync.RWMutex

func small8() { // ERROR "can inline small8"
	// The read-only gate keeps this method out of the inline fast path.
	rwmutex.RUnlock()
}

func small9() { // ERROR "can inline small9"
	// The read-only gate keeps this method out of the inline fast path.
	rwmutex.RLock()
}
