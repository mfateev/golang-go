//go:build phase0_e5a && phase0_e5a_diag && linux && arm64

package runtime_test

import (
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var isolatePhase0StackProgress atomic.Uint64

//go:noinline
func isolatePhase0BusyStackTarget(stop *atomic.Bool, ready chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	runtime.IsolatePhase0RegisterTarget()
	close(ready)
	for !stop.Load() {
		isolatePhase0StackProgress.Add(1)
	}
}

func TestIsolateRunningStackSamplePhase0(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)
	var stop atomic.Bool
	ready, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		isolatePhase0BusyStackTarget(&stop, ready)
	}()
	<-ready
	for isolatePhase0StackProgress.Load() < 1000 {
		runtime.Gosched()
	}
	start := time.Now()
	pcs, threadID := runtime.IsolatePhase0CaptureTargetStack(int64(100 * time.Millisecond))
	elapsed := time.Since(start)
	stop.Store(true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stack-sample target did not stop")
	}
	if len(pcs) == 0 || threadID <= 0 {
		t.Fatalf("no targeted stack sample after %s: pcs=%d tid=%d", elapsed, len(pcs), threadID)
	}
	frames := runtime.CallersFrames(pcs)
	first, _ := frames.Next()
	if first.Function == "" {
		t.Fatalf("sample PC did not symbolize: %#v", pcs[0])
	}
	frames = runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "isolatePhase0BusyStackTarget") {
			return
		}
		if !more {
			break
		}
	}
	if runtime.IsolatePhase0RaceEnabled() && len(pcs) == 1 {
		// The race detector sometimes interrupts inside its atomic assembly,
		// where unwinding stops. The top PC is still a useful diagnostic.
		return
	}
	t.Fatalf("sample did not contain target function: pcs=%#v tid=%d", pcs, threadID)
}
