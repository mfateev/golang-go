// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package withreflect

import "reflect"

var typ = reflect.TypeOf(0)

//temporal:workflow
func Entry() { _ = typ }
