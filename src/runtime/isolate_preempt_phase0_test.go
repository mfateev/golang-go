//go:build phase0_e5a && linux && arm64

package runtime_test

import (
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestIsolateSuspendLatencyPhase0 measures the existing runtime's time to
// suspend a busy user goroutine at a safe point. It is one component of the
// E5a hard-kill gate, not a kill implementation or a teardown test.
func TestIsolateSuspendLatencyPhase0(t *testing.T) {
	const trials = 100
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	var stop atomic.Bool
	var progress atomic.Uint64
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.IsolatePhase0RegisterTarget()
		for !stop.Load() {
			progress.Add(1)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for progress.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("user loop did not start")
		}
		runtime.Gosched()
	}
	durations := make([]int64, 0, trials)
	for attempts := 0; len(durations) < trials && attempts < trials*10; attempts++ {
		before := progress.Load()
		for progress.Load() == before {
			runtime.Gosched()
		}
		elapsed, wasRunning, _ := runtime.IsolateSuspendLatencyPhase0()
		if wasRunning {
			durations = append(durations, elapsed)
		}
	}
	stop.Store(true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("user loop did not stop after cooperative request")
	}
	if len(durations) != trials {
		t.Fatal("missing suspension samples")
	}
	slices.Sort(durations)
	t.Logf("safe-point suspension, %d trials: median=%s p99=%s max=%s", trials,
		time.Duration(durations[trials/2]), time.Duration(durations[trials*99/100]), time.Duration(durations[trials-1]))
}

func TestIsolateChannelWaitSuspendLatencyPhase0(t *testing.T) {
	release := make(chan struct{})
	registered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.IsolatePhase0RegisterTarget()
		close(registered)
		<-release
	}()
	<-registered
	var elapsed int64
	var waiting bool
	for attempts := 0; attempts < 100; attempts++ {
		elapsed, _, waiting = runtime.IsolateSuspendLatencyPhase0()
		if waiting {
			break
		}
		runtime.Gosched()
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("channel waiter did not resume")
	}
	if !waiting {
		t.Fatal("did not observe channel waiter in waiting state")
	}
	t.Logf("blocked-channel safe-point suspension: %s (wait record remains linked)", time.Duration(elapsed))
}

func TestIsolateRunningLoopKillPhase0(t *testing.T) {
	testIsolateRunningLoopKillPhase0(t, 2)
}

func TestIsolateRunningLoopKillSinglePPhase0(t *testing.T) {
	testIsolateRunningLoopKillPhase0(t, 1)
}

func testIsolateRunningLoopKillPhase0(t *testing.T, procs int) {
	previous := runtime.GOMAXPROCS(procs)
	defer runtime.GOMAXPROCS(previous)
	var stop atomic.Bool
	var progress atomic.Uint64
	var ranDefer atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ranDefer.Store(true)
		runtime.IsolatePhase0RegisterTarget()
		for !stop.Load() {
			progress.Add(1)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for progress.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("kill target did not start")
		}
		runtime.Gosched()
	}
	var elapsed int64
	var dead bool
	for attempt := 0; attempt < 100 && !dead; attempt++ {
		elapsed, dead = runtime.IsolateKillRegisteredPhase0()
	}
	if !dead {
		stop.Store(true)
		<-done
		t.Fatal("preemption did not terminate loop")
	}
	if ranDefer.Load() {
		t.Fatal("hard kill ran user defer")
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("running-loop kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	t.Logf("experimental running-loop kill: %s (teardown not verified)", time.Duration(elapsed))
}

func TestIsolateChannelWaitKillPhase0(t *testing.T) {
	release := make(chan struct{})
	registered := make(chan struct{})
	var ranDefer atomic.Bool
	go func() {
		defer ranDefer.Store(true)
		runtime.IsolatePhase0RegisterTarget()
		close(registered)
		<-release
	}()
	<-registered
	var waiting bool
	for attempts := 0; attempts < 100; attempts++ {
		_, _, waiting = runtime.IsolateSuspendLatencyPhase0()
		if waiting {
			break
		}
		runtime.Gosched()
	}
	if !waiting {
		close(release)
		t.Fatal("channel receiver did not park")
	}
	elapsed, dead := runtime.IsolateKillChannelWaitPhase0()
	if !dead {
		// The helper leaves the waiter resumable if it cannot find the
		// channel. Do not close release if the helper already closed it.
		t.Fatal("channel waiter was not terminated")
	}
	if ranDefer.Load() {
		t.Fatal("hard kill ran channel waiter's defer")
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("channel-wait kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	t.Logf("experimental channel-wait kill: %s (single receiver only)", time.Duration(elapsed))
}

func TestIsolateLockHolderKillPhase0(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	var held sync.Mutex
	var stop atomic.Bool
	var progress atomic.Uint64
	var ranDefer atomic.Bool
	go func() {
		held.Lock()
		defer func() {
			ranDefer.Store(true)
			held.Unlock()
		}()
		runtime.IsolatePhase0RegisterTarget()
		for !stop.Load() {
			progress.Add(1)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for progress.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("lock holder did not start")
		}
		runtime.Gosched()
	}
	elapsed, dead := runtime.IsolateKillRegisteredPhase0()
	if !dead {
		stop.Store(true)
		t.Fatal("lock holder was not terminated")
	}
	if ranDefer.Load() {
		t.Fatal("hard kill ran lock holder's defer")
	}
	if held.TryLock() {
		held.Unlock()
		t.Fatal("lock was released by hard kill")
	}
	if elapsed > int64(100*time.Millisecond) {
		t.Fatalf("lock-holder kill exceeded 100 ms: %s", time.Duration(elapsed))
	}
	t.Logf("experimental lock-holder kill: %s (no lock waiters)", time.Duration(elapsed))
}

func TestIsolateHeapReclaimPhase0(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	var progress atomic.Uint64
	var stop atomic.Bool
	go func() {
		buf := make([]byte, 8<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = byte(i)
		}
		runtime.IsolatePhase0RegisterTarget()
		for !stop.Load() {
			progress.Add(1)
		}
		runtime.KeepAlive(buf)
	}()
	deadline := time.Now().Add(time.Second)
	for progress.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("heap owner did not start")
		}
		runtime.Gosched()
	}
	runtime.GC()
	var live runtime.MemStats
	runtime.ReadMemStats(&live)
	if live.HeapAlloc < baseline.HeapAlloc+6<<20 {
		t.Fatalf("heap owner did not retain expected bytes: before=%d live=%d", baseline.HeapAlloc, live.HeapAlloc)
	}
	_, dead := runtime.IsolateKillRegisteredPhase0()
	if !dead {
		stop.Store(true)
		t.Fatal("heap owner was not terminated")
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > baseline.HeapAlloc+2<<20 {
		t.Fatalf("killed G retained heap: before=%d after=%d", baseline.HeapAlloc, after.HeapAlloc)
	}
}
