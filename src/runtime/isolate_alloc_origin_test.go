// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime_test

import (
	"runtime"
	"testing"
)

func TestIsolateLargeAllocOrigin(t *testing.T) {
	for _, owner := range []uintptr{0, 1, 2, 0} {
		b, got := runtime.IsolateLargeAllocOriginForTest(owner)
		if got != owner {
			t.Errorf("large allocation origin = %d, want %d", got, owner)
		}
		runtime.KeepAlive(b)
	}
}
