// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build race

package runtime

import "unsafe"

// The execution token synchronizes all members of a deterministic group.
// Record its handoff even when a sync primitive (for example WaitGroup.Wait)
// temporarily ignores its internal synchronization. Preserve that scope so
// unrelated primitive bookkeeping remains invisible to the race detector.
//
//go:nosplit
func isolateDispatchRaceSync(gp *g, acquire bool) {
	addr := unsafe.Pointer(&gp.isolateGroup.dispatchRace)
	if !isvalidaddr(addr) {
		return
	}
	if gp.raceignore != 0 {
		racecall(&__tsan_go_ignore_sync_end, gp.racectx, 0, 0, 0)
	}
	if acquire {
		racecall(&__tsan_acquire, gp.racectx, uintptr(addr), 0, 0)
	} else {
		racecall(&__tsan_release, gp.racectx, uintptr(addr), 0, 0)
	}
	if gp.raceignore != 0 {
		racecall(&__tsan_go_ignore_sync_begin, gp.racectx, 0, 0, 0)
	}
}
