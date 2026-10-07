// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"iter"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "unsafe"
)

//go:linkname resourceAccountCount runtime.isolateResourceAccountCount
func resourceAccountCount() int64

func resourceCommand(t *testing.T, i *Isolate) *Command {
	t.Helper()
	select {
	case command := <-i.Commands():
		return command
	case <-i.Done():
		t.Fatalf("unexpected resource completion: %v", i.Wait())
	case <-time.After(5 * time.Second):
		t.Fatal("resource handshake timed out")
	}
	return nil
}

func TestResourcesAccountingAndGC(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		i, err := New(Config{Deterministic: deterministic, Program: lifecycleProgram(func() {
			data := make([]byte, 1<<20)
			_, _ = Call(1, data[:1])
			runtime.KeepAlive(data)
			data = nil
			_, _ = Call(2, nil)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		first := resourceCommand(t, i)
		before := i.Resources()
		if before.LiveGoroutines != 1 || before.PeakGoroutines == 0 || before.HeapBytes < 1<<20 || before.ReservedHeapBytes < before.HeapBytes || before.StackBytes == 0 || before.RuntimeBytes == 0 || before.MemoryBytes != before.HeapBytes+before.StackBytes+before.RuntimeBytes {
			t.Fatalf("incorrect attached accounting: %+v", before)
		}
		runtime.GC()
		if i.Resources().HeapBytes < 1<<20 {
			t.Fatalf("GC refunded a live private object: before=%+v after=%+v", before, i.Resources())
		}
		first.Reply(nil, nil)
		second := resourceCommand(t, i)
		for range 5 {
			runtime.GC()
		}
		after := i.Resources()
		if before.HeapBytes < after.HeapBytes+(1<<20) || after.TotalAllocatedBytes < before.TotalAllocatedBytes || after.RunningNanoseconds == 0 || after.Progress == 0 {
			t.Fatalf("GC/progress accounting: before=%+v after=%+v", before, after)
		}
		second.Reply(nil, nil)
		if err := lifecycleWait(t, i); err != nil {
			t.Fatal(err)
		}
		for range 5 {
			runtime.GC()
		}
		closed := i.Resources()
		if closed.LiveGoroutines != 0 || closed.StackBytes != 0 || closed.HeapBytes != 0 || closed.ReservedHeapBytes != 0 || closed.MemoryBytes != closed.RuntimeBytes {
			t.Fatalf("closed handle retained private resources: %+v", closed)
		}
	}
}

func TestResourcesGoroutineLimit(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var continued atomic.Bool // Privileged probe; no application pointer crosses Call.
		i, err := New(Config{Deterministic: deterministic, ResourceLimits: ResourceLimits{MaxGoroutines: 2}, Program: lifecycleProgram(func() {
			defer func() { _ = recover(); continued.Store(true) }()
			go func() { select {} }()
			go func() { continued.Store(true) }()
			continued.Store(true)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var failure *ResourceLimitError
		if err := lifecycleWait(t, i); !errors.As(err, &failure) || failure.Resource != "goroutines" || failure.Limit != 2 || failure.Usage != 3 || failure.Stats.PeakGoroutines > 2 || failure.Stack == "" {
			t.Fatalf("goroutine failure: %v", err)
		}
		if continued.Load() || i.Resources().LiveGoroutines != 0 {
			t.Fatal("goroutine limit was recovered or leaked a member")
		}
	}
}

func TestResourcesMemoryLimit(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		var continued atomic.Bool
		i, err := New(Config{Deterministic: deterministic, ResourceLimits: ResourceLimits{MaxMemoryBytes: 128 << 10}, Program: lifecycleProgram(func() {
			defer func() { _ = recover(); continued.Store(true) }()
			data := make([]byte, 128<<20)
			runtime.KeepAlive(data)
			continued.Store(true)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var failure *ResourceLimitError
		if err := lifecycleWait(t, i); !errors.As(err, &failure) || failure.Resource != "memory" || failure.Limit != 128<<10 || failure.Usage < 128<<20 || failure.Stats.HeapBytes >= 128<<20 || failure.Stack == "" {
			t.Fatalf("memory failure: %v", err)
		}
		if continued.Load() {
			t.Fatal("allocation limit was recovered")
		}
	}
}

//go:noinline
func resourceRecurse(depth int) {
	var data [1024]byte
	data[depth%len(data)] = byte(depth)
	if depth != 0 {
		resourceRecurse(depth - 1)
	}
	runtime.KeepAlive(data)
}

func TestResourcesStackLimit(t *testing.T) {
	i, err := New(Config{ResourceLimits: ResourceLimits{MaxMemoryBytes: 64 << 10}, Program: lifecycleProgram(func() { resourceRecurse(10000) })})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	var failure *ResourceLimitError
	if err := lifecycleWait(t, i); !errors.As(err, &failure) || failure.Resource != "memory" {
		t.Fatalf("stack limit: %v", err)
	}
	if i.Resources().StackBytes != 0 {
		t.Fatal("stack charge survived the cleanup fence")
	}
}

func TestResourcesInitializationLimit(t *testing.T) {
	program := lifecycleProgram(func() {})
	program.entry.NewState = func() (func(func()), error) {
		runtime.KeepAlive(make([]byte, 64<<20))
		return func(fn func()) { fn() }, nil
	}
	i, err := New(Config{Program: program, ResourceLimits: ResourceLimits{MaxMemoryBytes: 128 << 10}})
	var failure *ResourceLimitError
	if i != nil || !errors.As(err, &failure) || failure.Resource != "memory" {
		t.Fatalf("initializer limit: instance=%v error=%v", i, err)
	}
}

func TestResourcesRetainedHandlesReclaimAccounts(t *testing.T) {
	for range 5 {
		runtime.GC()
	}
	before := resourceAccountCount()
	handles := make([]*Isolate, 32)
	for index := range handles {
		i, err := New(Config{Program: lifecycleProgram(func() { _, _ = Call(1, make([]byte, 8<<10)) })})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		resourceCommand(t, i)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := i.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		handles[index] = i
	}
	for range 5 {
		runtime.GC()
	}
	for _, i := range handles {
		stats := i.Resources()
		if stats.HeapBytes != 0 || stats.StackBytes != 0 || stats.ReservedHeapBytes != 0 {
			t.Fatalf("retained handle resource leak: %+v", stats)
		}
	}
	runtime.KeepAlive(handles)
	handles = nil
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if resourceAccountCount() <= before {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("resource records survived their last handle/cache/span: before=%d after=%d", before, resourceAccountCount())
}

func TestResourcesIteratorAdmission(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		i, err := New(Config{Deterministic: deterministic, ResourceLimits: ResourceLimits{MaxGoroutines: 1}, Program: lifecycleProgram(func() {
			next, stop := iter.Pull(func(yield func(int) bool) { yield(1) })
			defer stop()
			next()
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var limit *ResourceLimitError
		if err := lifecycleWait(t, i); !errors.As(err, &limit) || limit.Resource != "goroutines" || limit.Usage != 2 {
			t.Fatalf("iterator admission: %v", err)
		}
	}
}

func TestResourcesPinnedSelectRecords(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		i, err := New(Config{Deterministic: deterministic, ResourceLimits: ResourceLimits{MaxMemoryBytes: 1536 << 10}, Program: lifecycleProgram(func() {
			channel := make(chan int)
			cases := make([]reflect.SelectCase, 10000)
			for index := range cases {
				cases[index] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(channel)}
			}
			reflect.Select(cases)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var limit *ResourceLimitError
		if err := lifecycleWait(t, i); !errors.As(err, &limit) || limit.Resource != "memory" {
			t.Fatalf("pinned select limit: %v", err)
		}
		stats := i.Resources()
		if stats.LiveGoroutines != 0 || stats.StackBytes != 0 {
			t.Fatalf("pinned wait failed to drain: %+v", stats)
		}
	}
}

func TestResourcesCachedDensity(t *testing.T) {
	count := 2048
	if testing.Short() {
		count = 256
	} else if configured := os.Getenv("GO_ISOLATE_RESOURCE_DENSITY"); configured != "" {
		var err error
		count, err = strconv.Atoi(configured)
		if err != nil || count < 1 {
			t.Fatal("invalid GO_ISOLATE_RESOURCE_DENSITY")
		}
	}
	for range 5 {
		runtime.GC()
	}
	baselineAccounts := resourceAccountCount()
	baselineCaches := isolateAllocCacheCount()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	baselineRSS := resourceProcessRSS()
	handles := make([]*Isolate, count)
	t.Cleanup(func() {
		for _, i := range handles {
			if i != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = i.Kill(ctx)
				cancel()
			}
		}
	})
	for index := range handles {
		i, err := New(Config{Deterministic: true, ResourceLimits: ResourceLimits{MaxMemoryBytes: 128 << 10, MaxGoroutines: 2}, Program: lifecycleProgram(func() {
			data := isolateCachedHeapState(4096)
			for {
				_, _ = Call(1, data[:1])
				runtime.KeepAlive(data)
			}
		})})
		if err != nil {
			t.Fatal(err)
		}
		handles[index] = i
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		resourceCommand(t, i)
		if err := i.Suspend(); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		runtime.GC()
	}
	var memory, heap, stacks, metadata, reserved uint64
	for _, i := range handles {
		s := i.Resources()
		if s.LiveGoroutines != 1 || s.HeapBytes < 4096 || s.MemoryBytes != s.HeapBytes+s.StackBytes+s.RuntimeBytes {
			t.Fatalf("cached accounting: %+v", s)
		}
		memory += s.MemoryBytes
		heap += s.HeapBytes
		stacks += s.StackBytes
		metadata += s.RuntimeBytes
		reserved += s.ReservedHeapBytes
	}
	var cached runtime.MemStats
	runtime.ReadMemStats(&cached)
	t.Logf("cached=%d charged=%d heap=%d stacks=%d metadata=%d reserved-spans=%d process-HeapAlloc-delta=%d process-StackInuse-delta=%d RSS-baseline=%d RSS-cached=%d", count, memory, heap, stacks, metadata, reserved, int64(cached.HeapAlloc)-int64(baseline.HeapAlloc), int64(cached.StackInuse)-int64(baseline.StackInuse), baselineRSS, resourceProcessRSS())
	for _, i := range handles {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := i.Kill(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		remaining := false
		for _, i := range handles {
			s := i.Resources()
			if s.HeapBytes != 0 || s.ReservedHeapBytes != 0 || s.StackBytes != 0 || s.LiveGoroutines != 0 {
				remaining = true
				break
			}
		}
		if !remaining {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retained completed handles kept private resources charged")
		}
	}
	handles = nil
	for {
		runtime.GC()
		if resourceAccountCount() <= baselineAccounts && isolateAllocCacheCount() <= baselineCaches {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cached accounting leak: accounts=%d baseline=%d caches=%d baseline=%d", resourceAccountCount(), baselineAccounts, isolateAllocCacheCount(), baselineCaches)
		}
	}
}

func TestResourcesHostWatchdogFirstFault(t *testing.T) {
	i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() { _, _ = Call(1, nil) })})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	resourceCommand(t, i)
	if err := i.Suspend(); err != nil {
		t.Fatal(err)
	}
	first := i.FailProgress(10, 20)
	second := i.FailTaskDuration(30, 40)
	for _, err := range []error{first, second, lifecycleWait(t, i)} {
		var failure *ResourceLimitError
		if !errors.As(err, &failure) || failure.Resource != "no progress" || failure.Limit != 10 || failure.Usage != 20 {
			t.Fatalf("watchdog replaced immutable first fault: %v", err)
		}
	}
}

func TestResourcesProcessServicesExcluded(t *testing.T) {
	i, err := New(Config{Program: lifecycleProgram(func() {
		process := isolatebridge.EnterProcess()
		data := make([]byte, 64<<20)
		runtime.KeepAlive(data)
		isolatebridge.LeaveProcess(process)
		_, _ = Call(1, nil)
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	command := resourceCommand(t, i)
	if stats := i.Resources(); stats.TotalAllocatedBytes > 1<<20 || stats.HeapBytes > 1<<20 {
		t.Fatalf("process service charged to private heap: %+v", stats)
	}
	command.Reply(nil, nil)
	if err := lifecycleWait(t, i); err != nil {
		t.Fatal(err)
	}
}

// Exercise allocations and stack growth near the boundary, including GC assists
// and G reuse. Accepted reservations must either materialize or be refunded.
func TestResourcesAllocationStackBoundary(t *testing.T) {
	for index := range 128 {
		i, err := New(Config{ResourceLimits: ResourceLimits{MaxMemoryBytes: 32 << 10}, Program: lifecycleProgram(func() {
			resourceAllocationDepth(8 + index%24)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		var failure *ResourceLimitError
		if err := lifecycleWait(t, i); err != nil && !errors.As(err, &failure) {
			t.Fatal(err)
		}
		for range 3 {
			runtime.GC()
		}
		if stats := i.Resources(); stats.HeapBytes != 0 || stats.StackBytes != 0 || stats.ReservedHeapBytes != 0 {
			t.Fatalf("interrupted reservation survived cleanup: %+v", stats)
		}
	}
}

//go:noinline
func resourceAllocationDepth(depth int) {
	var frame [1024]byte
	if depth != 0 {
		resourceAllocationDepth(depth - 1)
	} else {
		for range 16 {
			runtime.KeepAlive(isolateCachedHeapState(1024))
		}
	}
	runtime.KeepAlive(frame)
}

// Linux density runs record RSS separately; other platforms still verify the
// portable accounting/reclamation contract without assuming procfs exists.
func resourceProcessRSS() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}
