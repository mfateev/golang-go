// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e4genericguard

var epoch int

func SetEpoch[T ~int](n T) { epoch = int(n) }

type Counter[T ~int] struct{ value T }

func (c *Counter[T]) Set(n T) { c.value = n }
