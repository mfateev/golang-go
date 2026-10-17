// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"internal/abi"
	"unsafe"
)

// HostContext describes an active call into a registered host operation. It is
// valid only on the calling goroutine until that operation returns. Its zero
// value is invalid. Use Reenter to invoke application callbacks.
type HostContext struct{ context unsafe.Pointer }

// Allocator selects an allocation domain, without granting application execution
// or access to preexisting private values. It may outlive its originating call;
// construction fails with ErrRevoked after its domain closes. Holding an
// allocator keeps ownership metadata alive, but cannot revive an instance.
// Its zero value is invalid.
type Allocator struct{ allocator unsafe.Pointer }

// NewHostFunc registers fn in host setup and returns an ordinary typed callable
// handle. The handle requires an isolate caller. fn runs with host globals and
// allocations and may borrow the caller's arguments until it returns. Shared
// caches must not retain private references. Results and errors are ordinary
// Go values: construct private results with Allocate or deliberately share host
// handles. Sharing a handle grants no ordinary access to its host object.
//
// A panic escaping fn produces copied PanicError diagnostics and revokes the
// instance, after host cleanup has run. The panic object is never transferred.
//
//go:noinline
func NewHostFunc[In, Out any](fn func(HostContext, In) (Out, error)) func(In) (Out, error) {
	checkHostRegistration(fn)
	if fn == nil {
		panic("isolate: nil host function")
	}
	return func(input In) (Out, error) {
		context := enterHost()
		defer leaveHost(context)
		defer func() {
			if value := recover(); value != nil {
				hostPanic(context, value)
			}
		}()
		return fn(HostContext{context}, input)
	}
}

// Allocator selects the original caller's domain, including read-only scratch.
func (c HostContext) Allocator() Allocator { return Allocator{hostContextAllocator(c.context)} }

// Allocator returns an initialized instance's main allocation domain. It is a
// host operation and remains usable before Start and while execution is fenced.
func (i *Isolate) Allocator() Allocator {
	if i == nil {
		panic("isolate: nil instance")
	}
	return Allocator{i.boundary.AllocationDomain()}
}

// HostAllocator selects host allocation, including for nested shared lazy
// initialization inside a private constructor. It is a host operation.
func HostAllocator() Allocator { return Allocator{hostAllocator()} }

// Allocate invokes build and its synchronous helpers with a's allocation owner.
// Existing objects keep their owner. Constructors run with host package globals;
// an allocator alone cannot borrow preexisting private objects. New goroutines
// inherit neither this allocation scope nor its borrowed access.
//
// Scopes nest and restore on return or panic. Revocation prevents a new scope
// from calling build, and suppresses delivery of an in-progress result. Active
// construction holds a lifetime lease and obeys the target's resource limits.
//
//go:noinline
func Allocate[T any](a Allocator, build func() T) (result T, err error) {
	scope := beginConstruction(a.allocator)
	if scope == nil {
		return result, ErrRevoked
	}
	defer func() {
		if !endConstruction(scope) {
			var zero T
			result, err = zero, ErrRevoked
			// Resource rejection unwinds the constructor before this boundary.
			recover()
		}
	}()
	checkConstructor(build)
	// Force results to escape across the dynamically selected allocation domain.
	return abi.Escape(build()), nil
}

// Reenter invokes fn synchronously in the original caller's execution context,
// restoring its globals, allocation owner, effects and read-only role. It never
// inherits a constructor's allocation override. The callback remains subject to
// caller ownership checks. Host state is restored on panic.
func (c HostContext) Reenter(fn func()) { hostReenter(c.context, fn) }

// Waiter connects one external host completion to its original isolate
// goroutine. It must not be copied after first use. Its zero value is invalid.
type Waiter struct{ waiter unsafe.Pointer }

// NewWaiter creates a control handle for the current isolate goroutine, or the
// original caller of a host operation. A host worker must release every borrow
// before Wake, even after revocation. Kill can report pending work until then.
func NewWaiter() *Waiter {
	return (*Waiter)(newHostWaiter())
}

// Park commits the one-shot wait, releasing deterministic dispatch. Completion
// may precede Park. Wake does not bypass suspension or role admission. Only the
// associated goroutine may Park, and only once.
func (w *Waiter) Park() { parkHostWaiter(unsafe.Pointer(w)) }

// Wake publishes completion and releases asynchronous borrowing. It is safe
// from any host goroutine and idempotent. Writes before Wake happen before the
// associated goroutine returns from Park. Late wakeups never revive an isolate.
func (w *Waiter) Wake() { wakeHostWaiter(unsafe.Pointer(w)) }

//go:linkname checkHostRegistration runtime.isolateCheckHostRegistration
func checkHostRegistration(any)

//go:linkname enterHost runtime.isolateEnterHost
func enterHost() unsafe.Pointer

//go:linkname leaveHost runtime.isolateLeaveHost
func leaveHost(unsafe.Pointer)

//go:linkname hostPanic runtime.isolateHostPanic
func hostPanic(unsafe.Pointer, any)

//go:linkname hostContextAllocator runtime.isolateHostContextAllocator
func hostContextAllocator(unsafe.Pointer) unsafe.Pointer

//go:linkname hostAllocator runtime.isolateHostAllocator
func hostAllocator() unsafe.Pointer

//go:linkname beginConstruction runtime.isolateBeginConstruction
func beginConstruction(unsafe.Pointer) unsafe.Pointer

//go:linkname endConstruction runtime.isolateEndConstruction
func endConstruction(unsafe.Pointer) bool

//go:linkname hostReenter runtime.isolateHostReenter
func hostReenter(unsafe.Pointer, func())

//go:linkname newHostWaiter runtime.isolateNewHostWaiter
func newHostWaiter() unsafe.Pointer

//go:linkname parkHostWaiter runtime.isolateParkHostWaiter
func parkHostWaiter(unsafe.Pointer)

//go:linkname wakeHostWaiter runtime.isolateWakeHostWaiter
func wakeHostWaiter(unsafe.Pointer)

//go:linkname checkConstructor runtime.isolateCheckConstructor
func checkConstructor(any)
