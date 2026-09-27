//go:build !phase0_e5a || !linux || !arm64

package runtime

func isolatePhase0Kill(*g) bool { return false }

func isolatePhase0Terminate(*g) { throw("unreachable isolate Phase 0 kill hook") }

func isolatePhase0CleanupDead(*g) {}

func isolatePhase0CaptureSignalStack(*g, uintptr, uintptr, uintptr) {}
