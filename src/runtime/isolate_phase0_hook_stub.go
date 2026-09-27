// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !phase0_e5a || !linux || !arm64

package runtime

func isolatePhase0Kill(*g) bool { return false }

func isolatePhase0Terminate(*g) { throw("unreachable isolate Phase 0 kill hook") }

func isolatePhase0CleanupDead(*g) {}

func isolatePhase0CaptureSignalStack(*g, uintptr, uintptr, uintptr) {}
