// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase2_isolation

package isolateproto

import (
	"context"
	"fmt"
	"testing"
)

// This intentionally failing conformance test is enabled only when the
// Phase 2 initialized-global isolation mechanism is ready to be evaluated.
// It covers a mutable map, pointer, and closure created during package init,
// including an entry adapter that captures an initialized pointer.
var phase2Global = func() struct {
	count  *int
	values map[string]int
	read   func() int
} {
	count := new(int)
	return struct {
		count  *int
		values map[string]int
		read   func() int
	}{count: count, values: map[string]int{"n": 0}, read: func() int { return *count }}
}()

var phase2Entry = func() Entry {
	capturedCount := phase2Global.count
	return Register("isolateproto.phase2.globals", func(_ *Task, _ []byte) ([]byte, error) {
		*capturedCount++
		phase2Global.values["n"]++
		return []byte(fmt.Sprintf("%d/%d/%d", *capturedCount, phase2Global.values["n"], phase2Global.read())), nil
	})
}()

func TestInitializedGlobalsAreIsolated(t *testing.T) {
	for instance := range 2 {
		iso, err := New(Config{Entry: phase2Entry})
		if err != nil {
			t.Fatal(err)
		}
		state, _, err := iso.Resume(context.Background(), nil)
		if err != nil || state != Completed {
			t.Fatalf("instance %d: state=%v err=%v", instance, state, err)
		}
		got, err := iso.Result()
		if err != nil || string(got) != "1/1/1" {
			t.Fatalf("instance %d: initialized graph leaked: %q, %v", instance, got, err)
		}
	}
}
