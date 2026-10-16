// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

// ProgramEntry is the code and state factory wrapped by the public isolate
// Program handle. Generated function metadata supplies these factories.
type ProgramEntry struct {
	MetadataVersion uint32
	Main            func()
	NewState        func() (func(func()), error)
}
