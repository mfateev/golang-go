// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"strings"
	"testing"
)

func TestIsolateConfigValidation(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{`{"format_version":2}`, "unsupported format version"},
		{`{"format_version":1,"trust_packages":["example.org/unsafe"]}`, "unknown field"},
		{`{"format_version":1} {}`, "expected one JSON object"},
		{`{"format_version":1,"readonly_packages":["example.org/jobs"]}`, "has no instance state"},
		{`{"format_version":1,"entries":[{"package":"example.org/jobs","function":"Entry","value":"_","invoke":"invoke"}]}`, "invalid identifier"},
		{`{"format_version":1,"entries":[{"package":"example.org/jobs","function":"Entry","value":"value","invoke":"invoke"}]}`, "has no adapter source"},
	} {
		var config isolateConfig
		if err := decodeIsolateConfig([]byte(test.input), &config); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: got %v, want %s", test.input, err, test.want)
		}
	}
}
