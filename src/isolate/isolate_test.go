// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"bytes"
	"context"
	"crypto/mlkem"
	crand "crypto/rand"
	"errors"
	"expvar"
	"flag"
	"fmt"
	"internal/isolatebridge"
	"io"
	"isolate"
	"iter"
	"log"
	"log/slog"
	"maps"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	rdebug "runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	rtrace "runtime/trace"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode"
	"unique"
	"unsafe"
)

//go:linkname runtimeOwner runtime.isolateGetOwner
func runtimeOwner() uintptr

//go:linkname largeAllocOrigin runtime.isolateLargeAllocOrigin
func largeAllocOrigin(unsafe.Pointer) (uintptr, bool)

func byteSliceOrigin(b []byte) (uintptr, bool) {
	if len(b) == 0 {
		return 0, false
	}
	return largeAllocOrigin(unsafe.Pointer(&b[0]))
}

var ownerTestSequence atomic.Uint64

type customAfterFuncParent struct {
	context.Context
	done chan struct{}
}

func (p *customAfterFuncParent) Done() <-chan struct{} { return p.done }
func (p *customAfterFuncParent) AfterFunc(func()) func() bool {
	return func() bool { return true }
}

func TestHostCallsFailClosed(t *testing.T) {
	defer func() {
		got := recover()
		if got == nil || !strings.Contains(got.(string), "outside an active isolate") {
			t.Errorf("panic = %v, want outside-an-isolate failure", got)
		}
	}()
	_, _ = isolate.Call(1, []byte("input"))
}

func TestFmtStandardStreamsRejectIsolate(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		if got := fmt.Sprintf("value=%d", 7); got != "value=7" {
			t.Errorf("Sprintf = %q", got)
		}
		var output bytes.Buffer
		if _, err := fmt.Fprintln(&output, "value", 7); err != nil || output.String() != "value 7\n" {
			t.Errorf("Fprintln = %q, %v", output.String(), err)
		}
		var scanned int
		if _, err := fmt.Sscan("7", &scanned); err != nil || scanned != 7 {
			t.Errorf("Sscan = %d, %v", scanned, err)
		}
		for _, call := range []func(){
			func() { _, _ = fmt.Print("value") },
			func() { _, _ = fmt.Printf("%d", 7) },
			func() { _, _ = fmt.Println("value") },
			func() { _, _ = fmt.Scan(&scanned) },
			func() { _, _ = fmt.Scanf("%d", &scanned) },
			func() { _, _ = fmt.Scanln(&scanned) },
		} {
			func() {
				defer func() {
					if got := recover(); got != "fmt: standard input and output are unavailable inside an isolate" {
						t.Errorf("standard stream call panic = %v", got)
					}
				}()
				call()
			}()
		}
	})
}

func TestMapWritesStayWithOwner(t *testing.T) {
	processString := map[string]int{"x": 1}
	process32 := map[uint32]int{1: 2}
	process64 := map[uint64]int{1: 3}
	ptr := new(int)
	processPointer := map[*int]int{ptr: 5}
	processStruct := map[struct{ A, B string }]int{{"a", "b"}: 4}
	unicodeAlias := unicode.Categories
	defer delete(unicodeAlias, "IsolateProbe")
	if unicodeAlias["L"] == nil {
		t.Fatal("missing process Unicode category")
	}
	wantReject := func(name string, call func()) {
		defer func() {
			if got := recover(); got != "isolate: map write crosses owner boundary" {
				t.Errorf("%s panic = %v", name, got)
			}
		}()
		call()
	}
	b := isolatebridge.New()
	var owned map[string]int
	b.Run(func() {
		if unicodeAlias["L"] == nil || processString["x"] != 1 {
			t.Error("process map read failed")
		}
		owned = map[string]int{"x": 1}
		owned["x"] = 2
		clone := maps.Clone(processString)
		clone["x"] = 9
		if processString["x"] != 1 || clone["x"] != 9 {
			t.Error("cloning a process map did not create isolate-owned state")
		}
		wantReject("string", func() { processString["x"] = 8 })
		wantReject("uint32", func() { process32[1] = 8 })
		wantReject("uint64", func() { process64[1] = 8 })
		wantReject("pointer", func() { processPointer[ptr] = 8 })
		wantReject("struct", func() { processStruct[struct{ A, B string }{"a", "b"}] = 8 })
		wantReject("delete", func() { delete(processString, "x") })
		wantReject("clear", func() { clear(processString) })
		wantReject("reflect", func() { reflect.ValueOf(processString).SetMapIndex(reflect.ValueOf("x"), reflect.ValueOf(8)) })
		wantReject("Unicode alias", func() { unicodeAlias["IsolateProbe"] = unicodeAlias["L"] })
	})
	if len(processString) != 1 || processString["x"] != 1 || process32[1] != 2 || process64[1] != 3 || processPointer[ptr] != 5 || processStruct[struct{ A, B string }{"a", "b"}] != 4 || unicodeAlias["IsolateProbe"] != nil {
		t.Fatal("isolate changed process-owned map")
	}
	second := isolatebridge.New()
	second.Run(func() { wantReject("other isolate", func() { owned["x"] = 3 }) })
	wantReject("host", func() { owned["x"] = 3 })
	wantCloneReject := func(name string, call func()) {
		defer func() {
			if got := recover(); got != "isolate: map clone crosses owner boundary" {
				t.Errorf("%s clone panic = %v", name, got)
			}
		}()
		call()
	}
	second.Run(func() { wantCloneReject("other isolate", func() { _ = maps.Clone(owned) }) })
	wantCloneReject("host", func() { _ = maps.Clone(owned) })
	b.Run(func() {
		if owned["x"] != 2 {
			t.Error("cross-owner write changed isolate-owned map")
		}
	})
}

