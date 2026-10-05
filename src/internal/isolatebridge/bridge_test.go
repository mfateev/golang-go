// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

//go:linkname allocationOwner runtime.isolateAllocOrigin
func allocationOwner(unsafe.Pointer) (uintptr, bool)

func TestBoundaryResponseOwnership(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		name := "concurrent"
		if deterministic {
			name = "deterministic"
		}
		t.Run(name, func(t *testing.T) {
			b := New()
			if deterministic {
				if err := b.EnableDeterminism(); err != nil {
					t.Fatal(err)
				}
			}
			// Dynamic nonempty text ensures errors.New cannot disguise a host
			// backing allocation as an immutable linker literal.
			message := strings.Repeat("host-owned error text ", 32)
			hostError := errors.New(message)
			payload := bytes.Repeat([]byte{7, 19}, 128)
			if owner, ok := allocationOwner(unsafe.Pointer(unsafe.StringData(message))); !ok || owner != 0 {
				t.Fatalf("host message owner=(%d,%v)", owner, ok)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				b.Run(func() {
					got, err := b.Call(1, nil)
					if err == nil || err.Error() != message || !bytes.Equal(got, payload) {
						t.Error("response changed its payload or error text")
						return
					}
					for label, pointer := range map[string]unsafe.Pointer{
						"payload":    unsafe.Pointer(&got[0]),
						"error":      reflect.ValueOf(err).UnsafePointer(),
						"error text": unsafe.Pointer(unsafe.StringData(err.Error())),
					} {
						if owner, ok := allocationOwner(pointer); !ok || owner != b.owner {
							t.Errorf("%s owner=(%d,%v), want %d", label, owner, ok, b.owner)
						}
					}
				})
			}()
			command := <-b.Commands()
			command.Reply(payload, hostError)
			<-done
		})
	}
}
