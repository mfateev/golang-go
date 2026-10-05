// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"
	"encoding/json"
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
var buildIsolateReport string

type isolateBuildReport struct {
	FormatVersion int                    `json:"format_version"`
	Programs      []isolateReportProgram `json:"programs"`
	Packages      []isolateReportPackage `json:"packages"`
}

type isolateReportProgram struct {
	Name     string   `json:"name"`
	Packages []string `json:"packages"`
}

type isolateReportPackage struct {
	Path           string `json:"path"`
	Standard       bool   `json:"standard"`
	InstanceState  bool   `json:"instance_state"`
	HostReachable  bool   `json:"host_reachable"`
	Classification string `json:"classification"`
}

// These standard packages participate in the per-instance state probe. This
// is not a complete ownership or effect audit of their dependency graph.
// The JSON v2 initializer writes callback globals in internal and jsonopts;
// its readers need the same per-instance routing. Reflect is selected with
// JSON because its type caches can retain values created by an isolate. Time
// has lazy mutable zone state, but its clock and timer effects remain shared.
var isolateOwnedStandardPackages = map[string]bool{
	"encoding/base32":                 true,
	"encoding/base64":                 true,
	"encoding/json":                   true,
	"encoding/json/internal":          true,
	"encoding/json/internal/jsonopts": true,
	"encoding/json/jsontext":          true,
	"encoding/json/v2":                true,
	"reflect":                         true,
	"time":                            true,
}

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
	buildStaticIsolates(ctx, ld, b, host, programs, loaded, nil)
}