func TestMapReadsStayWithOwner(t *testing.T) {
	process := map[string]int{"x": 1}
	b := isolatebridge.New()
	var ownedString map[string]int
	var owned32 map[uint32]int
	var owned64 map[uint64]int
	var ownedStruct map[struct{ A, B string }]int
	var iter *reflect.MapIter
	b.Run(func() {
		ownedString = map[string]int{"x": 2}
		owned32 = map[uint32]int{1: 2}
		owned64 = map[uint64]int{1: 2}
		ownedStruct = map[struct{ A, B string }]int{{"a", "b"}: 2}
		iter = reflect.ValueOf(ownedString).MapRange()
		if !iter.Next() {
			t.Error("owner iterator was empty")
		}
		if process["x"] != 1 {
			t.Error("process map read failed")
		}
	})
	wantReject := func(name string, call func()) {
		defer func() {
			if got := recover(); got != "isolate: map read crosses owner boundary" {
				t.Errorf("%s panic = %v", name, got)
			}
		}()
		call()
	}
	check := func() {
		wantReject("string", func() { _ = ownedString["x"] })
		wantReject("uint32", func() { _ = owned32[1] })
		wantReject("uint64", func() { _ = owned64[1] })
		wantReject("struct", func() { _ = ownedStruct[struct{ A, B string }{"a", "b"}] })
		wantReject("range", func() {
			for range ownedString {
			}
		})
		wantReject("reflect lookup", func() { _ = reflect.ValueOf(ownedString).MapIndex(reflect.ValueOf("x")) })
		wantReject("reflect range", func() { reflect.ValueOf(ownedString).MapRange().Next() })
		wantReject("reflect length", func() { _ = reflect.ValueOf(ownedString).Len() })
		wantReject("iterator key", func() { _ = iter.Key() })
		wantReject("iterator value", func() { _ = iter.Value() })
		wantReject("iterator next", func() { _ = iter.Next() })
	}
	check()
	other := isolatebridge.New()
	other.Run(check)
	b.Run(func() {
		if ownedString["x"] != 2 || owned32[1] != 2 || owned64[1] != 2 || ownedStruct[struct{ A, B string }{"a", "b"}] != 2 {
			t.Error("owner map read failed")
		}
	})
}

func TestCallReceivesHostRequests(t *testing.T) {
	b := isolatebridge.New()
	type result struct {
		initial string
		second  string
		reply   string
		err     error
	}
	done := make(chan result, 1)
	go b.Run(func() {
		first, err := isolate.Call(2, nil)
		if err != nil {
			done <- result{err: err}
			return
		}
		ready := make(chan string, 1)
		go func() {
			// This native child must inherit the same boundary.
			request, err := isolate.Call(2, nil)
			if err != nil {
				ready <- err.Error()
				return
			}
			ready <- string(request)
		}()
		second := <-ready
		reply, err := isolate.Call(7, []byte("request"))
		done <- result{string(first), second, string(reply), err}
	})

	firstCommand := <-b.Commands()
	if firstCommand.Op != 2 || len(firstCommand.Payload) != 0 {
		t.Fatalf("first command = %+v", firstCommand)
	}
	initial := []byte("initial")
	firstCommand.Reply(initial, nil)
	initial[0] = 'X'
	secondCommand := <-b.Commands()
	if secondCommand.Op != 2 || len(secondCommand.Payload) != 0 {
		t.Fatalf("second command = %+v", secondCommand)
	}
	second := []byte("second")
	secondCommand.Reply(second, nil)
	second[0] = 'X'
	cmd := <-b.Commands()
	if cmd.ID != 3 || cmd.Op != 7 || string(cmd.Payload) != "request" {
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

func TestBoundaryTracksNativeChildren(t *testing.T) {
	b := isolatebridge.New()
	release := make(chan struct{})
	releaseChild := sync.OnceFunc(func() { close(release) })
	defer releaseChild()
	ready := make(chan struct{})
	exited := make(chan struct{})
	b.RunOwner(func() {
		b.Run(func() {
			if got := b.LiveGoroutines(); got != 1 {
				t.Fatalf("nested Run counted %d goroutines, want 1", got)
			}
			if got := b.RunningGoroutines(); got != 1 {
				t.Fatalf("nested Run counted %d running goroutines, want 1", got)
			}
			go func() {
				defer close(exited)
				close(ready)
				<-release
			}()
			<-ready
			if got := b.LiveGoroutines(); got != 2 {
				t.Fatalf("Run with parked child counted %d goroutines, want 2", got)
			}
			deadline := time.Now().Add(time.Second)
			for b.RunningGoroutines() != 1 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if got := b.RunningGoroutines(); got != 1 {
				t.Fatalf("Run with parked child counted %d running goroutines, want 1", got)
			}
		})
	})
	if got := b.LiveGoroutines(); got != 1 {
		t.Fatalf("after Run, live goroutines = %d, want parked child", got)
	}
	if got := b.RunningGoroutines(); got != 0 {
		t.Fatalf("after Run, running goroutines = %d, want 0", got)
	}
	releaseChild()
	<-exited
	deadline := time.Now().Add(time.Second)
	for b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := b.LiveGoroutines(); got != 0 {
		t.Fatalf("after child exit, live goroutines = %d, want 0", got)
	}
	if got := b.RunningGoroutines(); got != 0 {
		t.Fatalf("after child exit, running goroutines = %d, want 0", got)
	}
}

func TestBoundaryCountsRunnableChildren(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)
	b := isolatebridge.New()
	release := make(chan struct{})
	releaseChild := sync.OnceFunc(func() { close(release) })
	defer releaseChild()
	started := make(chan struct{})
	exited := make(chan struct{})
	b.Run(func() {
		go func() {
			defer close(exited)
			close(started)
			<-release
		}()
	})
	<-started
	deadline := time.Now().Add(time.Second)
	for b.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if b.LiveGoroutines() != 1 || b.RunningGoroutines() != 0 || b.RunnableGoroutines() != 0 {
		t.Fatalf("parked child: live=%d running=%d runnable=%d, want 1, 0, 0",
			b.LiveGoroutines(), b.RunningGoroutines(), b.RunnableGoroutines())
	}
	releaseChild()
	if got := b.RunnableGoroutines(); got != 1 {
		t.Fatalf("readied child: runnable=%d, want 1", got)
	}
	<-exited
	deadline = time.Now().Add(time.Second)
	for b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if b.LiveGoroutines() != 0 || b.RunnableGoroutines() != 0 {
		t.Fatalf("exited child: live=%d runnable=%d, want 0, 0",
			b.LiveGoroutines(), b.RunnableGoroutines())
	}
}

func TestBoundaryRevokesUnstartedChildren(t *testing.T) {
	b := isolatebridge.New()
	var release atomic.Bool
	ready := make(chan struct{})
	childExited := make(chan struct{})
	var grandchildCreated atomic.Bool
	var grandchildRan atomic.Bool
	b.Run(func() {
		go func() {
			defer close(childExited)
			close(ready)
			for !release.Load() {
			}
			grandchildCreated.Store(true)
			go func() { grandchildRan.Store(true) }()
		}()
		<-ready
	})
	b.RevokeUnstarted()
	b.RevokeUnstarted() // repeated revocation is harmless
	release.Store(true)
	<-childExited
	deadline := time.Now().Add(time.Second)
	for b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := b.LiveGoroutines(); got != 0 {
		t.Fatalf("after revocation, live goroutines = %d, want 0", got)
	}
	if !grandchildCreated.Load() {
		t.Fatal("already running child did not create a grandchild")
	}
	if grandchildRan.Load() {
		t.Fatal("revoked grandchild entered user code")
	}
}

func TestBoundaryStopWakesCallWaiters(t *testing.T) {
	b := isolatebridge.New()
	var callReturned, callDeferred atomic.Bool
	go b.Run(func() {
		defer callDeferred.Store(true)
		_, _ = b.Call(1, nil)
		callReturned.Store(true)
	})
	command := <-b.Commands()
	b.Stop()
	b.Stop()
	deadline := time.Now().Add(time.Second)
	for b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	command.Reply(nil, nil) // a late host reply must not block
	if callReturned.Load() || callDeferred.Load() || b.LiveGoroutines() != 0 {
		t.Fatalf("stopped Call returned=%t, deferred=%t, live=%d", callReturned.Load(), callDeferred.Load(), b.LiveGoroutines())
	}

	blocked := isolatebridge.New()
	sendReady := make(chan struct{})
	go func() {
		blocked.Run(func() {
			defer callDeferred.Store(true)
			close(sendReady)
			_, _ = blocked.Call(2, nil) // no host receiver
			callReturned.Store(true)
		})
	}()
	<-sendReady
	deadline = time.Now().Add(time.Second)
	for blocked.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := blocked.RunningGoroutines(); got != 0 {
		t.Fatalf("command sender did not park: running=%d", got)
	}
	blocked.Stop()
	deadline = time.Now().Add(time.Second)
	for blocked.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if callReturned.Load() || callDeferred.Load() || blocked.LiveGoroutines() != 0 {
		t.Fatalf("stopped command send returned=%t, deferred=%t, live=%d", callReturned.Load(), callDeferred.Load(), blocked.LiveGoroutines())
	}
}

func TestRevokedSleepDoesNotResumeUserCode(t *testing.T) {
	b := isolatebridge.New()
	entered := make(chan struct{})
	var resumed, deferred atomic.Bool
	go b.Run(func() {
		defer deferred.Store(true)
		close(entered)
		time.Sleep(time.Hour)
		resumed.Store(true)
	})
	<-entered
	deadline := time.Now().Add(time.Second)
	for b.RunningGoroutines() != 0 && b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if b.LiveGoroutines() != 1 || b.RunningGoroutines() != 0 {
		t.Fatalf("sleep did not park: live=%d running=%d", b.LiveGoroutines(), b.RunningGoroutines())
	}
	b.Stop()
	waitBoundaryExit(t, b)
	if resumed.Load() || deferred.Load() {
		t.Fatalf("sleep returned=%t, deferred=%t", resumed.Load(), deferred.Load())
	}
}

func TestSleepRevocationTimerRace(t *testing.T) {
	for n := 0; n < 200; n++ {
		b := isolatebridge.New()
		entered := make(chan struct{})
		go func() {
			b.Run(func() {
				close(entered)
				time.Sleep(time.Millisecond)
			})
		}()
		<-entered
		deadline := time.Now().Add(5 * time.Second)
		for b.LiveGoroutines() != 0 && b.RunningGoroutines() != 0 && time.Now().Before(deadline) {
			runtime.Gosched()
		}
		if b.LiveGoroutines() != 0 && b.RunningGoroutines() != 0 {
			t.Fatal("short sleeper did not park")
		}
		time.Sleep(time.Duration(n%4) * 250 * time.Microsecond)
		b.Stop()
		waitBoundaryExit(t, b)
	}
}

func waitBoundaryExit(t *testing.T, b *isolatebridge.Boundary) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if got := b.LiveGoroutines(); got != 0 {
		t.Fatalf("revoked boundary still has %d live goroutines", got)
	}
}

