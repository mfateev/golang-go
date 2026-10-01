// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package isolate

import (
	"context"
	"internal/isolatebridge"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestKillWakesNetpollWait(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	entered := make(chan struct{})
	var resumed atomic.Bool
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			close(entered)
			var buf [1]byte
			_, _ = reader.Read(buf[:])
			resumed.Store(true)
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.RunningGoroutines() != 0 || i.boundary.LiveGoroutines() != 1 {
		t.Fatal("main did not park in netpoll")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill on netpoll wait = %v", err)
	}
	select {
	case <-i.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("netpoll waiter did not exit after revocation")
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked netpoll = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() {
		t.Fatal("main resumed after revoked netpoll wait")
	}
	if _, err := writer.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if n, err := reader.Read(buf[:]); n != 1 || err != nil || buf[0] != 'x' {
		t.Fatalf("host read after revoked waiter = %d, %v, %q", n, err, buf[:n])
	}
}

func TestRevokedReadyPollRead(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err := writer.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}

	b := isolatebridge.New()
	entered := make(chan struct{})
	exited := make(chan struct{})
	var release, resumed atomic.Bool
	go func() {
		defer close(exited)
		b.Run(func() {
			close(entered)
			for !release.Load() {
			}
			var buf [1]byte
			_, _ = reader.Read(buf[:])
			resumed.Store(true)
		})
	}()
	<-entered
	b.Stop()
	release.Store(true)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("ready poll read did not exit after revocation")
	}
	if resumed.Load() || b.LiveGoroutines() != 0 {
		t.Fatalf("revoked read returned=%t, live=%d", resumed.Load(), b.LiveGoroutines())
	}
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var buf [1]byte
	if n, err := reader.Read(buf[:]); n != 1 || err != nil || buf[0] != 'x' {
		t.Fatalf("host read after revoked ready read = %d, %v, %q", n, err, buf[:n])
	}
}

func TestKillWakesMultipleNetpollWaiters(t *testing.T) {
	var readers, writers [2]*os.File
	for index := range readers {
		var err error
		readers[index], writers[index], err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer readers[index].Close()
		defer writers[index].Close()
	}
	entered := make(chan struct{}, 2)
	var resumed atomic.Bool
	read := func(reader *os.File) {
		entered <- struct{}{}
		var buf [1]byte
		_, _ = reader.Read(buf[:])
		resumed.Store(true)
	}
	program := Program{entry: isolatebridge.ProgramEntry{
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			go read(readers[1])
			read(readers[0])
		},
	}}
	i, err := New(Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	<-entered
	<-entered
	deadline := time.Now().Add(time.Second)
	for i.boundary.RunningGoroutines() != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if i.boundary.LiveGoroutines() != 2 || i.boundary.RunningGoroutines() != 0 {
		t.Fatal("both poll readers did not park")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatalf("Kill with two poll waits = %v", err)
	}
	if err := i.Wait(); err != errMainRevoked {
		t.Fatalf("Wait after revoked poll waits = %v, want %v", err, errMainRevoked)
	}
	if resumed.Load() || i.boundary.LiveGoroutines() != 0 {
		t.Fatalf("read returned=%t, live=%d", resumed.Load(), i.boundary.LiveGoroutines())
	}
}