// buildStaticIsolates shares state selection and entry generation between
// legacy directory programs and compiler-discovered function entries.
func buildStaticIsolates(ctx context.Context, ld *modload.Loader, b *Builder, host *load.Package, programs []isolatecfg.Program, loaded []*load.Package, functions []isolateFunction) {
	implicit := load.PackagesAndErrors(ld, ctx, load.PackageOpts{}, []string{"unsafe", "runtime", "internal/isolatebridge", "internal/isolateproto"})
	load.CheckPackageErrors(implicit)
	if len(functions) != 0 {
		for _, p := range loaded {
			source, err := isolateInvokerSource(p, functions)
			if err != nil {
				base.Fatal(err)
			}
			p.Internal.IsolateSource = source
			for _, dep := range implicit {
				if dep.ImportPath != "unsafe" && dep.ImportPath != "internal/isolatebridge" {
					continue
				}
				if slices.Contains(p.Imports, dep.ImportPath) {
					continue
				}
				p.Imports = append(p.Imports, dep.ImportPath)
				p.Internal.RawImports = append(p.Internal.RawImports, dep.ImportPath)
				p.Internal.Imports = append(p.Internal.Imports, dep)
			}
		}
	}
	// The Temporal dispatcher is entered from a host-created factory, so a
	// workflow need not import it directly (a clock-only function is one such
	// example). Include its decoding/encoding state in each function instance.
	// This is the same trusted POC integration as the converter classification
	// below; a general SDK support-package declaration is future work.
	var support []*load.Package
	if len(functions) != 0 {
		for _, p := range load.PackageList([]*load.Package{host}) {
			if p.ImportPath == "github.com/mfateev/sdk-go-poc/workflow" {
				support = append(support, p)
			}
		}
	}
	// Application packages reached by a program own instance state. Only
	// audited standard packages join that selection in this trusted POC.
	programPaths := make([][]string, len(loaded))
	programReachable := make([][]string, len(loaded))
	allReachable := make(map[string]*load.Package)
	selected := make(map[string]bool)
	processStd := make(map[string]*load.Package)
	// The pinned Temporal default converter is a process service in the trusted
	// POC. Its dependency graph includes protobuf registries, embedded data,
	// gRPC configuration, and executable/environment reads during initialization.
	// Keep that graph out of per-instance initializer replay. The workflow SDK
	// package and application packages remain selected. This is a provisional
	// classification, not a complete state/effect audit of these dependencies.
	processConverter := make(map[string]bool)
	// Activities may live beside marked workflow functions. Their SDK imports
	// reach host logging and worker services that must initialize only in the
	// process. Keep this pinned SDK graph process-owned too; activity functions
	// still run exclusively on the host in this trusted POC. Application and
	// workflow-support packages are not part of that SDK dependency graph.
	processActivity := make(map[string]bool)
	for _, root := range loaded {
		for _, p := range load.PackageList(append([]*load.Package{root}, support...)) {
			var processGraph map[string]bool
			switch p.ImportPath {
			case "go.temporal.io/sdk/converter":
				processGraph = processConverter
			case "go.temporal.io/sdk/activity":
				processGraph = processActivity
			default:
				continue
			}
			for _, dep := range load.PackageList([]*load.Package{p}) {
				if !dep.Standard {
					processGraph[dep.ImportPath] = true
				}
			}
		}
	}
	for i, root := range loaded {
		for _, p := range load.PackageList(append([]*load.Package{root}, support...)) {
			if p.ImportPath == "" {
				base.Fatalf("isolate %q has a dependency without an import path", programs[i].Name)
			}
			programReachable[i] = append(programReachable[i], p.ImportPath)
			allReachable[p.ImportPath] = p
			if processConverter[p.ImportPath] || processActivity[p.ImportPath] {
				continue
			}
			if p.Standard {
				processStd[p.ImportPath] = p
				if !isolateOwnedStandardPackages[p.ImportPath] {
					continue
				}
			}
			programPaths[i] = append(programPaths[i], p.ImportPath)
			selected[p.ImportPath] = true
		}
	}
	hostReachable := make(map[string]bool)
	for _, p := range load.PackageList([]*load.Package{host}) {
		hostReachable[p.ImportPath] = true
	}
	selectedPaths := make([]string, 0, len(selected))
	startupSkip := make([]string, 0, len(selected))
	for path := range selected {
		selectedPaths = append(selectedPaths, path)
		if !hostReachable[path] && processStd[path] == nil {
			startupSkip = append(startupSkip, path)
		}
	}
	slices.Sort(selectedPaths)
	slices.Sort(startupSkip)
	forcedGcflags = append(forcedGcflags, "-d=isolatepackages="+strings.Join(selectedPaths, ":"))

	var imports []*load.Package
	seenImports := make(map[string]bool, len(imports))
	for _, p := range append(append([]*load.Package{host}, loaded...), implicit...) {
		if seenImports[p.ImportPath] {
			continue
		}
		imports = append(imports, p)
		seenImports[p.ImportPath] = true
	}
	processPaths := make([]string, 0, len(processStd))
	for path := range processStd {
		processPaths = append(processPaths, path)
	}
	slices.Sort(processPaths)
	for _, path := range processPaths {
		if !seenImports[path] {
			imports = append(imports, processStd[path])
		}
	}
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
	if len(functions) == 0 {
		for i, p := range loaded {
			fmt.Fprintf(&source, "//go:linkname isolateProgramMain%d %s.main\n", i, p.ImportPath)
			fmt.Fprintf(&source, "func isolateProgramMain%d()\n", i)
		}
	} else {
		for i, fn := range functions {
			fmt.Fprintf(&source, "//go:linkname isolateFunctionValue%d %s.%s\nfunc isolateFunctionValue%d() any\n", i, fn.Package.ImportPath, fn.valueName(), i)
			fmt.Fprintf(&source, "//go:linkname isolateFunctionInvoke%d %s.%s\nfunc isolateFunctionInvoke%d(decode, encode func(...isolatebridge.Value) error) error\n", i, fn.Package.ImportPath, fn.invokeName(), i)
		}
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
	if len(functions) == 0 {
		for i, p := range programs {
			fmt.Fprintf(&source, "isolatebridge.RegisterProgram(%s, isolatebridge.ProgramEntry{Main: isolateProgramMain%d, NewState: isolateProgramState%d})\n", strconv.Quote(p.Name), i, i)
		}
	} else {
		for i, fn := range functions {
			root := slices.Index(loaded, fn.Package)
			fmt.Fprintf(&source, "isolatebridge.RegisterFunction(isolatebridge.FunctionEntry{Name:%q, Function:isolateFunctionValue%d(), Invoke:isolateFunctionInvoke%d, NewState:isolateProgramState%d})\n", fn.fullName(), i, i, root)
		}
	}
	source.WriteString("}\nfunc main() { isolateHostMain() }\n")

	buildInfo := host.Internal.BuildInfo
	var entryGcflags []string
	if len(startupSkip) != 0 {
		entryGcflags = append(entryGcflags, "-d=isolateentryskip="+strings.Join(startupSkip, ":"))
	}
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
			Gcflags:    entryGcflags,
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
	var reportPath string
	if buildIsolateReport != "" {
		output, err := filepath.Abs(cfg.BuildO)
		if err != nil {
			base.Fatal(err)
		}
		reportPath, err = filepath.Abs(buildIsolateReport)
		if err != nil {
			base.Fatal(err)
		}
		if reportPath == output {
			base.Fatalf("-isolate-report must differ from executable output %q", cfg.BuildO)
		}
	}
	pmain.Target = cfg.BuildO
	pmain.Stale = true
	pmain.StaleReason = "static isolate program build"
	a := b.AutoAction(ld, ModeInstall, ModeBuild, pmain)
	b.Do(ctx, a)
	if reportPath != "" {
		report := isolateBuildReport{FormatVersion: 1}
		if len(functions) == 0 {
			for i, program := range programs {
				paths := slices.Clone(programReachable[i])
				slices.Sort(paths)
				report.Programs = append(report.Programs, isolateReportProgram{Name: program.Name, Packages: paths})
			}
		} else {
			for _, fn := range functions {
				paths := slices.Clone(programReachable[slices.Index(loaded, fn.Package)])
				slices.Sort(paths)
				report.Programs = append(report.Programs, isolateReportProgram{Name: fn.fullName(), Packages: paths})
			}
		}
		paths := make([]string, 0, len(allReachable))
		for path := range allReachable {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		for _, path := range paths {
			p := allReachable[path]
			classification := "reachable-application"
			if processActivity[path] {
				classification = "process-owned-activity-poc"
			}
			if processConverter[path] {
				classification = "process-owned-converter-poc"
			}
			if p.Standard {
				classification = "unclassified-standard"
				if selected[path] {
					classification = "selected-standard-probe"
				}
			}
			report.Packages = append(report.Packages, isolateReportPackage{
				Path: path, Standard: p.Standard, InstanceState: selected[path],
				HostReachable: hostReachable[path], Classification: classification,
			})
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			base.Fatal(err)
		}
		if err := os.WriteFile(reportPath, append(data, '\n'), 0666); err != nil {
			base.Fatal(err)
		}
	}
}