func TestPermanentParkRevocationRace(t *testing.T) {
	for n := 0; n < 200; n++ {
		b := isolatebridge.New()
		entered := make(chan struct{})
		go func() {
			b.Run(func() {
				close(entered)
				if n%2 == 0 {
					var ch chan int
					<-ch
				} else {
					select {}
				}
			})
		}()
		<-entered
		b.Stop()
		waitBoundaryExit(t, b)
	}
}

func TestRevokedChannelWaitDoesNotResumeUserCode(t *testing.T) {
	for _, name := range []string{"receive", "send"} {
		t.Run(name, func(t *testing.T) {
			b := isolatebridge.New()
			ch := make(chan int)
			entered := make(chan struct{})
			var resumed, deferred atomic.Bool
			go b.Run(func() {
				defer deferred.Store(true)
				close(entered)
				if name == "receive" {
					<-ch
				} else {
					ch <- 1
				}
				resumed.Store(true)
			})
			<-entered
			deadline := time.Now().Add(time.Second)
			for b.RunningGoroutines() != 0 && b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if b.LiveGoroutines() != 1 || b.RunningGoroutines() != 0 {
				t.Fatalf("channel operation did not park: live=%d running=%d", b.LiveGoroutines(), b.RunningGoroutines())
			}
			b.Stop()
			waitBoundaryExit(t, b)
			if name == "receive" {
				select {
				case ch <- 1:
					t.Fatal("revoked receive remained queued")
				default:
				}
			} else {
				select {
				case <-ch:
					t.Fatal("revoked send remained queued")
				default:
				}
			}
			if resumed.Load() || deferred.Load() {
				t.Fatalf("channel operation returned=%t, deferred=%t", resumed.Load(), deferred.Load())
			}
		})
	}
}

func TestChannelRevocationCloseRace(t *testing.T) {
	for n := 0; n < 200; n++ {
		b := isolatebridge.New()
		ch := make(chan int)
		entered := make(chan struct{})
		go func() {
			b.Run(func() {
				close(entered)
				<-ch
			})
		}()
		<-entered
		closed := make(chan struct{})
		go func() {
			close(ch)
			close(closed)
		}()
		b.Stop()
		<-closed
		waitBoundaryExit(t, b)
	}
}

