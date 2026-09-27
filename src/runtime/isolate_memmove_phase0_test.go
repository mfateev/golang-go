//go:build phase0_e5a && phase0_e5a_stress && linux && arm64

package runtime_test

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestIsolateLargeCopyHardKillAcceptancePhase0 checks whether a supported
// built-in copy can delay a kill request past 100 ms. It uses about 8 GiB.
func TestIsolateLargeCopyHardKillAcceptancePhase0(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)
	const size = 4 << 30
	src := make([]byte, size)
	dst := make([]byte, size)
	for i := 0; i < size; i += 4096 {
		src[i] = 1
		dst[i] = 1
	}
	ready := make(chan struct{})
	done := make(chan struct{})
	var stop atomic.Bool
	go func() {
		defer close(done)
		runtime.IsolatePhase0RegisterTarget()
		close(ready)
		for !stop.Load() {
			copy(dst, src)
		}
	}()
	<-ready
	// The target enters a long memmove immediately after waking the host.
	// Waiting briefly makes a request between copies less likely.
	time.Sleep(10 * time.Millisecond)
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	if !dead {
		pcs, threadID := runtime.IsolatePhase0CaptureTargetStack(int64(100 * time.Millisecond))
		var stack strings.Builder
		if len(pcs) == 0 {
			stack.WriteString("<unavailable>")
		} else {
			frames := runtime.CallersFrames(pcs)
			for n := 0; n < 8; n++ {
				frame, more := frames.Next()
				fmt.Fprintf(&stack, "\n%s:%d %s", frame.File, frame.Line, frame.Function)
				if !more {
					break
				}
			}
		}
		stop.Store(true)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("large-copy goroutine did not exit after the failed kill")
		}
		t.Fatalf("large-copy goroutine survived kill request after %s (tid=%d stack=%s)", time.Duration(elapsed), threadID, stack.String())
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("large copy delayed hard kill beyond 100 ms: %s", time.Duration(elapsed))
	}
	select {
	case <-done:
		t.Fatal("hard kill ran the target's defer")
	default:
	}
}
