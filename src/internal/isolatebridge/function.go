// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

import (
	"internal/isolateabi"
	"reflect"
	"strconv"
	"sync"
	"unsafe"
)

// Value describes a typed slot in a generated invoker. Pointer points to the
// slot; Value preserves its concrete type, including typed nil pointers.
// The slots and their contents are created in the executing isolate.
type Value struct {
	Value   any
	Pointer any
}

// FunctionEntry is immutable code metadata registered by the generated host.
// Invoke calls the original function directly, without reflect.Call. Decode
// and encode run in the same isolate and must not retain another owner's data.
type FunctionEntry struct {
	MetadataVersion uint32
	Name            string
	Function        any
	Invoke          func(decode, encode func(...Value) error) error
	NewState        func() (func(func()), error)
	// StateDescriptors returns a fresh slice of immutable compiler descriptors.
	// Support functions can join a program without sharing package globals.
	StateDescriptors func() (packages, readOnlyLibraries []unsafe.Pointer)
}

var functions = struct {
	sync.RWMutex
	byPC map[uintptr]FunctionEntry
}{byPC: make(map[uintptr]FunctionEntry)}

// RegisterFunction is called by the generated host before the user's main.
func RegisterFunction(entry FunctionEntry) {
	if entry.MetadataVersion != isolateabi.MetadataVersion {
		panic("isolate: incompatible function metadata version " + strconv.FormatUint(uint64(entry.MetadataVersion), 10) + ", runtime requires " + strconv.Itoa(isolateabi.MetadataVersion) + "; rebuild with a compatible toolchain")
	}
	v := reflect.ValueOf(entry.Function)
	if entry.Name == "" || v.Kind() != reflect.Func || v.IsNil() || entry.Invoke == nil || entry.NewState == nil {
		panic("isolate: incomplete function entry")
	}
	pc := v.Pointer()
	functions.Lock()
	defer functions.Unlock()
	if _, exists := functions.byPC[pc]; exists {
		panic("isolate: duplicate function " + entry.Name)
	}
	functions.byPC[pc] = entry
}

// LookupFunction uses code identity, so converting a function to an interface
// or assigning it to another variable preserves its isolate marker.
func LookupFunction(fn any) (FunctionEntry, bool) {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return FunctionEntry{}, false
	}
	functions.RLock()
	defer functions.RUnlock()
	entry, ok := functions.byPC[v.Pointer()]
	return entry, ok
}
