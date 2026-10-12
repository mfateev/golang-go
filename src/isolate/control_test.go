// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"internal/isolatebridge"
	"math/rand/v2"
	"runtime"
	"testing"
)

func TestControlCallPreservesWorkflowRandom(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 2, 8} {
		runtime.GOMAXPROCS(procs)
		b := deterministicBoundary(t)
		seed := [32]byte{19}
		if err := b.ConfigureRandom(seed); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := range 3 {
				command := <-b.Commands()
				if command.Op != 73 || len(command.Payload) != 1 || command.Payload[0] != byte(i) {
					t.Error("wrong control payload")
				}
				command.Reply(command.Payload, nil)
			}
		}()
		want := rand.NewChaCha8(seed)
		b.Run(func() {
			before := rand.Uint64()
			isolatebridge.RunInbound(func() {
				for i := range 3 {
					payload, err := b.ControlCall(73, []byte{byte(i)})
					if err != nil || len(payload) != 1 || payload[0] != byte(i) {
						t.Error("control reply changed")
					}
				}
			})
			after := rand.Uint64()
			if before != want.Uint64() || after != want.Uint64() {
				t.Error("control transport consumed workflow randomness")
			}
		})
		<-done
	}
}

func TestControlCallRejectsWorkflowGoroutine(t *testing.T) {
	b := deterministicBoundary(t)
	b.Run(func() {
		defer func() {
			if recover() == nil {
				t.Error("ordinary workflow used ControlCall")
			}
		}()
		_, _ = b.ControlCall(73, nil)
	})
}
