// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_stdlib

package e4base64caller

import "encoding/base64"

// ReadStd is a second importing package for the build-wide caller probe.
//
//go:noinline
func ReadStd() *base64.Encoding { return base64.StdEncoding }

//go:noinline
func SetStd(encoding *base64.Encoding) { base64.StdEncoding = encoding }
