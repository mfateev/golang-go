// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/isolatecfg"
	"cmd/go/internal/load"
	"cmd/go/internal/modload"
)

type isolateDirsFlag []string

func (f *isolateDirsFlag) String() string { return strings.Join(*f, ",") }

func (f *isolateDirsFlag) Set(dir string) error {
	if dir == "" {
		return fmt.Errorf("empty isolate program directory")
	}
	*f = append(*f, dir)
	return nil
}

var buildIsolateDirs isolateDirsFlag

// runBuildIsolates is the first cmd/go integration slice for static isolate
// programs. It links configured package main directories under their own
// import paths with one host package main. The generated top-level main
// registers every program before invoking the host main.
func runBuildIsolates(ctx context.Context, args []string) {
	if len(args) != 1 {
		base.Fatalf("go build -isolate-dir requires exactly one host package")
	}
	if cfg.BuildBuildmode != "default" || cfg.BuildContext.Compiler != "gc" {
		base.Fatalf("go build -isolate-dir currently requires the default gc build mode")
	}
	if cfg.BuildN || cfg.BuildCover {
		base.Fatalf("go build -isolate-dir does not yet support -n or -cover")
	}

	programs, err := isolatecfg.LoadDirectories(buildIsolateDirs)
	if err != nil {
		base.Fatal(err)
	}
	if !slices.Contains(cfg.BuildContext.BuildTags, "phase0_e4") {
		cfg.BuildContext.BuildTags = append(cfg.BuildContext.BuildTags, "phase0_e4")
	}
	ld := modload.NewLoader()
	ld.InitWorkfile()
	BuildInit(ld)
	b := NewBuilder("", ld.VendorDirOrEmpty)
	defer func() {
		if err := b.Close(); err != nil {
			base.Fatal(err)
		}
	}()

	hosts := load.PackagesAndErrors(ld, ctx, load.PackageOpts{AutoVCS: true}, args)
	load.CheckPackageErrors(hosts)
	if len(hosts) != 1 || hosts[0].Name != "main" {
		base.Fatalf("go build -isolate-dir requires one package main host")
	}
	host := hosts[0]
	if host.ImportPath == "command-line-arguments" {
		base.Fatalf("go build -isolate-dir requires a host package directory, not a list of Go files")
	}

	paths := make([]string, len(programs))
	for i, p := range programs {
		paths[i] = p.Dir
	}
	loaded := load.PackagesAndErrors(ld, ctx, load.PackageOpts{AutoVCS: true}, paths)
	load.CheckPackageErrors(loaded)
	if len(loaded) != len(programs) {
		base.Fatalf("go build -isolate-dir: package loader returned %d packages for %d directories", len(loaded), len(programs))
	}
	seenPath := map[string]bool{host.ImportPath: true}
	for i, p := range loaded {
		if p.Name != "main" || p.ImportPath == "command-line-arguments" {
			base.Fatalf("isolate %q in %q must be a package main with a unique import path", programs[i].Name, programs[i].Dir)
		}
		abs, err := filepath.Abs(p.Dir)
		if err != nil || abs != programs[i].Dir {
			base.Fatalf("isolate %q loaded from %q, want %q", programs[i].Name, p.Dir, programs[i].Dir)
		}
		if seenPath[p.ImportPath] {
			base.Fatalf("duplicate host or isolate import path %q", p.ImportPath)
		}
		seenPath[p.ImportPath] = true
	}
	// In this trusted POC, application packages reached by a program own
	// instance state. Standard packages remain process-owned pending their
	// separate state and effect audit.
	programPaths := make([][]string, len(loaded))
	selected := make(map[string]bool)
	for i, root := range loaded {
		for _, p := range load.PackageList([]*load.Package{root}) {
			if p.Standard {
				continue
			}
			if p.ImportPath == "" {
				base.Fatalf("isolate %q has a dependency without an import path", programs[i].Name)
			}
			programPaths[i] = append(programPaths[i], p.ImportPath)
			selected[p.ImportPath] = true
		}
	}
	selectedPaths := make([]string, 0, len(selected))
	for path := range selected {
		selectedPaths = append(selectedPaths, path)
	}
	slices.Sort(selectedPaths)
	forcedGcflags = append(forcedGcflags, "-d=isolatepackages="+strings.Join(selectedPaths, ":"))

	implicit := load.PackagesAndErrors(ld, ctx, load.PackageOpts{}, []string{"unsafe", "runtime", "internal/isolatebridge", "internal/isolateproto"})
	load.CheckPackageErrors(implicit)
	imports := append([]*load.Package{host}, loaded...)
	imports = append(imports, implicit...)
	importPaths := make([]string, len(imports))
	for i, p := range imports {
		importPaths[i] = p.ImportPath
	}

	var source strings.Builder
	source.WriteString("package main\nimport (\n")
	for _, path := range importPaths {
		switch path {
		case "internal/isolatebridge":
			fmt.Fprintf(&source, "isolatebridge %s\n", strconv.Quote(path))
		case "internal/isolateproto":
			fmt.Fprintf(&source, "isolateproto %s\n", strconv.Quote(path))
		case "unsafe":
			fmt.Fprintf(&source, "unsafe %s\n", strconv.Quote(path))
		default:
			fmt.Fprintf(&source, "_ %s\n", strconv.Quote(path))
		}
	}
	source.WriteString(")\n")
	fmt.Fprintf(&source, "//go:linkname isolateHostMain %s.main\n", host.ImportPath)
	source.WriteString("func isolateHostMain()\n")
	for i, p := range loaded {
		fmt.Fprintf(&source, "//go:linkname isolateProgramMain%d %s.main\n", i, p.ImportPath)
		fmt.Fprintf(&source, "func isolateProgramMain%d()\n", i)
	}
	descriptorIndex := make(map[string]int, len(selectedPaths))
	for i, path := range selectedPaths {
		descriptorIndex[path] = i
		fmt.Fprintf(&source, "//go:linkname isolatePackageDescriptor%d %s.isolatePackageDescriptor\n", i, path)
		fmt.Fprintf(&source, "var isolatePackageDescriptor%d byte\n", i)
	}
	for i, paths := range programPaths {
		fmt.Fprintf(&source, "func isolateProgramState%d() (func(func()), error) {\n", i)
		source.WriteString("state, err := isolateproto.NewPackageInstance([]unsafe.Pointer{\n")
		for _, path := range paths {
			fmt.Fprintf(&source, "unsafe.Pointer(&isolatePackageDescriptor%d),\n", descriptorIndex[path])
		}
		source.WriteString("})\n")
		source.WriteString("if err != nil { return nil, err }; return state.Run, nil\n}\n")
	}
	source.WriteString("func init() {\n")
	for i, p := range programs {
		fmt.Fprintf(&source, "isolatebridge.RegisterProgram(%s, isolatebridge.ProgramEntry{Main: isolateProgramMain%d, NewState: isolateProgramState%d})\n", strconv.Quote(p.Name), i, i)
	}
	source.WriteString("}\nfunc main() { isolateHostMain() }\n")

	buildInfo := host.Internal.BuildInfo
	host.Internal.ForceLibrary = true
	host.Internal.BuildInfo = nil
	for _, p := range loaded {
		p.Internal.ForceLibrary = true
		p.Internal.BuildInfo = nil
	}
	pmain := &load.Package{
		PackagePublic: load.PackagePublic{
			Name:       "main",
			ImportPath: host.ImportPath + ".isolate",
			Root:       host.Root,
			GoFiles:    []string{"_isolate_main.go"},
			Imports:    importPaths,
			Module:     host.Module,
		},
		Internal: load.PackageInternal{
			Build:      &build.Package{Name: "main"},
			BuildInfo:  buildInfo,
			Imports:    imports,
			RawImports: importPaths,
		},
	}
	pmain.DefaultGODEBUG = host.DefaultGODEBUG
	objdir := b.CompileAction(ModeBuild, ModeBuild, pmain).Objdir
	if err := b.BackgroundShell().Mkdir(objdir); err != nil {
		base.Fatal(err)
	}
	pmain.Dir = objdir
	if err := os.WriteFile(filepath.Join(objdir, "_isolate_main.go"), []byte(source.String()), 0666); err != nil {
		base.Fatal(err)
	}
	if cfg.BuildO == "" {
		cfg.BuildO = host.DefaultExecName() + cfg.ExeSuffix
	}
	if fi, err := os.Stat(cfg.BuildO); err == nil && fi.IsDir() {
		base.Fatalf("go build -isolate-dir requires a file output, not directory %q", cfg.BuildO)
	}
	pmain.Target = cfg.BuildO
	pmain.Stale = true
	pmain.StaleReason = "static isolate program build"
	a := b.AutoAction(ld, ModeInstall, ModeBuild, pmain)
	b.Do(ctx, a)
}
