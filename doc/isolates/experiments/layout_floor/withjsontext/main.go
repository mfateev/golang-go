// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package withjsontext

import "encoding/json/jsontext"

var value jsontext.Value

//temporal:workflow
func Entry() { _ = value }
