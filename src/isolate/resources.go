// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"internal/isolatebridge"
	"runtime"
	"strconv"
	"strings"
)

// ResourceLimits are host limits installed before initialization. Zero means
// unlimited. MaxGoroutines includes attached initializer and entry goroutines.
// MaxMemoryBytes charges allocation slots (until swept), stacks and attributable
// runtime metadata. Unused span capacity is reported separately, not charged.
// This budget is not a per-isolate RSS cap.
type ResourceLimits struct {
	MaxMemoryBytes uint64
	MaxGoroutines  uint32
}

// ResourceStats is a host-only usage snapshot, including cached isolates.
type ResourceStats = isolatebridge.ResourceStats

// ResourceLimitError is a permanently revoked isolate's first limit violation.
// Usage describes the attempted charge, which may exceed the accepted snapshot.
// Application recovery cannot turn this error into a workflow result.
type ResourceLimitError struct {
	Resource     string
	Limit, Usage uint64
	Stats        ResourceStats
	Stack        string
}

func (e *ResourceLimitError) Error() string {
	return "isolate: " + e.Resource + " limit exceeded: usage=" + strconv.FormatUint(e.Usage, 10) + ", limit=" + strconv.FormatUint(e.Limit, 10)
}

// Resources reads usage from the host without stopping the world. Individual
// counters are independently sampled while the isolate runs. After suspension
// goroutine/stack counters are stable; GC may still refund heap/span charges.
// Retaining a completed handle keeps diagnostics, not the private heap, alive.
func (i *Isolate) Resources() ResourceStats { return i.boundary.Resources() }

// FailTaskDuration and FailProgress are host watchdog notifications. Both use
// the same immutable fault and permanent fence as native admission limits;
// callers still use Kill to wait for cleanup or obtain pending diagnostics.
func (i *Isolate) FailTaskDuration(limit, usage uint64) error {
	i.boundary.FailResource(3, limit, usage)
	return i.executionError(nil)
}

func (i *Isolate) FailProgress(limit, usage uint64) error {
	i.boundary.FailResource(4, limit, usage)
	return i.executionError(nil)
}

func (i *Isolate) resourceFault(stack string) error {
	kind, limit, usage := i.boundary.ResourceFaultDetails()
	name := "unknown resource"
	switch kind {
	case 1:
		name = "memory"
	case 2:
		name = "goroutines"
	case 3:
		name = "task duration"
	case 4:
		name = "no progress"
	}
	if stack == "" {
		pcs, count := i.boundary.ResourceFaultPCs()
		if count != 0 {
			var text strings.Builder
			frames := runtime.CallersFrames(pcs[:count])
			for {
				frame, more := frames.Next()
				text.WriteString(frame.Function + "\n\t" + frame.File + ":" + strconv.Itoa(frame.Line) + "\n")
				if !more {
					break
				}
			}
			stack = text.String()
		} else {
			_, _, stack = i.boundary.Snapshot()
		}
	}
	return &ResourceLimitError{Resource: name, Limit: limit, Usage: usage, Stats: i.Resources(), Stack: stack}
}
