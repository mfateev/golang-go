// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"context"
	"runtime"
	"strings"
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
