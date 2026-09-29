// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"isolate"
	"strings"
	"testing"
)

func TestHostCallsFailClosed(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func()
	}{
		{"Call", func() { _, _ = isolate.Call(1, []byte("input")) }},
		{"Inbox", func() { _ = isolate.Inbox() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				got := recover()
				if got == nil || !strings.Contains(got.(string), "outside an active isolate") {
					t.Errorf("panic = %v, want outside-an-isolate failure", got)
				}
			}()
			tt.call()
		})
	}
}
