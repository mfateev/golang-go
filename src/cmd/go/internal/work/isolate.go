// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"internal/isolateabi"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cmd/go/internal/base"
	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
	"cmd/go/internal/modload"
	metadatapolicy "cmd/internal/isolatepolicy"
	"cmd/internal/objabi"
	"internal/isolatepolicy"
)

var buildIsolateReport string

type isolateBuildReport struct {
	FormatVersion      int                    `json:"format_version"`
	MetadataVersion    int                    `json:"metadata_version"`
	APIVersion         int                    `json:"api_version"`
	DeterminismVersion int                    `json:"determinism_version"`
	Programs           []isolateReportProgram `json:"programs"`
	Packages           []isolateReportPackage `json:"packages"`
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

// buildStaticIsolates links compiler-discovered function entries with the
// host and their per-package state factories.
func buildStaticIsolates(ctx context.Context, ld *modload.Loader, b *Builder, host *load.Package, loaded []*load.Package, functions []isolateFunction) {
	prepareStaticIsolates(ctx, ld, b, host, loaded, functions, false)
}

// TestHasIsolates includes declarations and imports from both kinds of test
// package. Call before creating build actions so phase0_e4 can be reloaded.
func TestHasIsolates(ctx context.Context, ld *modload.Loader, opts load.PackageOpts, packages []*load.Package) bool {
	for _, p := range packages {
		if p.Standard {
			continue
		}
		// TestPackagesFor resolves test imports in place. Discovery must not
		// feed expanded vendor paths back into the actual test package load.
		copy := *p
		copy.TestImports = slices.Clone(p.TestImports)
		copy.XTestImports = slices.Clone(p.XTestImports)
		_, ptest, pxtest, perr := load.TestPackagesFor(ld, ctx, opts, &copy, nil)
		if perr != nil {
			continue // The test command reports setup errors per package.
		}
		roots := []*load.Package{ptest}
		if pxtest != nil {
			roots = append(roots, pxtest)
		}
		functions, err := discoverIsolateFunctions(roots)
		if err != nil || len(functions) != 0 {
			return true
		}
	}
	return false
}

var isolateTestPackages, isolateTestEntries []string

// PrepareIsolateTest wraps the generated testing main with isolate registration.
// The caller retains the test command's ordinary linking, caching and execution.
func PrepareIsolateTest(ctx context.Context, ld *modload.Loader, b *Builder, host *load.Package) (*load.Package, error) {
	functions, err := discoverIsolateFunctions(host.Internal.Imports)
	if err != nil || len(functions) == 0 {
		return host, err
	}
	if cfg.BuildCover || cfg.BuildN || cfg.BuildContext.Compiler != "gc" || cfg.BuildBuildmode != "default" {
		return nil, fmt.Errorf("go test with explicit isolate entries requires the default gc build mode without -n or -cover")
	}
	var roots []*load.Package
	for _, fn := range functions {
		if !slices.Contains(roots, fn.Package) {
			roots = append(roots, fn.Package)
		}
	}
	return prepareStaticIsolates(ctx, ld, b, host, roots, functions, true), nil
}

func prepareStaticIsolates(ctx context.Context, ld *modload.Loader, b *Builder, host *load.Package, loaded []*load.Package, functions []isolateFunction, testing bool) *load.Package {
	// Effect checks are compulsory throughout the linked graph, including
	// host-initialized dependencies. Host execution returns through each guard.
	forcedGcflags = append(forcedGcflags, "-d=isolateeffects=1")
	var effectEntries []string
	for _, fn := range functions {
		effectEntries = append(effectEntries, fn.fullName())
	}
	if testing {
		isolateTestEntries = append(isolateTestEntries, effectEntries...)
		slices.Sort(isolateTestEntries)
		isolateTestEntries = slices.Compact(isolateTestEntries)
		effectEntries = isolateTestEntries
	}
	forcedGcflags = append(forcedGcflags, "-d=isolateeffectentries="+strings.Join(effectEntries, ":"))
	manifest, err := readIsolateConfig()
	if err != nil {
		base.Fatal(err)
	}
	var support []*load.Package
	for _, p := range load.PackageList([]*load.Package{host}) {
		if slices.Contains(manifest.SupportPackages, p.ImportPath) {
			support = append(support, p)
		}
	}
	// Adapter imports support invocation, not application state. Select from
	// the original source graph before adding the public isolate API import.
	graphs := make([][]*load.Package, len(loaded))
	for i, root := range loaded {
		graphs[i] = load.PackageList(append([]*load.Package{root}, support...))
	}
	// Runtime support is a set of implicit imports, not a new command's roots.
	// Keep the module loader's complete graph so later test binaries can resolve
	// dependencies that appear only in their test files.
	var implicit []*load.Package
	for _, path := range []string{"unsafe", "runtime", "internal/isolatebridge", "internal/isolateproto", "isolate"} {
		var stack load.ImportStack
		implicit = append(implicit, load.LoadPackageWithFlags(ld, path, host.Dir, &stack, nil, 0))
	}
	load.CheckPackageErrors(implicit)
	for _, p := range loaded {
		source, err := isolateAdapterSource(p)
		if err != nil {
			base.Fatal(err)
		}
		p.Internal.IsolateSource = source
		for _, dep := range implicit {
			if dep.ImportPath != "unsafe" && dep.ImportPath != "isolate" {
				continue
			}
			if slices.Contains(p.Imports, dep.ImportPath) {
				continue
			}
			p.Imports = append(p.Imports, dep.ImportPath)
			p.Internal.RawImports = append(p.Internal.RawImports, dep.ImportPath)
			p.Internal.Imports = append(p.Internal.Imports, dep)
		}
		// A package without tests may already have a compile action when
		// a later test binary discovers its marked entries. Its generated
		// invoker needs the same implicit imports as a newly planned action.
		export := b.BuildExportAction(ModeBuild, ModeBuild, p)
		for _, dep := range p.Internal.Imports {
			if !slices.ContainsFunc(export.Deps, func(a *Action) bool { return a.Package == dep }) {
				export.Deps = append(export.Deps, b.BuildExportAction(ModeBuild, ModeBuild, dep))
			}
		}
		if vet := b.actionCache[cacheKey{"vet", p}]; vet != nil {
			for _, dep := range p.Internal.Imports {
				a := b.vetAction(ld, ModeBuild, ModeBuild, dep)
				if !slices.Contains(vet.Deps, a) {
					vet.Deps = append(vet.Deps, a)
				}
			}
			b.addTransitiveCompileActions(vet, export)
		}
	}
	programPaths := make([][]string, len(loaded))
	programReachable := make([][]string, len(loaded))
	allReachable := make(map[string]*load.Package)
	selected := make(map[string]bool)
	processStd := make(map[string]*load.Package)
	for i := range loaded {
		for _, p := range graphs[i] {
			programReachable[i] = append(programReachable[i], p.ImportPath)
			allReachable[p.ImportPath] = p
			if p.Standard {
				processStd[p.ImportPath] = p
			}
			if slices.Contains(manifest.StatePackages, p.ImportPath) {
				programPaths[i] = append(programPaths[i], p.ImportPath)
				selected[p.ImportPath] = true
			}
		}
	}

	// Transitional guard: the compiler still contains dependency-specific
	// metadata rewrites. Keep their source provenance checks until those
	// rewrites are replaced; an explicit manifest cannot confer trust.
	// Compiler service scopes require the owning dependency module. Protobuf,
	// API and telemetry sources retain exact version checks; the SDK supports
	// official revisions and explicit development checkouts.
	metadataServices := false
	// Host-only implementations can be reached through process registries too.
	// Validate every compiled source that could receive callback provenance.
	for _, p := range load.PackageList(append([]*load.Package{host}, loaded...)) {
		for _, trusted := range []struct{ path, version string }{
			{metadatapolicy.ProtobufModule, metadatapolicy.ProtobufVersion},
			{metadatapolicy.TemporalAPIModule, metadatapolicy.TemporalAPIVersion},
			{metadatapolicy.TemporalSDKModule, ""},
			{metadatapolicy.OTelTraceModule, metadatapolicy.OTelVersion},
		} {
			if p.ImportPath != trusted.path && !strings.HasPrefix(p.ImportPath, trusted.path+"/") {
				continue
			}
			// SDK contrib integrations are separate modules and have no entries
			// in the compiler service manifest. Their ordinary instrumented
			// code never inherits the parent SDK's metadata privileges.
			if trusted.path == metadatapolicy.TemporalSDKModule && strings.HasPrefix(p.ImportPath, trusted.path+"/contrib/") {
				continue
			}
			validSource := p.Module != nil && p.Module.Path == trusted.path && p.Module.Version == trusted.version && p.Module.Replace == nil
			if trusted.path == metadatapolicy.TemporalSDKModule && p.Module != nil && p.Module.Path == trusted.path {
				var replacementPath, replacementVersion string
				if p.Module.Replace != nil {
					replacementPath, replacementVersion = p.Module.Replace.Path, p.Module.Replace.Version
				}
				validSource = metadatapolicy.TemporalSDKSource(replacementPath, replacementVersion)
			}
			if !validSource || cfg.BuildMod == "vendor" {
				if trusted.path == metadatapolicy.TemporalSDKModule {
					base.Fatalf("isolate: SDK metadata services require %s as the owning module, using an official revision or filesystem checkout; replacement modules, nested modules, and vendored sources are unsupported", trusted.path)
				}
				base.Fatalf("isolate: metadata services require %s@%s without a replacement, nested module, or vendored source; audit the service manifest before upgrading", trusted.path, trusted.version)
			}
			metadataServices = true
		}
	}
	if metadataServices {
		forcedGcflags = append(forcedGcflags, "-d=isolatemetadata=1")
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
	compilerPaths := selectedPaths
	if testing {
		// Shared dependencies are built once for a multi-package go test. Keep
		// their descriptors for every test binary planned in this invocation.
		isolateTestPackages = append(isolateTestPackages, selectedPaths...)
		slices.Sort(isolateTestPackages)
		isolateTestPackages = slices.Compact(isolateTestPackages)
		compilerPaths = isolateTestPackages
	}
	forcedGcflags = append(forcedGcflags, "-d=isolatepackages="+strings.Join(compilerPaths, ":"))

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
	fmt.Fprintf(&source, "//go:linkname isolateHostMain %s.main\n", objabi.PathToPrefix(host.ImportPath))
	source.WriteString("func isolateHostMain()\n")
	for i, fn := range functions {
		fmt.Fprintf(&source, "//go:linkname isolateFunctionValue%d %s.%s\nfunc isolateFunctionValue%d() any\n", i, objabi.PathToPrefix(fn.Package.ImportPath), fn.valueName(), i)
		fmt.Fprintf(&source, "//go:linkname isolateFunctionInvoke%d %s.%s\nfunc isolateFunctionInvoke%d(decode, encode func(...isolatebridge.Value) error) error\n", i, objabi.PathToPrefix(fn.Package.ImportPath), fn.invokeName(), i)
	}
	descriptorIndex := make(map[string]int, len(selectedPaths))
	for i, path := range selectedPaths {
		descriptorIndex[path] = i
		fmt.Fprintf(&source, "//go:linkname isolatePackageDescriptor%d %s.isolatePackageDescriptor\n", i, objabi.PathToPrefix(path))
		fmt.Fprintf(&source, "var isolatePackageDescriptor%d byte\n", i)
	}
	for i, paths := range programPaths {
		fmt.Fprintf(&source, "func isolateProgramDescriptors%d() ([]unsafe.Pointer, []unsafe.Pointer) {\n", i)
		source.WriteString("return []unsafe.Pointer{\n")
		for _, path := range paths {
			fmt.Fprintf(&source, "unsafe.Pointer(&isolatePackageDescriptor%d),\n", descriptorIndex[path])
		}
		source.WriteString("}, []unsafe.Pointer{\n")
		for _, path := range paths {
			// UUID caches its Reader and optionally a mutable random-byte pool.
			// Read-only handlers need a fresh layout, just like standard caches.
			if slices.Contains(manifest.ReadOnlyPackages, path) {
				fmt.Fprintf(&source, "unsafe.Pointer(&isolatePackageDescriptor%d),\n", descriptorIndex[path])
			}
		}
		source.WriteString("}\n}\n")
		fmt.Fprintf(&source, "func isolateProgramState%d() (func(func()), error) {\npackages, libraries := isolateProgramDescriptors%d()\nstate, err := isolateproto.NewPackageInstance(packages, libraries)\nif err != nil { return nil, err }; return state.Run, nil\n}\n", i, i)
	}
	source.WriteString("func init() {\n")
	source.WriteString("isolateproto.PrepareReadOnlyLibraries([]unsafe.Pointer{\n")
	for _, path := range selectedPaths {
		if isolatepolicy.FrozenLibraryPackage(path) {
			fmt.Fprintf(&source, "unsafe.Pointer(&isolatePackageDescriptor%d),\n", descriptorIndex[path])
		}
	}
	source.WriteString("})\n")
	for i, fn := range functions {
		root := slices.Index(loaded, fn.Package)
		fmt.Fprintf(&source, "isolatebridge.RegisterFunction(isolatebridge.FunctionEntry{MetadataVersion:%d, Name:%q, Function:isolateFunctionValue%d(), Invoke:isolateFunctionInvoke%d, NewState:isolateProgramState%d, StateDescriptors:isolateProgramDescriptors%d})\n", isolateabi.MetadataVersion, fn.fullName(), i, i, root, root)
	}
	source.WriteString("}\nfunc main() { isolateHostMain() }\n")

	buildInfo := host.Internal.BuildInfo
	var entryGcflags []string
	entryAliases := []string{host.ImportPath + ".main"}
	for _, fn := range functions {
		entryAliases = append(entryAliases, fn.Package.ImportPath+"."+fn.valueName(), fn.Package.ImportPath+"."+fn.invokeName())
	}
	entryGcflags = append(entryGcflags, "-d=isolateentryaliases="+strings.Join(entryAliases, ":"))
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
			Ldflags:    host.Internal.Ldflags,
			OmitDebug:  host.Internal.OmitDebug,
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
	if testing {
		return pmain
	}
	if cfg.BuildO == "" {
		cfg.BuildO = host.DefaultExecName() + cfg.ExeSuffix
	}
	if fi, err := os.Stat(cfg.BuildO); err == nil && fi.IsDir() {
		base.Fatalf("go build with explicit isolate entries requires a file output, not directory %q", cfg.BuildO)
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
		report := isolateBuildReport{FormatVersion: 1, MetadataVersion: isolateabi.MetadataVersion, APIVersion: isolateabi.APIVersion, DeterminismVersion: isolateabi.DeterminismVersion}
		for _, fn := range functions {
			paths := slices.Clone(programReachable[slices.Index(loaded, fn.Package)])
			slices.Sort(paths)
			report.Programs = append(report.Programs, isolateReportProgram{Name: fn.fullName(), Packages: paths})
		}
		paths := make([]string, 0, len(allReachable))
		for path := range allReachable {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		for _, path := range paths {
			p := allReachable[path]
			classification := "process-state-denied"
			if selected[path] {
				classification = "instance-state"
			}
			if p.Standard && isolateTrustedRuntimePackage(path) {
				classification = "trusted-runtime"
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
	return pmain
}

// Match the compiler's GOROOT-only implementation boundary. All other packages
// receive ownership checks, including dependencies with host-only startup.
func isolateTrustedRuntimePackage(path string) bool {
	if path == "runtime" || strings.HasPrefix(path, "internal/runtime/") {
		return true
	}
	switch path {
	case "runtime/cgo", "runtime/race", "runtime/asan", "runtime/msan",
		"internal/abi", "internal/goarch", "internal/goos", "internal/cpu", "internal/bytealg",
		"internal/race", "internal/asan", "internal/msan", "internal/coverage/rtcov",
		"internal/isolatebridge", "internal/isolateproto", "internal/isolatepolicy", "isolate":
		return true
	}
	return false
}
