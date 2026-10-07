// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"internal/isolatebridge"
	"runtime"
	"testing"
	"time"
)

func TestIsolateLifecyclePanicAccounting(t *testing.T) {
	for _, mode := range []string{"unrecovered", "nested", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			baseline := runtime.IsolateRunningPanicDefersForTest()
			for range 100 {
				b := isolatebridge.New()
				reported := make(chan struct{})
				b.SetOwnershipFaultHandler(func(string) { close(reported) })
				go func() {
					b.RunOwner(func() {
						go func() {
							if mode == "nested" {
								defer func() { panic("nested panic") }()
							} else if mode == "metadata" {
								defer func() {
									runtime.IsolateMetadataScopeForTest(func() { go func() {}() })
								}()
							}
							panic("original panic")
						}()
						select {}
					})
				}()
				select {
				case <-reported:
				case <-time.After(5 * time.Second):
					t.Fatal("isolate panic was not contained")
				}
				b.WaitDrained()
				if got := runtime.IsolateRunningPanicDefersForTest(); got != baseline {
					t.Fatalf("discard leaked global panic accounting: got %d, baseline %d", got, baseline)
				}
			}
		})
	}
}
