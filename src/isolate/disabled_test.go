// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.isolates

package isolate_test

import (
	"isolate"
	"strings"
	"testing"
)

func TestDisabled(t *testing.T) {
	instance, err := isolate.New(isolate.Config{})
	if instance != nil || err == nil || !strings.Contains(err.Error(), "GOEXPERIMENT=isolates") {
		t.Fatalf("New without experiment = %v, %v", instance, err)
	}
}
