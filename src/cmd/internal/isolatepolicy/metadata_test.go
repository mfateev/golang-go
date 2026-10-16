// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatepolicy

import "testing"

func TestTemporalSDKSource(t *testing.T) {
	for _, test := range []struct {
		path, replacement string
		want              bool
	}{
		{"", "", true},
		{"../temporal-go-sdk-main", "", true},
		{"./sdk", "", true},
		{".", "", true},
		{"/tmp/sdk-go-main", "", true},
		{"../temporal-go-sdk-main", "v1.50.0", false},
		{"github.com/mfateev/temporal-go-sdk", "v1.49.0-isolates.1", false},
		{"example.org/temporal-go-sdk", "", false},
		{"", "v1.50.0", false},
	} {
		if got := TemporalSDKSource(test.path, test.replacement); got != test.want {
			t.Errorf("TemporalSDKSource(%q, %q) = %v, want %v", test.path, test.replacement, got, test.want)
		}
	}
}

func TestProtobufMetadataBoundary(t *testing.T) {
	for _, test := range []struct {
		pkg, fn       string
		scope, reject bool
	}{
		{ProtobufModule + "/internal/filedesc", "(*Field).Default", true, false},
		{ProtobufModule + "/internal/filedesc", "(*Fields).ByJSONName", true, false},
		{ProtobufModule + "/internal/filedesc", "(*Message).ExtensionRangeOptions", false, true},
		{ProtobufModule + "/internal/filedesc", "(*Field).Options", false, true},
		{ProtobufModule + "/reflect/protoregistry", "(*Types).RegisterMessage", false, true},
		{ProtobufModule + "/proto", "Marshal", false, false},
		{ProtobufModule + "/encoding/protojson", "MarshalOptions.marshal", false, false},
	} {
		if got := MetadataScope(test.pkg, test.fn); got != test.scope {
			t.Errorf("metadata scope %s.%s = %v", test.pkg, test.fn, got)
		}
		if got := RejectedMetadata(test.pkg, test.fn); got != test.reject {
			t.Errorf("metadata rejection %s.%s = %v", test.pkg, test.fn, got)
		}
	}
}
