// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"errors"
	"internal/isolatebridge"
	"isolate"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unique"
	"unsafe"
)

//go:linkname runtimeOwner runtime.isolateGetOwner
func runtimeOwner() unsafe.Pointer

var ownerTestSequence atomic.Uint64

func TestHostCallsFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func()
	}{
		{"Call", func() { _, _ = isolate.Call(1, []byte("input")) }},
		{"Inbox", func() { _ = isolate.Inbox() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(got.(string), "outside an active isolate") {
					t.Errorf("panic = %v, want outside-an-isolate failure", got)
				}
			}()
			tt.call()
		})
	}
}

func TestCallAndInbox(t *testing.T) {
	initial := []byte("initial")
	b := isolatebridge.New(initial)
	initial[0] = 'X'

	type result struct {
		initial string
		second  string
		reply   string
		err     error
	}
	done := make(chan result, 1)
	go b.Run(func() {
		first := <-isolate.Inbox()
		ready := make(chan string, 1)
		go func() {
			// This native child must inherit the same boundary.
			ready <- string(<-isolate.Inbox())
		}()
		second := <-ready
		reply, err := isolate.Call(7, []byte("request"))
		done <- result{string(first), second, string(reply), err}
	})

	second := []byte("second")
	b.Send(second)
	second[0] = 'X'
	cmd := <-b.Commands()
	if cmd.ID != 1 || cmd.Op != 7 || string(cmd.Payload) != "request" {
		t.Fatalf("command = %+v", cmd)
	}
	reply := []byte("reply")
	cmd.Reply(reply, nil)
	reply[0] = 'X'
	got := <-done
	if got != (result{initial: "initial", second: "second", reply: "reply"}) {
		t.Fatalf("result = %+v", got)
	}
}

func TestConcurrentCallsKeepTheirReplies(t *testing.T) {
	b := isolatebridge.New(nil)
	done := make(chan string, 2)
	b.Run(func() {
		for i := range 2 {
			go func() {
				response, err := isolate.Call(uint32(i+1), nil)
				if err != nil {
					done <- err.Error()
					return
				}
				done <- string(response)
			}()
		}
	})
	first := <-b.Commands()
	second := <-b.Commands()
	if first.ID == second.ID || first.Op == second.Op {
		t.Fatalf("commands not distinct: %+v, %+v", first, second)
	}
	second.Reply([]byte("second"), nil)
	first.Reply([]byte("first"), errors.New("host error"))
	a, z := <-done, <-done
	if !(a == "second" && z == "host error" || a == "host error" && z == "second") {
		t.Fatalf("replies = %q, %q", a, z)
	}
}

func TestPoolValuesDoNotCrossBoundary(t *testing.T) {
	var allocations atomic.Int32
	pool := sync.Pool{New: func() any {
		allocations.Add(1)
		return new(int)
	}}
	hostValue := new(int)
	pool.Put(hostValue)

	b := isolatebridge.New(nil)
	var first, second, childValue any
	b.Run(func() {
		first = pool.Get()
		pool.Put(first)
		second = pool.Get()
		pool.Put(second)
		child := make(chan any, 1)
		go func() {
			value := pool.Get()
			pool.Put(value)
			child <- value
		}()
		childValue = <-child
	})
	if first == hostValue || second == hostValue || childValue == hostValue || first == second || first == childValue || second == childValue {
		t.Fatalf("pool reused a host or isolate value: host=%p first=%p second=%p child=%p", hostValue, first, second, childValue)
	}
	if got := allocations.Load(); got != 3 {
		t.Fatalf("pool allocated %d values inside isolate, want 3", got)
	}
}

func TestProcessCleanupPathsRejectIsolate(t *testing.T) {
	if got := unique.Make("host").Value(); got != "host" {
		t.Fatalf("process unique.Make = %q", got)
	}
	checkPanic := func(name, want string, fn func()) {
		t.Helper()
		defer func() {
			got, ok := recover().(string)
			if !ok || !strings.Contains(got, want) {
				t.Errorf("%s panic = %q, want %q", name, got, want)
			}
		}()
		fn()
	}
	b := isolatebridge.New(nil)
	b.Run(func() {
		checkPanic("AddCleanup", "runtime.AddCleanup is unavailable", func() {
			runtime.AddCleanup(new(int), func(int) {}, 0)
		})
		checkPanic("SetFinalizer", "runtime.SetFinalizer is unavailable", func() {
			runtime.SetFinalizer(new(int), func(*int) {})
		})
		checkPanic("unique.Make", "unique.Make is unavailable", func() {
			unique.Make("isolate")
		})
		checkPanic("netip.WithZone", "unique.Make is unavailable", func() {
			netip.MustParseAddr("fe80::1").WithZone("zone")
		})
		child := make(chan bool, 1)
		go func() {
			defer func() { child <- recover() != nil }()
			unique.Make("child")
		}()
		if !<-child {
			t.Error("child goroutine accepted unique.Make")
		}
	})
}

func TestInstanceOwnerSpansInitializationAndChildren(t *testing.T) {
	name := "test-instance-owner-context-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	var initOwner unsafe.Pointer
	type observedOwners struct{ main, child unsafe.Pointer }
	observed := make(chan observedOwners, 1)
	isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			initOwner = runtimeOwner()
			func() {
				defer func() {
					if recover() == nil {
						t.Error("Inbox was available during package initialization")
					}
				}()
				isolate.Inbox()
			}()
			func() {
				defer func() {
					if recover() == nil {
						t.Error("unique.Make was available during package initialization")
					}
				}()
				unique.Make("during-initialization")
			}()
			return func(fn func()) {
				if got := runtimeOwner(); got != initOwner {
					t.Errorf("state runner owner = %p, want %p", got, initOwner)
				}
				fn()
			}, nil
		},
		Main: func() {
			child := make(chan unsafe.Pointer, 1)
			go func() { child <- runtimeOwner() }()
			observed <- observedOwners{runtimeOwner(), <-child}
		},
	})
	program, ok := isolate.LookupProgram(name)
	if !ok {
		t.Fatal("missing test program")
	}
	instance, err := isolate.New(isolate.Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if initOwner == nil || runtimeOwner() != nil {
		t.Fatalf("owner after New: initializer=%p host=%p", initOwner, runtimeOwner())
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	got := <-observed
	<-instance.Done()
	if got.main != initOwner || got.child != initOwner || runtimeOwner() != nil {
		t.Fatalf("owners: initializer=%p main=%p child=%p host=%p", initOwner, got.main, got.child, runtimeOwner())
	}
}
