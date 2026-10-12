// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import _ "unsafe"

// IsGeneratedProtobuf reports whether value's concrete message type was
// registered by the audited generated Google protobuf process builder.
// Values, pointers and decoding pointer slots are accepted. This checks type
// provenance only; it grants no access to the value's storage or descriptors.
func IsGeneratedProtobuf(value any) bool { return isGeneratedProtobuf(value) }

//go:linkname isGeneratedProtobuf runtime.isolateIsGeneratedProtobuf
func isGeneratedProtobuf(value any) bool
