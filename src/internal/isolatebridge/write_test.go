// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"bytes"
	"testing"
	"time"
	"unsafe"
)

func TestWriteReadOnly(t *testing.T) {
	b := New()
	if err := b.EnableDeterminism(); err != nil {
		t.Fatal(err)
	}
	var workflowBytes []byte
	b.Run(func() { workflowBytes = bytes.Repeat([]byte{3}, 128) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.Run(func() {
			_, _ = b.ReadOnlyCall(1, nil)
			if !InReadOnly() {
				panic("missing scratch admission")
			}
			b.Write(10, workflowBytes) // Borrow existing workflow data.
			scratch := bytes.Repeat([]byte{5}, 128)
			b.Write(11, scratch)
			clear(scratch)
		})
	}()
	command := <-b.Commands()
	if err := b.Suspend(); err != nil {
		t.Fatal(err)
	}
	command.Reply(nil, nil)
	if err := b.ResumeReadOnly(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("read-only Write waited for a host acknowledgment")
	}
	for _, expected := range []struct {
		op    uint32
		value byte
	}{{10, 3}, {11, 5}} {
		message := <-b.Writes()
		if message.Op != expected.op || !bytes.Equal(message.Payload, bytes.Repeat([]byte{expected.value}, 128)) {
			t.Fatalf("read-only message=%+v", message)
		}
		if owner, ok := allocationOwner(unsafe.Pointer(&message.Payload[0])); !ok || owner != 0 {
			t.Fatalf("read-only payload owner=(%d,%v), want process", owner, ok)
		}
	}
}
