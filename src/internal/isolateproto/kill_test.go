// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolateproto

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

var killTestID atomic.Uint64

func registerKillTestEntry(fn EntryFunc) Entry {
	return Register(fmt.Sprintf("isolateproto.test.kill.%d", killTestID.Add(1)), fn)
}

func TestKillBeforeStart(t *testing.T) {
	var ran atomic.Bool
	entry := registerKillTestEntry(func(_ *Task, _ []byte) ([]byte, error) {
		ran.Store(true)
		return nil, nil
	})
	iso := newTestIsolate(t, Config{Entry: entry})
	if err := iso.Kill(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, cmds, err := iso.Resume(context.Background(), nil)
	if err != nil || state != Killed || len(cmds) != 0 || ran.Load() {
		t.Fatalf("state=%v commands=%v err=%v ran=%v", state, cmds, err, ran.Load())
	}
	if err := iso.Kill(context.Background()); err != nil {
		t.Fatalf("repeat Kill: %v", err)
	}
}

func TestKillParkedTasks(t *testing.T) {
	var afterBoundary atomic.Int32
	entry := registerKillTestEntry(func(task *Task, _ []byte) ([]byte, error) {
		task.Go(func(child *Task) {
			_, _ = child.Call(7, []byte("park"))
			afterBoundary.Add(1)
		})
		_ = task.Inbox()
		afterBoundary.Add(1)
		return nil, nil
	})
	iso := newTestIsolate(t, Config{Entry: entry})
	state, cmds := resume(t, iso)
	if state != Quiescent || len(cmds) != 1 {
		t.Fatalf("state=%v commands=%v", state, cmds)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := iso.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	state, cmds, err := iso.Resume(context.Background(), []Event{{ID: cmds[0].ID}, {Payload: []byte("wake")}})
	if err != nil || state != Killed || len(cmds) != 0 || afterBoundary.Load() != 0 {
		t.Fatalf("state=%v commands=%v err=%v after=%d", state, cmds, err, afterBoundary.Load())
	}
}

func TestKillPendingActiveTask(t *testing.T) {
	started := make(chan struct{})
	var release atomic.Bool
	var afterBoundary atomic.Bool
	entry := registerKillTestEntry(func(task *Task, _ []byte) ([]byte, error) {
		close(started)
		for !release.Load() {
		}
		task.Yield()
		afterBoundary.Store(true)
		return nil, nil
	})
	iso := newTestIsolate(t, Config{Entry: entry})
	resumeDone := make(chan State, 1)
	go func() {
		state, _, _ := iso.Resume(context.Background(), nil)
		resumeDone <- state
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("entry did not start")
	}
	defer release.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := iso.Kill(ctx)
	var pending *KillPendingError
	if !errors.As(err, &pending) || pending.GoroutineID != 1 {
		t.Fatalf("Kill err=%v, want pending task 1", err)
	}
	select {
	case state := <-resumeDone:
		if state != Killed {
			t.Fatalf("Resume state=%v", state)
		}
	case <-time.After(time.Second):
		t.Fatal("Resume did not return after kill")
	}
	state, _, err := iso.Resume(context.Background(), nil)
	if err != nil || state != Killed {
		t.Fatalf("revoked Resume state=%v err=%v", state, err)
	}
	release.Store(true)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := iso.Kill(waitCtx); err != nil {
		t.Fatalf("Kill after task reached boundary: %v", err)
	}
	if afterBoundary.Load() {
		t.Fatal("task executed after revoked Task boundary")
	}
}
