// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e1

package isolateproto

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var nativeCoverageID atomic.Uint64

// TestNativeCoverage runs E1's native-operation sample. It documents which
// work the pure-Go scheduler cannot see; it is not a Phase 2 conformance test.
func TestNativeCoverage(t *testing.T) {
	gate := make(chan struct{})
	done := make(chan struct{})
	seen := make(chan struct {
		mapItems   int
		reflected  int
		nativeNow  time.Time
		logicalNow time.Time
	}, 1)
	entry := Register(fmt.Sprintf("isolateproto.phase0.native-coverage.%d", nativeCoverageID.Add(1)), func(task *Task, _ []byte) ([]byte, error) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		values := make(chan int, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			values <- 7
			mu.Unlock()
		}()
		wg.Wait()
		select {
		case <-values:
		default:
			panic("native channel send did not happen")
		}
		values <- 8
		chosen, received, ok := reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(values)}})
		if chosen != 0 || !ok || int(received.Int()) != 8 {
			panic("reflect.Select did not receive the value")
		}
		cond := sync.NewCond(&mu)
		ready := false
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			ready = true
			cond.Signal()
			mu.Unlock()
		}()
		mu.Lock()
		for !ready {
			cond.Wait()
		}
		mu.Unlock()
		wg.Wait()
		m := map[string]int{"a": 1, "b": 2}
		observation := struct {
			mapItems   int
			reflected  int
			nativeNow  time.Time
			logicalNow time.Time
		}{nativeNow: time.Now(), logicalNow: task.Now()}
		for range m {
			observation.mapItems++
		}
		iter := reflect.ValueOf(m).MapRange()
		for iter.Next() {
			observation.reflected++
		}
		time.Sleep(time.Millisecond)
		deadline, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Millisecond))
		<-deadline.Done()
		cancel()
		timerDone := make(chan struct{})
		time.AfterFunc(time.Millisecond, func() { close(timerDone) })
		<-timerDone
		started := make(chan struct{})
		go func() {
			close(started)
			<-gate // native parked goroutine, invisible to the coordinator
			close(done)
		}()
		<-started
		seen <- observation
		_, err := task.Call(99, nil)
		return nil, err
	})
	logical := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	iso, err := New(Config{Entry: entry, Clock: logical})
	if err != nil {
		t.Fatal(err)
	}
	state, commands, err := iso.Resume(context.Background(), nil)
	if err != nil || state != Quiescent || len(commands) != 1 {
		t.Fatalf("state=%v commands=%v err=%v", state, commands, err)
	}
	observation := <-seen
	if observation.mapItems != 2 || observation.reflected != 2 || !observation.logicalNow.Equal(logical) || observation.nativeNow.Equal(logical) {
		t.Fatalf("observation=%+v", observation)
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native goroutine did not exit")
	}
	state, _, err = iso.Resume(context.Background(), []Event{{ID: commands[0].ID}})
	if err != nil || state != Completed {
		t.Fatalf("state=%v err=%v", state, err)
	}
}
