// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

//go:noinline
func stackTraceChild() { _, _ = Call(1, nil) }

//go:noinline
func stackTraceService() { _, _ = ReadOnlyCall(2, nil) }

//go:noinline
func stackTraceInbound() { _, _ = Call(5, nil) }

//go:noinline
func stackTraceHandler() { _, _ = Call(6, nil) }

//go:noinline
func stackTraceSDKService() {
	MarkServiceGoroutine()
	GoWithPriority(PriorityHigh, stackTraceHandler)
	_, _ = Call(7, nil)
}

//go:noinline
func stackTraceOther() { _, _ = Call(3, nil) }

//go:noinline
func stackTraceHost(done <-chan struct{}) { <-done }

func TestStackTraceWorkflowGoroutines(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		t.Run(string(rune('0'+procs)), func(t *testing.T) {
			done := make(chan struct{})
			defer close(done)
			go stackTraceHost(done)
			other := priorityInstance(t, stackTraceOther)
			priorityCommand(t, other)
			if err := other.Suspend(); err != nil {
				t.Fatal(err)
			}
			const children = 256
			instance := priorityInstance(t, func() {
				for range children {
					go stackTraceChild()
				}
				go stackTraceService()
				GoInbound(stackTraceInbound)
				GoWithPriority(PriorityHigh, stackTraceSDKService)
				_, _ = Call(4, nil)
			})
			for range children + 5 {
				priorityCommand(t, instance)
			}
			if err := instance.Suspend(); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				stack := instance.StackTrace()
				if len(stack) <= 64<<10 {
					t.Fatalf("dump did not exercise buffer growth: %d bytes", len(stack))
				}
				if got := strings.Count(stack, "\ngoroutine ") + 1; got != children+2 {
					t.Fatalf("stacks = %d, want %d", got, children+2)
				}
				if got := strings.Count(stack, "isolate.stackTraceChild()"); got != children {
					t.Fatalf("child stacks = %d, want %d", got, children)
				}
				if !strings.Contains(stack, "isolate.stackTraceHandler()") {
					t.Fatal("application handler missing from dump")
				}
				for _, excluded := range []string{"isolate.stackTraceOther", "isolate.stackTraceHost", "isolate.stackTraceService", "isolate.stackTraceInbound", "isolate.stackTraceSDKService", "TestStackTraceWorkflowGoroutines("} {
					if strings.Contains(stack, excluded) {
						t.Fatalf("unrelated stack included: %s", excluded)
					}
				}
				runtime.GC()
			}
			diagnostic := instance.TaskDiagnostics()
			if len(diagnostic) <= 64<<10 {
				t.Fatal("task diagnostic did not preserve the full large dump")
			}
			for _, included := range []string{"isolate.stackTraceService", "isolate.stackTraceInbound", "isolate.stackTraceSDKService", "isolate.stackTraceHandler", "TestStackTraceWorkflowGoroutines", "phase=normal paused=true", "token=0", "normal_queue=[] priority_queue=[]"} {
				if !strings.Contains(diagnostic, included) {
					t.Fatalf("task diagnostic omitted %q: %s", included, diagnostic)
				}
			}
			for _, excluded := range []string{"isolate.stackTraceOther", "isolate.stackTraceHost"} {
				if strings.Contains(diagnostic, excluded) {
					t.Fatalf("task diagnostic included unrelated %q", excluded)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := instance.Kill(ctx); err != nil {
				t.Fatal(err)
			}
			if stack := instance.StackTrace(); stack != "" {
				t.Fatalf("drained isolate retained stacks: %s", stack)
			}
		})
	}
}

//go:noinline
func taskDiagnosticBusy(release *atomic.Bool) {
	for !release.Load() {
	}
}

func TestTaskDiagnosticsActiveDispatcher(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	var release atomic.Bool
	defer release.Store(true)
	instance := priorityInstance(t, func() {
		go stackTraceChild()
		GoWithPriority(PriorityHigh, stackTraceChild)
		_, _ = Call(17, nil)
		taskDiagnosticBusy(&release)
		_, _ = Call(19, nil)
	})
	var root *Command
	var children []*Command
	for range 3 {
		c := priorityCommand(t, instance)
		if c.Op == 17 {
			root = c
		} else {
			children = append(children, c)
		}
	}
	if err := instance.Suspend(); err != nil {
		t.Fatal(err)
	}
	root.Reply(nil, nil)
	if err := instance.Resume(); err != nil {
		t.Fatal(err)
	}
	for _, c := range children {
		c.Reply(nil, nil)
	}
	suspended := make(chan error, 1)
	go func() { suspended <- instance.Suspend() }()
	deadline := time.Now().Add(2 * time.Second)
	var snapshot string
	for {
		snapshot = instance.TaskDiagnostics()
		if !strings.Contains(snapshot, "suspension_waiter=0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("suspension waiter not registered: %s", snapshot)
		}
		runtime.Gosched()
	}
	for _, missing := range []string{"token=0", "normal_queue=[]", "priority_queue=[]"} {
		if strings.Contains(snapshot, missing) {
			t.Fatalf("active dispatcher omitted state %q: %s", missing, snapshot)
		}
	}
	for _, included := range []string{"isolate.taskDiagnosticBusy", "isolate.(*Isolate).Suspend", "TestTaskDiagnosticsActiveDispatcher(", "priority=1 queued=true"} {
		if !strings.Contains(snapshot, included) {
			t.Fatalf("missing %q: %s", included, snapshot)
		}
	}
	release.Store(true)
	select {
	case err := <-suspended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("suspension helper did not finish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := instance.Kill(ctx); err != nil {
		t.Fatal(err)
	}
}
