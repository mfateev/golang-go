// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"internal/isolatebridge"
	"syscall"
	"testing"
)

func TestForkExecRejectsIsolate(t *testing.T) {
	b := isolatebridge.New()
	b.Run(func() {
		defer func() {
			const want = "isolate: forbidden operation syscall.ForkExec"
			if got := recover(); got != want {
				t.Errorf("ForkExec panic = %v, want %q", got, want)
			}
		}()
		_, _ = syscall.ForkExec("/nonexistent", []string{"/nonexistent"}, nil)
	})
}
