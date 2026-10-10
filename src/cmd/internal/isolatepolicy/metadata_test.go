// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatepolicy

import "testing"

func TestTemporalSDKSource(t *testing.T) {
	for _, test := range []struct {
		version, path, replacement string
		want                       bool
	}{
		{TemporalSDKVersion, "", "", true},
		{TemporalSDKVersion, TemporalSDKForkModule, TemporalSDKForkVersion, true},
		{"v1.50.0", TemporalSDKForkModule, TemporalSDKForkVersion, false},
		{TemporalSDKVersion, "../temporal-go-sdk", "", false},
		{TemporalSDKVersion, TemporalSDKForkModule, "", false},
		{TemporalSDKVersion, TemporalSDKForkModule, "v1.49.0-isolates.2", false},
		{TemporalSDKVersion, "example.org/temporal-go-sdk", TemporalSDKForkVersion, false},
		{TemporalSDKVersion, "", TemporalSDKForkVersion, false},
	} {
		if got := TemporalSDKSource(test.version, test.path, test.replacement); got != test.want {
			t.Errorf("TemporalSDKSource(%q, %q, %q) = %v, want %v", test.version, test.path, test.replacement, got, test.want)
		}
	}
}
