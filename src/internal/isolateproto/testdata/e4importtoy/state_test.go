// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency

package e4importtoy_test

import (
	"internal/isolateproto/testdata/e4importtoy"
	"sync"
	"testing"
	"unsafe"
)

//go:linkname depType internal/isolateproto/testdata/e4deptoy.isolateLayoutType
var depType unsafe.Pointer

//go:linkname depKey internal/isolateproto/testdata/e4deptoy.isolateLayoutKey
var depKey byte

//go:linkname importType internal/isolateproto/testdata/e4importtoy.isolateLayoutType
var importType unsafe.Pointer

//go:linkname importKey internal/isolateproto/testdata/e4importtoy.isolateLayoutKey
var importKey byte

//go:linkname newPackageBases runtime.isolateE4NewPackageBases
func newPackageBases([]unsafe.Pointer, []unsafe.Pointer) unsafe.Pointer

//go:linkname setPackageBases runtime.isolateE4SetPackageBases
func setPackageBases(unsafe.Pointer) unsafe.Pointer

//go:linkname packageBase runtime.isolateE4PackageBase
func packageBase(unsafe.Pointer, unsafe.Pointer) unsafe.Pointer

//go:linkname rerunDepVars internal/isolateproto/testdata/e4deptoy.init
func rerunDepVars()

//go:linkname rerunDepInit internal/isolateproto/testdata/e4deptoy.init.0
func rerunDepInit()

//go:linkname rerunImportVars internal/isolateproto/testdata/e4importtoy.init
func rerunImportVars()

//go:linkname rerunImportInit internal/isolateproto/testdata/e4importtoy.init.0
func rerunImportInit()

func withPackageBases(table unsafe.Pointer, fn func()) {
	old := setPackageBases(table)
	defer setPackageBases(old)
	fn()
}

func newInstance(t *testing.T) unsafe.Pointer {
	t.Helper()
	if depType == nil || importType == nil {
		t.Fatal("compiler did not emit both package layout types")
	}
	dep := unsafe.Pointer(&depKey)
	imp := unsafe.Pointer(&importKey)
	if dep == imp {
		t.Fatal("package identity symbols are shared")
	}
	table := newPackageBases([]unsafe.Pointer{dep, imp}, []unsafe.Pointer{depType, importType})
	if packageBase(table, dep) == packageBase(table, imp) {
		t.Fatal("dependent packages share global storage")
	}
	withPackageBases(table, func() {
		rerunDepVars()
		rerunDepInit()
		rerunImportVars()
		rerunImportInit()
	})
	return table
}

func checkRun(t *testing.T, want int) {
	t.Helper()
	observed, count, read, depCount := e4importtoy.Run()
	if observed != 41 || count != want || read != want || depCount != want {
		t.Errorf("Run=(%d,%d,%d,%d), want (41,%d,%d,%d)", observed, count, read, depCount, want, want, want)
	}
}

func TestPackageDependencyInitialization(t *testing.T) {
	a, b := newInstance(t), newInstance(t)
	withPackageBases(a, func() { checkRun(t, 42) })
	withPackageBases(a, func() { checkRun(t, 43) })
	withPackageBases(b, func() { checkRun(t, 42) })
	withPackageBases(b, func() { checkRun(t, 43) })
}

func TestPackageBasesInheritedByChild(t *testing.T) {
	table := newInstance(t)
	withPackageBases(table, func() {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			checkRun(t, 42)
		}()
		wg.Wait()
		checkRun(t, 43)
	})
}

func TestMissingPackageStateFailsClosed(t *testing.T) {
	table := newPackageBases([]unsafe.Pointer{unsafe.Pointer(&depKey)}, []unsafe.Pointer{depType})
	withPackageBases(table, func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("importing package used process state without a package layout")
			}
		}()
		e4importtoy.Run()
	})
}
