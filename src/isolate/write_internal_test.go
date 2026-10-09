// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"bytes"
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestWriteWithoutHostReceipt(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		i, err := New(Config{Deterministic: deterministic, Program: lifecycleProgram(func() {
			payload := bytes.Repeat([]byte{7}, 64<<10)
			Write(19, payload)
			clear(payload)
			Write(20, []byte("last"))
			Write(21, nil)
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		// Do not receive a write until the whole instance has terminated. An
		// unbuffered send or disguised Call would leave it parked forever.
		if err := lifecycleWait(t, i); err != nil {
			t.Fatal(err)
		}
		if i.PendingCalls() != 0 || i.DroppedWrites() != 0 {
			t.Fatalf("writes became pending calls or drops: %d, %d", i.PendingCalls(), i.DroppedWrites())
		}
		first := <-i.Writes()
		if first.Op != 19 || !bytes.Equal(first.Payload, bytes.Repeat([]byte{7}, 64<<10)) {
			t.Fatal("write lost its operation or copied payload")
		}
		for _, pointer := range []unsafe.Pointer{unsafe.Pointer(first), unsafe.Pointer(&first.Payload[0])} {
			if owner, tracked := ownershipAllocationOrigin(pointer); !tracked || owner != 0 {
				t.Fatalf("write retained private allocation: owner=%d, tracked=%v", owner, tracked)
			}
		}
		second, third := <-i.Writes(), <-i.Writes()
		if second.Op != 20 || string(second.Payload) != "last" || third.Op != 21 || third.Payload != nil {
			t.Fatalf("unexpected final writes: %+v, %+v", second, third)
		}
		select {
		case <-i.Commands():
			t.Fatal("Write produced a Call command")
		default:
		}
	}
}

func TestWriteQueueBounded(t *testing.T) {
	const writes = 10000
	i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
		Write(1, make([]byte, (64<<10)+1)) // Oversized, even with room available.
		for op := uint32(0); op < writes; op++ {
			Write(op, []byte("event"))
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, i); err != nil {
		t.Fatal(err)
	}
	if got := i.DroppedWrites(); got != writes-64+1 {
		t.Fatalf("dropped writes=%d, want %d", got, writes-64+1)
	}
	for op := uint32(0); op < 64; op++ {
		message := <-i.Writes()
		if message.Op != op || string(message.Payload) != "event" {
			t.Fatalf("queued write=%+v, want op %d", message, op)
		}
	}
	select {
	case <-i.Writes():
		t.Fatal("write queue exceeded its bound")
	default:
	}
}

func TestWriteDoesNotChangeDeterministicDispatch(t *testing.T) {
	var want []byte
	for _, mode := range []string{"none", "undrained", "drained"} {
		mode := mode
		i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
			_, _ = Call(1, nil)
			var trace []byte
			var traceMu sync.Mutex
			var wg sync.WaitGroup
			for child := byte(0); child < 8; child++ {
				wg.Go(func() {
					a, b := make(chan byte, 1), make(chan byte, 1)
					for iteration := 0; iteration < 100; iteration++ {
						a <- child
						b <- child + 8
						var selected byte
						select {
						case value := <-a:
							selected = value
							<-b
						case value := <-b:
							selected = value
							<-a
						}
						traceMu.Lock()
						trace = append(trace, selected)
						traceMu.Unlock()
						if mode != "none" {
							Write(10, []byte{selected})
						}
						runtime.Gosched()
					}
				})
			}
			wg.Wait()
			_, _ = Call(2, trace)
		})})
		if err != nil {
			t.Fatal(err)
		}
		drained := make(chan struct{})
		if mode == "drained" {
			go func() {
				defer close(drained)
				for {
					select {
					case <-i.Writes():
					case <-i.Done():
						return
					}
				}
			}()
		} else {
			close(drained)
		}
		if err := i.Start(); err != nil {
			t.Fatal(err)
		}
		first := resourceCommand(t, i)
		if first.ID != 1 || first.Op != 1 {
			t.Fatal("unexpected starting command")
		}
		first.Reply(nil, nil)
		last := resourceCommand(t, i)
		if last.ID != 2 || last.Op != 2 || len(last.Payload) != 800 {
			t.Fatalf("writes altered Call IDs or dispatcher progress: id=%d op=%d trace=%d", last.ID, last.Op, len(last.Payload))
		}
		if mode == "none" {
			want = bytes.Clone(last.Payload)
		} else if !bytes.Equal(last.Payload, want) {
			t.Fatalf("%s writes changed select results or goroutine order", mode)
		}
		last.Reply(nil, nil)
		if err := lifecycleWait(t, i); err != nil {
			t.Fatal(err)
		}
		<-drained
	}
}

func TestWriteConcurrentProducers(t *testing.T) {
	i, err := New(Config{Program: lifecycleProgram(func() {
		var wg sync.WaitGroup
		for op := uint32(1); op <= 8; op++ {
			wg.Go(func() {
				payload := []byte{byte(op)}
				for range 1000 {
					Write(op, payload)
				}
				clear(payload)
			})
		}
		wg.Wait()
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, i); err != nil {
		t.Fatal(err)
	}
	if got := i.DroppedWrites(); got != 8000-64 {
		t.Fatalf("concurrent dropped writes=%d, want %d", got, 8000-64)
	}
	for range 64 {
		message := <-i.Writes()
		if message.Op < 1 || message.Op > 8 || len(message.Payload) != 1 || message.Payload[0] != byte(message.Op) {
			t.Fatalf("concurrent writer payload corrupted: %+v", message)
		}
	}
}

func TestWriteRevocation(t *testing.T) {
	i, err := New(Config{Deterministic: true, Program: lifecycleProgram(func() {
		for {
			Write(1, []byte("event"))
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-i.Writes():
	case <-time.After(5 * time.Second):
		t.Fatal("write loop did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lifecycleWait(t, i); err != ErrRevoked {
		t.Fatalf("write loop terminal outcome=%v", err)
	}
}

func TestWriteOutsideIsolate(t *testing.T) {
	defer func() {
		if got := recover(); got != "isolate: Call outside an active isolate" {
			t.Fatalf("Write outside isolate panic=%v", got)
		}
	}()
	Write(1, nil)
}
