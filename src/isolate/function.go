// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"errors"
	"internal/isolatebridge"
	"reflect"
)

// Value is a typed argument or result slot supplied by a generated invoker.
// An SDK decodes into Pointer and encodes Value. Both belong to the isolate;
// they must remain behind its copied-byte communication boundary.
type Value = isolatebridge.Value

// Handle identifies a function marked //go:isolate in a dependency of the
// host. A normal go build discovers the marker and creates its metadata.
// The function retains its original Go signature.
type Handle struct{ entry isolatebridge.FunctionEntry }

// LookupFunction returns a handle only for a compiler-discovered marked
// function. It is a host operation and does not create an isolate.
func LookupFunction(fn any) (Handle, bool) {
	entry, ok := isolatebridge.LookupFunction(fn)
	return Handle{entry: entry}, ok
}

// Name returns the fully qualified package path and function name.
func (h Handle) Name() string { return h.entry.Name }

// Signature returns the original function's Go type for host-side validation.
func (h Handle) Signature() reflect.Type { return reflect.TypeOf(h.entry.Function) }

// Program supplies an SDK entry dispatcher while retaining the compiler's
// package state factory. The dispatcher decodes bytes, invokes the function,
// and reports completion using the SDK's own communication protocol.
func (h Handle) Program(dispatch func()) Program {
	return Program{name: h.Name(), entry: isolatebridge.ProgramEntry{Main: dispatch, NewState: h.entry.NewState}}
}

// ProgramWithHandle supplies a dispatcher with a copy of the compiler-created
// function metadata. The entry wrapper performs this trusted metadata transfer
// before calling application code. Dispatchers can use a noncapturing function
// instead of retaining a host-owned closure containing the handle. Other state
// captured by dispatch still requires the ordinary ownership checks.
// Keep the trusted entry wrapper in this package when callers are instrumented.
//
//go:noinline
func (h Handle) ProgramWithHandle(dispatch func(Handle)) Program {
	if dispatch == nil {
		return h.Program(nil)
	}
	return h.Program(func() { dispatch(h) })
}

// Invoke calls the marked function directly in the current isolate. Decode
// receives argument slots; encode receives zero or one result slot. A function
// error is returned instead of calling encode. SDK callbacks perform all
// conversion inside the isolate and may not retain the slots in the host.
func (h Handle) Invoke(decode, encode func(...Value) error) error {
	if h.entry.Invoke == nil || decode == nil || encode == nil {
		return errors.New("isolate: incomplete function invocation")
	}
	_ = isolatebridge.Current() // Reject invocation without an isolate boundary.
	return h.entry.Invoke(decode, encode)
}