func TestChannelRevocationLeavesProcessWaiter(t *testing.T) {
	b := isolatebridge.New()
	ch := make(chan int)
	entered := make(chan struct{})
	go func() {
		b.Run(func() {
			close(entered)
			<-ch
		})
	}()
	<-entered
	deadline := time.Now().Add(time.Second)
	for b.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if b.RunningGoroutines() != 0 {
		t.Fatal("isolate waiter did not park")
	}
	hostEntered := make(chan struct{})
	hostReceived := make(chan int, 1)
	go func() {
		close(hostEntered)
		hostReceived <- <-ch
	}()
	<-hostEntered
	b.Stop()
	waitBoundaryExit(t, b)
	select {
	case ch <- 7:
	case <-time.After(5 * time.Second):
		t.Fatal("process waiter did not remain on channel")
	}
	select {
	case got := <-hostReceived:
		if got != 7 {
			t.Fatalf("process waiter received %d, want 7", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process waiter did not receive")
	}
}

func TestRevokedSelectWaitDoesNotResumeUserCode(t *testing.T) {
	for _, name := range []string{"receive", "send"} {
		t.Run(name, func(t *testing.T) {
			b := isolatebridge.New()
			ch := make(chan int)
			if name == "send" {
				ch = make(chan int, 1)
				ch <- 0
			}
			other := make(chan int)
			entered := make(chan struct{})
			var resumed, deferred atomic.Bool
			go b.Run(func() {
				defer deferred.Store(true)
				close(entered)
				if name == "receive" {
					select {
					case <-ch:
					case <-other:
					}
				} else {
					select {
					case ch <- 1:
					case other <- 2:
					}
				}
				resumed.Store(true)
			})
			<-entered
			deadline := time.Now().Add(time.Second)
			for b.RunningGoroutines() != 0 && b.LiveGoroutines() != 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if b.LiveGoroutines() != 1 || b.RunningGoroutines() != 0 {
				t.Fatalf("select did not park: live=%d running=%d", b.LiveGoroutines(), b.RunningGoroutines())
			}
			b.Stop()
			waitBoundaryExit(t, b)
			if name == "receive" {
				for _, candidate := range []chan int{ch, other} {
					select {
					case candidate <- 1:
						t.Fatal("revoked select receive remained queued")
					default:
					}
				}
			} else {
				if got := <-ch; got != 0 {
					t.Fatalf("buffered value = %d, want 0", got)
				}
				for _, candidate := range []chan int{ch, other} {
					select {
					case <-candidate:
						t.Fatal("revoked select send remained queued")
					default:
					}
				}
			}
			if resumed.Load() || deferred.Load() {
				t.Fatalf("select returned=%t, deferred=%t", resumed.Load(), deferred.Load())
			}
		})
	}
}

func TestSelectRevocationCloseRace(t *testing.T) {
	for n := 0; n < 200; n++ {
		b := isolatebridge.New()
		ch := make(chan int)
		other := make(chan int)
		entered := make(chan struct{})
		go func() {
			b.Run(func() {
				close(entered)
				select {
				case <-ch:
				case <-other:
				}
			})
		}()
		<-entered
		closed := make(chan struct{})
		go func() {
			close(ch)
			close(closed)
		}()
		b.Stop()
		<-closed
		waitBoundaryExit(t, b)
		select {
		case other <- 1:
			t.Fatal("losing select case remained queued")
		default:
		}
	}
}

func TestRevokedGoschedLoopExits(t *testing.T) {
	b := isolatebridge.New()
	started := make(chan struct{})
	var deferred atomic.Bool
	go b.Run(func() {
		defer deferred.Store(true)
		close(started)
		for {
			runtime.Gosched()
		}
	})
	<-started
	b.Stop()
	waitBoundaryExit(t, b)
	if deferred.Load() {
		t.Fatal("revoked Gosched loop ran user defer")
	}
}

func TestBoundaryCountsCoroutineSwitches(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		seq := iter.Seq[int](func(yield func(int) bool) {
			if got := b.RunningGoroutines(); got != 1 {
				t.Errorf("inside coroutine, running goroutines = %d, want 1", got)
			}
			yield(7)
		})
		next, stop := iter.Pull(seq)
		if got := b.LiveGoroutines(); got != 2 {
			t.Errorf("with parked coroutine, live goroutines = %d, want 2", got)
		}
		if got := b.RunningGoroutines(); got != 1 {
			t.Errorf("with parked coroutine, running goroutines = %d, want 1", got)
		}
		if value, ok := next(); !ok || value != 7 {
			t.Errorf("next = %d, %v, want 7, true", value, ok)
		}
		if got := b.RunningGoroutines(); got != 1 {
			t.Errorf("after yield, running goroutines = %d, want 1", got)
		}
		stop()
		if got := b.RunningGoroutines(); got != 1 {
			t.Errorf("after stop, running goroutines = %d, want 1", got)
		}
	})
	if got := b.LiveGoroutines(); got != 0 {
		t.Errorf("after Run, live goroutines = %d, want 0", got)
	}
	if got := b.RunningGoroutines(); got != 0 {
		t.Errorf("after Run, running goroutines = %d, want 0", got)
	}
}

func TestBoundaryRejectsNestedOtherInstance(t *testing.T) {
	first, second := isolatebridge.New(), isolatebridge.New()
	first.Run(func() {
		defer func() {
			got, ok := recover().(string)
			if !ok || got != "isolate: cannot nest different goroutine groups" {
				t.Errorf("nested boundary panic = %v", got)
			}
		}()
		second.Run(func() { t.Error("nested instance ran") })
	})
	if first.LiveGoroutines() != 0 || second.LiveGoroutines() != 0 {
		t.Fatalf("leaked group membership: first=%d second=%d", first.LiveGoroutines(), second.LiveGoroutines())
	}
}

