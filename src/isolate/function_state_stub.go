// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.isolates

package isolate

import (
	"errors"
	"internal/isolatebridge"
)

func newSupportedState(entries ...isolatebridge.FunctionEntry) (func(func()), error) {
	return nil, errors.New("isolate: support functions require a compiler-generated isolate build")
}
