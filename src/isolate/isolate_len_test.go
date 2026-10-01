// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build isolate_len

package isolate_test

import (
	"internal/isolatebridge"
	"testing"
)

// Run with -gcflags=all=-d=isolatepackages=unused/isolateprobe so the
// compiler routes len(map) through runtime.isolateMapLen.
func TestMapLengthStaysWithOwner(t *testing.T) {
	process := map[string]int{"x": 1}
	var nilMap map[string]int
	owner := isolatebridge.New()
	var owned map[string]int
	owner.Run(func() {
		owned = map[string]int{"x": 1}
		if len(owned) != 1 || len(process) != 1 || len(nilMap) != 0 {
			t.Error("owner or process map length is incorrect")
		}
	})
	wantReject := func() {
		defer func() {
			if got := recover(); got != "isolate: map read crosses owner boundary" {
				t.Errorf("foreign map length panic = %v", got)
			}
		}()
		_ = len(owned)
	}
	wantReject()
	other := isolatebridge.New()
	other.Run(wantReject)
	owner.Run(func() {
		if len(owned) != 1 {
			t.Error("owner map length changed")
		}
	})
}
