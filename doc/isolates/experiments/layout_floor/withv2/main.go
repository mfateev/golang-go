// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package withv2

import jsonv2 "encoding/json/v2"

var options = jsonv2.DefaultOptionsV2()

//temporal:workflow
func Entry() { _ = options }
