// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build phase0_e4 && phase2b_layout && phase2b_dependency

package e4importtoy_test

import (
	"internal/isolateproto"
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

//go:linkname rerunDepVars internal/isolateproto/testdata/e4deptoy.init
func rerunDepVars()

//go:linkname rerunDepInit internal/isolateproto/testdata/e4deptoy.init.0
func rerunDepInit()

//go:linkname rerunImportVars internal/isolateproto/testdata/e4importtoy.init
func rerunImportVars()

//go:linkname rerunImportInit internal/isolateproto/testdata/e4importtoy.init.0
func rerunImportInit()

func newInstance(t *testing.T) *isolateproto.PackageInstance {
	t.Helper()
	if depType == nil || importType == nil {
		t.Fatal("compiler did not emit both package layout types")
	}
	dep := unsafe.Pointer(&depKey)
	imp := unsafe.Pointer(&importKey)
	if dep == imp {
		t.Fatal("package identity symbols are shared")
	}
	instance, err := isolateproto.NewPackageInstance([]isolateproto.PackageInitSpec{
		// Deliberately list the importer first. The host must still run its
		// selected dependency's initializers before the importer's.
		{
			Path:         "internal/isolateproto/testdata/e4importtoy",
			Key:          imp,
			Type:         importType,
			Dependencies: []string{"internal/isolateproto/testdata/e4deptoy"},
			Initializers: []func(){rerunImportVars, rerunImportInit},
		},
		{
			Path:         "internal/isolateproto/testdata/e4deptoy",
			Key:          dep,
			Type:         depType,
			Initializers: []func(){rerunDepVars, rerunDepInit},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return instance
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
	a.Run(func() { checkRun(t, 42) })
	a.Run(func() { checkRun(t, 43) })
	b.Run(func() { checkRun(t, 42) })
	b.Run(func() { checkRun(t, 43) })
}

func TestPackageBasesInheritedByChild(t *testing.T) {
	table := newInstance(t)
	table.Run(func() {
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
	table, err := isolateproto.NewPackageInstance([]isolateproto.PackageInitSpec{{
		Path:         "internal/isolateproto/testdata/e4deptoy",
		Key:          unsafe.Pointer(&depKey),
		Type:         depType,
		Initializers: []func(){rerunDepVars, rerunDepInit},
	}})
	if err != nil {
		t.Fatal(err)
	}
	table.Run(func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("importing package used process state without a package layout")
			}
		}()
		e4importtoy.Run()
	})
}

func TestPackageInitManifestValidation(t *testing.T) {
	dep := isolateproto.PackageInitSpec{
		Path: "dep", Key: unsafe.Pointer(&depKey), Type: depType,
		Dependencies: []string{"missing"},
	}
	if _, err := isolateproto.NewPackageInstance([]isolateproto.PackageInitSpec{dep}); err == nil {
		t.Error("accepted an absent selected dependency")
	}
	dep.Dependencies = []string{"dep"}
	if _, err := isolateproto.NewPackageInstance([]isolateproto.PackageInitSpec{dep}); err == nil {
		t.Error("accepted a package initialization cycle")
	}
}
