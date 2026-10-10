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
	"cmd/go/internal/isolatecfg"
	"cmd/go/internal/load"
	"cmd/go/internal/modload"
	"cmd/internal/isolatepolicy"
	"cmd/internal/objabi"
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

// These standard packages replay initialization and route mutable globals
// to each instance. Other standard packages are still instrumented: their
// unlisted process state is denied to private execution.
// The JSON v2 initializer writes callback globals in internal and jsonopts;
// its readers need the same per-instance routing. Reflect is selected with
// JSON because its type caches can retain values created by an isolate. Time
// has lazy mutable zone state, but its clock and timer effects remain shared.
var isolateOwnedStandardPackages = map[string]bool{
	"encoding/binary":                  true,
	"math":                             true,
	"math/big":                         true,
	"math/rand":                        true,
	"math/rand/v2":                     true,
	"crypto/rand":                      true,
	"regexp":                           true,
	"regexp/syntax":                    true,
	"unicode":                          true,
	"unicode/utf8":                     true,
	"unicode/utf16":                    true,
	"io":                               true,
	"fmt":                              true,
	"log":                              true,
	"log/internal":                     true,
	"log/slog":                         true,
	"log/slog/internal":                true,
	"log/slog/internal/buffer":         true,
	"strconv":                          true,
	"bytes":                            true,
	"strings":                          true,
	"errors":                           true,
	"internal/reflectlite":             true,
	"context":                          true,
	"encoding/base32":                  true,
	"encoding/base64":                  true,
	"encoding/json":                    true,
	"encoding/json/internal":           true,
	"encoding/json/internal/jsonwire":  true,
	"encoding/json/internal/jsonflags": true,
	"encoding/json/internal/jsonopts":  true,
	"encoding/json/jsontext":           true,
	"encoding/json/v2":                 true,
	"reflect":                          true,
	"time":                             true,
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
	// Effect checks are compulsory throughout the linked graph, including
	// host-initialized dependencies. Host execution returns through each guard.
	forcedGcflags = append(forcedGcflags, "-d=isolateeffects=1")
	var effectEntries []string
	for _, fn := range functions {
		effectEntries = append(effectEntries, fn.fullName())
	}
	if len(functions) == 0 {
		for _, p := range loaded {
			effectEntries = append(effectEntries, p.ImportPath+".main")
		}
	}
	forcedGcflags = append(forcedGcflags, "-d=isolateeffectentries="+strings.Join(effectEntries, ":"))
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
	// Converter dependencies include host registries, gRPC configuration and
	// environment reads during initialization. Keep their startup on the host;
	// their process state remains denied by compulsory memory checks. Only the
	// exact reviewed metadata methods may enter a process service. The converter
	// package itself replays its error values and default objects per instance.
	processConverter := make(map[string]bool)
	// Activities and SDK-compatible option/error/interceptor types may live beside marked
	// workflow functions. Their SDK imports reach host logging and worker
	// services that must initialize only in the
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
			case "go.opentelemetry.io/otel/trace", "go.temporal.io/sdk/activity", "go.temporal.io/sdk/temporal", "go.temporal.io/sdk/workflow", "go.temporal.io/sdk/client", "go.temporal.io/sdk/interceptor", "go.temporal.io/sdk/interceptor/tracing":
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
			// UUID's reader and optional byte pool must be private when workflows
			// use it too. Ordinary ownership/effect checks still apply to its code.
			if (processConverter[p.ImportPath] || processActivity[p.ImportPath]) && p.ImportPath != "go.temporal.io/sdk/converter" && p.ImportPath != "github.com/google/uuid" && !isolateOwnedObservabilityPackage(p.ImportPath) {
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
	// Compiler service scopes and their dynamic calls are valid only for the
	// reviewed module versions. Replacements and nested modules cannot inherit
	// a trusted namespace without a source audit.
	metadataServices := false
	// Host-only implementations can be reached through process registries too.
	// Validate every compiled source that could receive callback provenance.
	for _, p := range load.PackageList(append([]*load.Package{host}, loaded...)) {
		for _, trusted := range []struct{ path, version string }{
			{isolatepolicy.ProtobufModule, isolatepolicy.ProtobufVersion},
			{isolatepolicy.TemporalAPIModule, isolatepolicy.TemporalAPIVersion},
			{isolatepolicy.TemporalSDKModule, isolatepolicy.TemporalSDKVersion},
			{isolatepolicy.OTelTraceModule, isolatepolicy.OTelVersion},
		} {
			if p.ImportPath != trusted.path && !strings.HasPrefix(p.ImportPath, trusted.path+"/") {
				continue
			}
			// SDK contrib integrations are separate modules and have no entries
			// in the compiler service manifest. Their ordinary instrumented
			// code never inherits the parent SDK's metadata privileges.
			if trusted.path == isolatepolicy.TemporalSDKModule && strings.HasPrefix(p.ImportPath, trusted.path+"/contrib/") {
				continue
			}
			validSource := p.Module != nil && p.Module.Path == trusted.path && p.Module.Version == trusted.version && p.Module.Replace == nil
			if trusted.path == isolatepolicy.TemporalSDKModule && p.Module != nil && p.Module.Path == trusted.path {
				var replacementPath, replacementVersion string
				if p.Module.Replace != nil {
					replacementPath, replacementVersion = p.Module.Replace.Path, p.Module.Replace.Version
				}
				validSource = isolatepolicy.TemporalSDKSource(p.Module.Version, replacementPath, replacementVersion)
			}
			if !validSource || cfg.BuildMod == "vendor" {
				var forkHint string
				if trusted.path == isolatepolicy.TemporalSDKModule {
					forkHint = "; the audited replacement " + isolatepolicy.TemporalSDKForkModule + "@" + isolatepolicy.TemporalSDKForkVersion + " is also supported"
				}
				base.Fatalf("isolate: metadata services require %s@%s without a replacement, nested module, or vendored source%s; audit the service manifest before upgrading", trusted.path, trusted.version, forkHint)
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
	fmt.Fprintf(&source, "//go:linkname isolateHostMain %s.main\n", objabi.PathToPrefix(host.ImportPath))
	source.WriteString("func isolateHostMain()\n")
	if len(functions) == 0 {
		for i, p := range loaded {
			fmt.Fprintf(&source, "//go:linkname isolateProgramMain%d %s.main\n", i, objabi.PathToPrefix(p.ImportPath))
			fmt.Fprintf(&source, "func isolateProgramMain%d()\n", i)
		}
	} else {
		for i, fn := range functions {
			fmt.Fprintf(&source, "//go:linkname isolateFunctionValue%d %s.%s\nfunc isolateFunctionValue%d() any\n", i, objabi.PathToPrefix(fn.Package.ImportPath), fn.valueName(), i)
			fmt.Fprintf(&source, "//go:linkname isolateFunctionInvoke%d %s.%s\nfunc isolateFunctionInvoke%d(decode, encode func(...isolatebridge.Value) error) error\n", i, objabi.PathToPrefix(fn.Package.ImportPath), fn.invokeName(), i)
		}
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
			if isolateOwnedStandardPackages[path] || path == "github.com/google/uuid" || isolateOwnedObservabilityPackage(path) {
				fmt.Fprintf(&source, "unsafe.Pointer(&isolatePackageDescriptor%d),\n", descriptorIndex[path])
			}
		}
		source.WriteString("}\n}\n")
		fmt.Fprintf(&source, "func isolateProgramState%d() (func(func()), error) {\npackages, libraries := isolateProgramDescriptors%d()\nstate, err := isolateproto.NewPackageInstance(packages, libraries)\nif err != nil { return nil, err }; return state.Run, nil\n}\n", i, i)
	}
	source.WriteString("func init() {\n")
	if len(functions) == 0 {
		for i, p := range programs {
			fmt.Fprintf(&source, "isolatebridge.RegisterProgram(%s, isolatebridge.ProgramEntry{MetadataVersion:%d, Main: isolateProgramMain%d, NewState: isolateProgramState%d})\n", strconv.Quote(p.Name), isolateabi.MetadataVersion, i, i)
		}
	} else {
		for i, fn := range functions {
			root := slices.Index(loaded, fn.Package)
			fmt.Fprintf(&source, "isolatebridge.RegisterFunction(isolatebridge.FunctionEntry{MetadataVersion:%d, Name:%q, Function:isolateFunctionValue%d(), Invoke:isolateFunctionInvoke%d, NewState:isolateProgramState%d, StateDescriptors:isolateProgramDescriptors%d})\n", isolateabi.MetadataVersion, fn.fullName(), i, i, root, root)
		}
	}
	source.WriteString("}\nfunc main() { isolateHostMain() }\n")

	buildInfo := host.Internal.BuildInfo
	var entryGcflags []string
	entryAliases := []string{host.ImportPath + ".main"}
	if len(functions) == 0 {
		for _, p := range loaded {
			entryAliases = append(entryAliases, p.ImportPath+".main")
		}
	} else {
		for _, fn := range functions {
			entryAliases = append(entryAliases, fn.Package.ImportPath+"."+fn.valueName(), fn.Package.ImportPath+"."+fn.invokeName())
		}
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
		report := isolateBuildReport{FormatVersion: 1, MetadataVersion: isolateabi.MetadataVersion, APIVersion: isolateabi.APIVersion, DeterminismVersion: isolateabi.DeterminismVersion}
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

// OTel API attribute encoders and header parsing caches are private, including
// scratch copies for read-only handlers. The trace API initializes an automatic
// tracer from process environment; keep that startup on the host. Native sink
// providers use only its value/context API, never the automatic/global provider.
func isolateOwnedObservabilityPackage(path string) bool {
	switch path {
	// The active read-only interceptor chain must be scratch-owned even when
	// a query closure passes a context captured from the writable workflow.
	case "github.com/mfateev/sdk-go-poc/internal/interceptorscope":
		return true
	// OpenTracing's context key, error values, optional global tracer and ext
	// tag descriptors are ordinary private package state. Replay their startup
	// in query/validator scratch layouts too; grant no process-state access.
	case "github.com/opentracing/opentracing-go", "github.com/opentracing/opentracing-go/log", "github.com/opentracing/opentracing-go/ext":
		return true
	case "go.opentelemetry.io/otel/attribute", "go.opentelemetry.io/otel/attribute/internal", "go.opentelemetry.io/otel/baggage", "go.opentelemetry.io/otel/internal/baggage", "go.opentelemetry.io/otel/propagation":
		return true
	}
	return false
}