func TestConcurrentCallsKeepTheirReplies(t *testing.T) {
	b := isolatebridge.New()
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

	b := isolatebridge.New()
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
	b := isolatebridge.New()
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

func TestProcessRuntimeAPIsRejectIsolate(t *testing.T) {
	checkPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			got, ok := recover().(string)
			want := "runtime." + name + " is unavailable in an isolate"
			if !ok || got != want {
				t.Errorf("%s panic = %q, want %q", name, got, want)
			}
		}()
		fn()
	}
	b := isolatebridge.New()
	b.Run(func() {
		checkPanic("GOMAXPROCS", func() { runtime.GOMAXPROCS(0) })
		checkPanic("SetDefaultGOMAXPROCS", runtime.SetDefaultGOMAXPROCS)
		checkPanic("NumCPU", func() { runtime.NumCPU() })
		checkPanic("NumCgoCall", func() { runtime.NumCgoCall() })
		checkPanic("NumGoroutine", func() { runtime.NumGoroutine() })
		checkPanic("ReadMemStats", func() { runtime.ReadMemStats(new(runtime.MemStats)) })
		checkPanic("GC", runtime.GC)
		checkPanic("SetCPUProfileRate", func() { runtime.SetCPUProfileRate(0) })
		checkPanic("SetBlockProfileRate", func() { runtime.SetBlockProfileRate(0) })
		checkPanic("SetMutexProfileFraction", func() { runtime.SetMutexProfileFraction(-1) })
		checkPanic("MemProfile", func() { runtime.MemProfile(nil, false) })
		checkPanic("BlockProfile", func() { runtime.BlockProfile(nil) })
		checkPanic("MutexProfile", func() { runtime.MutexProfile(nil) })
		checkPanic("ThreadCreateProfile", func() { runtime.ThreadCreateProfile(nil) })
		checkPanic("GoroutineProfile", func() { runtime.GoroutineProfile(nil) })
		checkPanic("LockOSThread", func() {
			runtime.LockOSThread()
			runtime.UnlockOSThread()
		})
		checkPanic("UnlockOSThread", runtime.UnlockOSThread)
		child := make(chan bool, 1)
		go func() {
			defer func() { child <- recover() != nil }()
			runtime.NumCPU()
		}()
		if !<-child {
			t.Error("child goroutine read process CPU count")
		}
	})
}

func TestProcessDebugAPIsRejectIsolate(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		for _, tt := range []struct {
			name string
			call func()
		}{
			{"ReadGCStats", func() { rdebug.ReadGCStats(new(rdebug.GCStats)) }},
			{"SetGCPercent", func() {
				prev := rdebug.SetGCPercent(100)
				rdebug.SetGCPercent(prev)
			}},
			{"FreeOSMemory", rdebug.FreeOSMemory},
			{"SetMaxStack", func() {
				prev := rdebug.SetMaxStack(1 << 30)
				rdebug.SetMaxStack(prev)
			}},
			{"SetMaxThreads", func() {
				prev := rdebug.SetMaxThreads(10000)
				rdebug.SetMaxThreads(prev)
			}},
			{"SetPanicOnFault", func() {
				prev := rdebug.SetPanicOnFault(false)
				rdebug.SetPanicOnFault(prev)
			}},
			{"SetMemoryLimit", func() { rdebug.SetMemoryLimit(-1) }},
			{"PrintStack", rdebug.PrintStack},
			{"Stack", func() { rdebug.Stack() }},
			{"SetCrashOutput", func() { _ = rdebug.SetCrashOutput(nil, rdebug.CrashOptions{}) }},
			{"ReadBuildInfo", func() { rdebug.ReadBuildInfo() }},
			{"WriteHeapDump", func() { rdebug.WriteHeapDump(^uintptr(0)) }},
			{"SetTraceback", func() { rdebug.SetTraceback("single") }},
		} {
			func() {
				defer func() {
					got, ok := recover().(string)
					want := "runtime/debug." + tt.name + " is unavailable in an isolate"
					if !ok || got != want {
						t.Errorf("%s panic = %q, want %q", tt.name, got, want)
					}
				}()
				tt.call()
			}()
		}
	})
}

func TestProcessProfilesRejectIsolate(t *testing.T) {
	profile := pprof.Lookup("heap")
	defer pprof.StopCPUProfile()
	b := isolatebridge.New()
	b.Run(func() {
		pprof.Do(context.Background(), pprof.Labels("key", "value"), func(ctx context.Context) {
			if got, ok := pprof.Label(ctx, "key"); !ok || got != "value" {
				t.Errorf("goroutine label = %q, %v", got, ok)
			}
		})
		for _, tt := range []struct {
			name string
			call func()
		}{
			{"NewProfile", func() { pprof.NewProfile("isolate.test") }},
			{"Lookup", func() { pprof.Lookup("heap") }},
			{"Profiles", func() { pprof.Profiles() }},
			{"Profile.Count", func() { profile.Count() }},
			{"Profile.Add", func() { profile.Add(new(int), 0) }},
			{"Profile.Remove", func() { profile.Remove(new(int)) }},
			{"Profile.WriteTo", func() { _ = profile.WriteTo(io.Discard, 0) }},
			{"WriteHeapProfile", func() { _ = pprof.WriteHeapProfile(io.Discard) }},
			{"StartCPUProfile", func() { _ = pprof.StartCPUProfile(io.Discard) }},
			{"StopCPUProfile", pprof.StopCPUProfile},
		} {
			func() {
				defer func() {
					want := "runtime/pprof." + tt.name + " is unavailable in an isolate"
					if got := recover(); got != want {
						t.Errorf("%s panic = %v, want %q", tt.name, got, want)
					}
				}()
				tt.call()
			}()
		}
	})
}

func TestTraceAPIsKeepHostTraceIsolated(t *testing.T) {
	var output bytes.Buffer
	if err := rtrace.Start(&output); err != nil {
		t.Fatal(err)
	}
	defer rtrace.Stop()
	if !rtrace.IsEnabled() {
		t.Fatal("host trace did not start")
	}
	b := isolatebridge.New()
	b.Run(func() {
		if rtrace.IsEnabled() {
			t.Error("isolate observed host tracing")
		}
		ctx, task := rtrace.NewTask(context.Background(), "isolate-task")
		if ctx == nil || task == nil {
			t.Fatal("trace.NewTask returned a nil context or task")
		}
		rtrace.Log(ctx, "isolate", "message")
		rtrace.Logf(ctx, "isolate", "message %d", 1)
		called := false
		rtrace.WithRegion(ctx, "isolate-region", func() { called = true })
		if !called {
			t.Error("trace.WithRegion did not call its function")
		}
		rtrace.StartRegion(ctx, "isolate-region").End()
		task.End()

		fr := rtrace.NewFlightRecorder(rtrace.FlightRecorderConfig{})
		for _, tt := range []struct {
			name string
			call func()
		}{
			{"Start", func() { _ = rtrace.Start(io.Discard) }},
			{"Stop", rtrace.Stop},
			{"FlightRecorder.Start", func() { _ = fr.Start() }},
			{"FlightRecorder.Stop", fr.Stop},
			{"FlightRecorder.Enabled", func() { _ = fr.Enabled() }},
			{"FlightRecorder.WriteTo", func() { _, _ = fr.WriteTo(io.Discard) }},
		} {
			func() {
				defer func() {
					want := "runtime/trace." + tt.name + " is unavailable in an isolate"
					if got := recover(); got != want {
						t.Errorf("%s panic = %v, want %q", tt.name, got, want)
					}
				}()
				tt.call()
			}()
		}
	})
	if !rtrace.IsEnabled() {
		t.Fatal("isolate stopped the host trace")
	}
}

