// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_stdlib

package e4base64toy

import "encoding/base64"

// These accesses are compiled in an importing package, rather than base64.
//
//go:noinline
func Snapshot() (std, url, rawStd, rawURL *base64.Encoding) {
	return base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding
}

//go:noinline
func SetStd(encoding *base64.Encoding) { base64.StdEncoding = encoding }

//go:noinline
func EncodeStd(src []byte) string { return base64.StdEncoding.EncodeToString(src) }

//go:noinline
func EncodeRawStd(src []byte) string { return base64.RawStdEncoding.EncodeToString(src) }