func TestEntropyAndMetricsRejectIsolate(t *testing.T) {
	hostDescriptions := metrics.All()
	if len(hostDescriptions) == 0 {
		t.Fatal("runtime/metrics.All returned no descriptions")
	}
	firstName := hostDescriptions[0].Name
	b := isolatebridge.New()
	b.Run(func() {
		descriptions := metrics.All()
		descriptions[0].Name = "isolate-only"
		for _, tt := range []struct {
			want string
			call func()
		}{
			{"crypto/rand.Read is unavailable in an isolate", func() {
				_, _ = crand.Read(make([]byte, 1))
			}},
			{"crypto/rand.Reader is unavailable in an isolate", func() {
				_, _ = io.ReadFull(crand.Reader, make([]byte, 1))
			}},
			{"crypto/internal/fips140/drbg.Read is unavailable in an isolate", func() {
				_, _ = mlkem.GenerateKey768()
			}},
			{"runtime/metrics.Read is unavailable in an isolate", func() {
				metrics.Read([]metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}})
			}},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
	if got := metrics.All()[0].Name; got != firstName {
		t.Fatalf("isolate changed host metric name to %q", got)
	}
}

func TestStandardLoggerRejectsIsolate(t *testing.T) {
	oldWriter := log.Writer()
	defer log.SetOutput(oldWriter)
	var hostOutput bytes.Buffer
	log.SetOutput(&hostOutput)
	b := isolatebridge.New()
	b.Run(func() {
		var isolateOutput bytes.Buffer
		local := log.New(&isolateOutput, "", 0)
		local.Print("local")
		if got := isolateOutput.String(); got != "local\n" {
			t.Errorf("local logger output = %q", got)
		}
		for _, tt := range []struct {
			want string
			call func()
		}{
			{"log.Output is unavailable in an isolate", func() { log.Print("host") }},
			{"log.Default is unavailable in an isolate", func() { _ = log.Default() }},
			{"log.SetOutput is unavailable in an isolate", func() { log.SetOutput(io.Discard) }},
			{"log.Flags is unavailable in an isolate", func() { _ = log.Flags() }},
			{"log.Writer is unavailable in an isolate", func() { _ = log.Writer() }},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
	if got := hostOutput.String(); got != "" {
		t.Fatalf("standard logger wrote %q", got)
	}
}

func TestSlogDefaultRejectsIsolate(t *testing.T) {
	hostDefault := slog.Default()
	hostHandler := hostDefault.Handler()
	oldWriter, oldFlags := log.Writer(), log.Flags()
	defer func() {
		slog.SetDefault(hostDefault)
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	var hostOutput bytes.Buffer
	alias := slog.New(slog.NewTextHandler(&hostOutput, nil))
	aliasClone := alias.With("before", "default")
	slog.SetDefault(alias)
	bridgeWriter := log.Writer()
	slog.SetDefault(hostDefault)
	log.SetOutput(&hostOutput)

	b := isolatebridge.New()
	b.Run(func() {
		var localOutput bytes.Buffer
		local := slog.New(slog.NewTextHandler(&localOutput, &slog.HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey {
					return slog.Attr{}
				}
				return a
			},
		}))
		local.Info("local")
		slog.NewLogLogger(local.Handler(), slog.LevelInfo).Print("bridge")
		if got := localOutput.String(); !strings.Contains(got, "msg=local") || !strings.Contains(got, "msg=bridge") {
			t.Errorf("local slog output = %q", got)
		}

		for _, tt := range []struct {
			want string
			call func()
		}{
			{"log/slog.Default is unavailable in an isolate", func() { _ = slog.Default() }},
			{"log/slog.Default is unavailable in an isolate", func() { slog.Info("host") }},
			{"log/slog.SetDefault is unavailable in an isolate", func() { slog.SetDefault(local) }},
			{"log/slog.SetLogLoggerLevel is unavailable in an isolate", func() { slog.SetLogLoggerLevel(slog.LevelDebug) }},
			{"log/slog default Logger is unavailable in an isolate", func() { hostDefault.Info("host") }},
			{"log/slog default Logger is unavailable in an isolate", func() { _ = hostDefault.With() }},
			{"log/slog default Logger is unavailable in an isolate", func() { alias.Info("host") }},
			{"log/slog default Logger is unavailable in an isolate", func() { aliasClone.Info("host") }},
			{"log/slog default Handler is unavailable in an isolate", func() { _ = hostHandler.Enabled(context.Background(), slog.LevelInfo) }},
			{"log/slog default Handler is unavailable in an isolate", func() { _ = hostHandler.Handle(context.Background(), slog.Record{}) }},
			{"log/slog default Handler is unavailable in an isolate", func() { slog.New(hostHandler).Info("host") }},
			{"log/slog default bridge is unavailable in an isolate", func() { _, _ = bridgeWriter.Write([]byte("host")) }},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
	if got := hostOutput.String(); got != "" {
		t.Fatalf("process slog output = %q", got)
	}
	if got := slog.Default(); got != hostDefault {
		t.Fatalf("isolate changed process slog default to %p", got)
	}
}

func TestExpvarRegistryRejectsIsolate(t *testing.T) {
	handler := expvar.Handler()
	name := "isolate-unpublished-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	b := isolatebridge.New()
	b.Run(func() {
		var local expvar.Int
		local.Add(3)
		var localMap expvar.Map
		localMap.Init().Set("count", &local)
		if got := localMap.Get("count").(*expvar.Int).Value(); got != 3 {
			t.Errorf("local expvar value = %d", got)
		}
		for _, tt := range []struct {
			want string
			call func()
		}{
			{"expvar.Publish is unavailable in an isolate", func() { expvar.Publish(name, &local) }},
			{"expvar.Get is unavailable in an isolate", func() { _ = expvar.Get("cmdline") }},
			{"expvar.NewInt is unavailable in an isolate", func() { _ = expvar.NewInt(name) }},
			{"expvar.NewFloat is unavailable in an isolate", func() { _ = expvar.NewFloat(name) }},
			{"expvar.NewMap is unavailable in an isolate", func() { _ = expvar.NewMap(name) }},
			{"expvar.NewString is unavailable in an isolate", func() { _ = expvar.NewString(name) }},
			{"expvar.Do is unavailable in an isolate", func() { expvar.Do(func(expvar.KeyValue) { t.Error("process registry callback ran") }) }},
			{"expvar.Handler is unavailable in an isolate", func() { _ = expvar.Handler() }},
			{"expvar.Handler is unavailable in an isolate", func() { handler.ServeHTTP(nil, nil) }},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
	if got := expvar.Get(name); got != nil {
		t.Fatalf("isolate published process expvar %q: %v", name, got)
	}
}

func TestFlagCommandLineRejectsIsolate(t *testing.T) {
	defaultSet := flag.CommandLine
	all := flag.All()
	name := "isolate-unregistered-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	hostBool := false
	b := isolatebridge.New()
	b.Run(func() {
		local := flag.NewFlagSet("local", flag.ContinueOnError)
		value := local.Int("count", 3, "local count")
		if err := local.Parse([]string{"-count=5", "argument"}); err != nil {
			t.Errorf("local Parse: %v", err)
		}
		if *value != 5 || local.Arg(0) != "argument" {
			t.Errorf("local flag state = %d, %q", *value, local.Arg(0))
		}
		for _, call := range []func(){
			func() { _ = flag.Bool(name, false, "host flag") },
			func() { _ = flag.Lookup("test.v") },
			func() { _ = flag.NArg() },
			func() { _ = flag.Set("test.v", "true") },
			func() { flag.Parse() },
			func() { _ = flag.All() },
			func() { all(func(*flag.Flag) bool { t.Error("process flag yielded"); return false }) },
			func() { defaultSet.SetOutput(io.Discard) },
			func() { defaultSet.BoolVar(&hostBool, name, true, "host flag") },
			func() { _ = defaultSet.Args() },
			func() { defaultSet.Init("changed", flag.ContinueOnError) },
		} {
			func() {
				defer func() {
					if got := recover(); got != "flag.CommandLine is unavailable in an isolate" {
						t.Errorf("panic = %v, want flag.CommandLine rejection", got)
					}
				}()
				call()
			}()
		}
	})
	if got := flag.Lookup(name); got != nil {
		t.Fatalf("isolate registered process flag %q", name)
	}
	if hostBool {
		t.Fatal("isolate changed host flag value before rejection")
	}
}

func TestProcessLaunchAPIsRejectIsolate(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		for _, tt := range []struct {
			name string
			want string
			call func()
		}{
			{"os.StartProcess", "os.StartProcess is unavailable in an isolate", func() {
				_, _ = os.StartProcess("/nonexistent", []string{"/nonexistent"}, nil)
			}},
			{"os/exec.Cmd.Start", "os/exec.Cmd.Start is unavailable in an isolate", func() {
				_ = exec.Command("/nonexistent").Start()
			}},
			{"syscall.StartProcess", "syscall.StartProcess is unavailable in an isolate", func() {
				_, _, _ = syscall.StartProcess("/nonexistent", []string{"/nonexistent"}, nil)
			}},
			{"syscall.Exec", "syscall.Exec is unavailable in an isolate", func() {
				_ = syscall.Exec("/nonexistent", []string{"/nonexistent"}, nil)
			}},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("%s panic = %v, want %q", tt.name, got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
}

func TestEnvironmentAPIsRejectIsolate(t *testing.T) {
	const key = "GO_ISOLATE_ENV_GUARD_TEST"
	t.Setenv(key, "host")
	b := isolatebridge.New()
	b.Run(func() {
		for _, tt := range []struct {
			want string
			call func()
		}{
			{"os.Getenv is unavailable in an isolate", func() { _ = os.Getenv(key) }},
			{"os.LookupEnv is unavailable in an isolate", func() { _, _ = os.LookupEnv(key) }},
			{"os.Setenv is unavailable in an isolate", func() { _ = os.Setenv(key, "isolate") }},
			{"os.Unsetenv is unavailable in an isolate", func() { _ = os.Unsetenv(key) }},
			{"os.Clearenv is unavailable in an isolate", os.Clearenv},
			{"os.Environ is unavailable in an isolate", func() { _ = os.Environ() }},
			{"syscall.Getenv is unavailable in an isolate", func() { _, _ = syscall.Getenv(key) }},
			{"syscall.Setenv is unavailable in an isolate", func() { _ = syscall.Setenv(key, "isolate") }},
			{"syscall.Unsetenv is unavailable in an isolate", func() { _ = syscall.Unsetenv(key) }},
			{"syscall.Clearenv is unavailable in an isolate", syscall.Clearenv},
			{"syscall.Environ is unavailable in an isolate", func() { _ = syscall.Environ() }},
		} {
			func() {
				defer func() {
					if got := recover(); got != tt.want {
						t.Errorf("environment panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			}()
		}
	})
	if got := os.Getenv(key); got != "host" {
		t.Fatalf("host environment changed to %q", got)
	}
}

func TestSignalAPIsRejectIsolate(t *testing.T) {
	ch := make(chan os.Signal, 1)
	b := isolatebridge.New()
	b.Run(func() {
		for _, tt := range []struct {
			name string
			call func()
		}{
			{"Ignore", func() { signal.Ignore(nil) }},
			{"Ignored", func() { _ = signal.Ignored(nil) }},
			{"Notify", func() { signal.Notify(ch, nil) }},
			{"Reset", func() { signal.Reset(nil) }},
			{"Stop", func() { signal.Stop(ch) }},
			{"NotifyContext", func() {
				_, stop := signal.NotifyContext(context.Background(), nil)
				stop()
			}},
		} {
			func() {
				defer func() {
					want := "os/signal." + tt.name + " is unavailable in an isolate"
					if got := recover(); got != want {
						t.Errorf("%s panic = %v, want %q", tt.name, got, want)
					}
				}()
				tt.call()
			}()
		}
	})
}

func TestAfterFuncRejectsUnownedCallback(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		defer func() {
			got, ok := recover().(string)
			if !ok || got != "time: AfterFunc is unavailable inside an isolate" {
				t.Errorf("AfterFunc panic = %v, want isolate rejection", got)
			}
		}()
		time.AfterFunc(0, func() {})
	})
}

func TestContextCallbacksRejectUnownedRegistration(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
		call func()
	}{
		{"AfterFunc", "context: AfterFunc is unavailable inside an isolate", func() {
			context.AfterFunc(context.Background(), func() {})
		}},
		{"WithTimeout", "context: future deadlines are unavailable inside an isolate", func() {
			_, cancel := context.WithTimeout(context.Background(), time.Hour)
			cancel()
		}},
		{"CustomAfterFuncParent", "context: custom AfterFunc parent is unavailable inside an isolate", func() {
			parent := &customAfterFuncParent{Context: context.Background(), done: make(chan struct{})}
			_, cancel := context.WithCancel(parent)
			cancel()
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := isolatebridge.New()
			b.Run(func() {
				defer func() {
					got, ok := recover().(string)
					if !ok || got != tt.want {
						t.Errorf("panic = %v, want %q", got, tt.want)
					}
				}()
				tt.call()
			})
		})
	}
}

func TestInstanceOwnerSpansInitializationAndChildren(t *testing.T) {
	name := "test-instance-owner-context-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	var initOwner uintptr
	type observedOwners struct{ main, child uintptr }
	observed := make(chan observedOwners, 1)
	isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) {
			initOwner = runtimeOwner()
			initializerChild := make(chan uintptr, 1)
			go func() { initializerChild <- runtimeOwner() }()
			if got := <-initializerChild; got != initOwner {
				t.Errorf("initializer child owner = %d, want %d", got, initOwner)
			}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("Call was available during package initialization")
					}
				}()
				_, _ = isolate.Call(2, nil)
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
					t.Errorf("state runner owner = %d, want %d", got, initOwner)
				}
				fn()
			}, nil
		},
		Main: func() {
			child := make(chan uintptr, 1)
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
	if initOwner == 0 || runtimeOwner() != 0 {
		t.Fatalf("owner after New: initializer=%d host=%d", initOwner, runtimeOwner())
	}
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	got := <-observed
	<-instance.Done()
	if got.main != initOwner || got.child != initOwner || runtimeOwner() != 0 {
		t.Fatalf("owners: initializer=%d main=%d child=%d host=%d", initOwner, got.main, got.child, runtimeOwner())
	}
	firstOwner := initOwner
	if _, err := isolate.New(isolate.Config{Program: program}); err != nil {
		t.Fatal(err)
	}
	if initOwner <= firstOwner || runtimeOwner() != 0 {
		t.Fatalf("second owner=%d, first owner=%d, host=%d", initOwner, firstOwner, runtimeOwner())
	}
}

func TestMainFailureReportedToHost(t *testing.T) {
	for _, tt := range []struct {
		name string
		main func()
		want string
	}{
		{"return", func() {}, ""},
		{"panic", func() { panic("boom") }, "isolate: main panicked"},
		{"Goexit", runtime.Goexit, "isolate: main goroutine exited without returning"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			name := "test-main-failure-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
			isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{
				NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
				Main:     tt.main,
			})
			program, ok := isolate.LookupProgram(name)
			if !ok {
				t.Fatal("missing test program")
			}
			instance, err := isolate.New(isolate.Config{Program: program})
			if err != nil {
				t.Fatal(err)
			}
			if err := instance.Wait(); err == nil || err.Error() != "isolate: instance not started" {
				t.Fatalf("Wait before Start = %v", err)
			}
			if err := instance.Start(); err != nil {
				t.Fatal(err)
			}
			<-instance.Done()
			got := instance.Wait()
			if tt.want == "" && got != nil || tt.want != "" && (got == nil || got.Error() != tt.want) {
				t.Fatalf("Wait = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestInitializerFailureReportedToHost(t *testing.T) {
	for _, tt := range []struct {
		name string
		init func() (func(func()), error)
		want string
	}{
		{"panic", func() (func(func()), error) { panic("boom") }, "isolate: package initializer panicked"},
		{"Goexit", func() (func(func()), error) { runtime.Goexit(); return nil, nil }, "isolate: package initializer goroutine exited without returning"},
		{"error", func() (func(func()), error) { return nil, errors.New("isolate-owned error") }, "isolate: package initialization failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			name := "test-initializer-failure-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
			isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{NewState: tt.init, Main: func() {}})
			program, ok := isolate.LookupProgram(name)
			if !ok {
				t.Fatal("missing test program")
			}
			instance, err := isolate.New(isolate.Config{Program: program})
			if instance != nil || err == nil || err.Error() != tt.want {
				t.Fatalf("New = %v, %v, want %q", instance, err, tt.want)
			}
			if owner := runtimeOwner(); owner != 0 {
				t.Fatalf("owner after failed New = %d, want process owner", owner)
			}
		})
	}
}

func TestCallCopiesLargeRequestIntoProcessContext(t *testing.T) {
	name := "test-call-allocation-origin-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	type origins struct {
		owner, request, reply uintptr
		requestOK, replyOK    bool
		err                   error
	}
	observed := make(chan origins, 1)
	isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			owner := runtimeOwner()
			request := make([]byte, 64<<10)
			requestOrigin, requestOK := byteSliceOrigin(request)
			reply, err := isolate.Call(7, request)
			replyOrigin, replyOK := byteSliceOrigin(reply)
			observed <- origins{owner, requestOrigin, replyOrigin, requestOK, replyOK, err}
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
	if err := instance.Start(); err != nil {
		t.Fatal(err)
	}
	command := <-instance.Commands()
	hostOrigin, hostOK := byteSliceOrigin(command.Payload)
	command.Reply(make([]byte, 64<<10), nil)
	got := <-observed
	<-instance.Done()
	if command.ID != 1 || command.Op != 7 || got.err != nil {
		t.Fatalf("command ID=%d op=%d, reply error=%v", command.ID, command.Op, got.err)
	}
	if got.owner == 0 || !got.requestOK || got.request != got.owner || !hostOK || hostOrigin != 0 || !got.replyOK || got.reply != got.owner {
		t.Fatalf("origins: %+v, host request=%d tracked=%t", got, hostOrigin, hostOK)
	}
}
