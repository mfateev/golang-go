// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
)

var buildIsolateConfig string

// isolateConfig is a build input, not a grant of runtime privilege. An external
// driver supplies typed adapters and state selection. Only the generated main
// can access internal registration and compiler-created package descriptors.
// Source contents and effective compiler flags participate in action hashing;
// the location of this file deliberately does not.
type isolateConfig struct {
	FormatVersion    int                  `json:"format_version"`
	Entries          []isolateConfigEntry `json:"entries"`
	Sources          map[string]string    `json:"sources"`
	StatePackages    []string             `json:"state_packages"`
	ReadOnlyPackages []string             `json:"readonly_packages"`
	SupportPackages  []string             `json:"support_packages"`
}

type isolateConfigEntry struct {
	Package  string `json:"package"`
	ForTest  string `json:"for_test,omitempty"`
	Function string `json:"function"`
	Value    string `json:"value"`
	Invoke   string `json:"invoke"`
}

var isolateConfigOnce sync.Once
var isolateManifest isolateConfig
var isolateConfigError error

func readIsolateConfig() (*isolateConfig, error) {
	isolateConfigOnce.Do(func() {
		if buildIsolateConfig == "" {
			return
		}
		if !cfg.Experiment.Isolates {
			isolateConfigError = fmt.Errorf("isolate config requires GOEXPERIMENT=isolates")
			return
		}
		data, err := os.ReadFile(buildIsolateConfig)
		if err != nil {
			isolateConfigError = err
			return
		}
		isolateConfigError = decodeIsolateConfig(data, &isolateManifest)
	})
	return &isolateManifest, isolateConfigError
}

func decodeIsolateConfig(data []byte, config *isolateConfig) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(config); err != nil {
		return fmt.Errorf("isolate config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("isolate config: expected one JSON object")
	}
	if config.FormatVersion != 1 {
		return fmt.Errorf("isolate config: unsupported format version %d", config.FormatVersion)
	}
	seen := make(map[string]bool)
	for _, entry := range config.Entries {
		key := entry.packageKey() + "." + entry.Function
		if entry.Package == "" || seen[key] || entry.Function == "main" || entry.Function == "init" {
			return fmt.Errorf("isolate config: invalid or duplicate entry %q", key)
		}
		for _, name := range []string{entry.Function, entry.Value, entry.Invoke} {
			if !token.IsIdentifier(name) || name == "_" {
				return fmt.Errorf("isolate config: invalid identifier %q in %s", name, key)
			}
		}
		if config.Sources[entry.packageKey()] == "" {
			return fmt.Errorf("isolate config: entry %s has no adapter source", key)
		}
		seen[key] = true
	}
	for _, path := range config.ReadOnlyPackages {
		if !slices.Contains(config.StatePackages, path) {
			return fmt.Errorf("isolate config: scratch package %q has no instance state", path)
		}
	}
	return nil
}

type isolateFunction struct {
	Package *load.Package
	Name    string
	Value   string
	Invoke  string
}

func (f isolateFunction) fullName() string   { return f.Package.ImportPath + "." + f.Name }
func (f isolateFunction) valueName() string  { return f.Value }
func (f isolateFunction) invokeName() string { return f.Invoke }

// discoverIsolateFunctions resolves explicit entries only in the loaded graph.
// Source directives and external dependency policy belong to the driver.
func discoverIsolateFunctions(hosts []*load.Package) ([]isolateFunction, error) {
	config, err := readIsolateConfig()
	if err != nil {
		return nil, err
	}
	var functions []isolateFunction
	for _, p := range load.PackageList(hosts) {
		for _, entry := range config.Entries {
			if entry.Package == p.ImportPath && (entry.ForTest == p.ForTest || entry.ForTest == "" && config.Sources[isolatePackageKey(p.ImportPath, p.ForTest)] == "") {
				functions = append(functions, isolateFunction{p, entry.Function, entry.Value, entry.Invoke})
			}
		}
	}
	slices.SortFunc(functions, func(a, b isolateFunction) int { return strings.Compare(a.fullName(), b.fullName()) })
	return functions, nil
}

func isolateAdapterSource(p *load.Package) ([]byte, error) {
	config, err := readIsolateConfig()
	if err != nil {
		return nil, err
	}
	source := []byte(config.Sources[isolatePackageKey(p.ImportPath, p.ForTest)])
	if len(source) == 0 {
		source = []byte(config.Sources[p.ImportPath])
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "isolate adapter", source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if parsed.Name.Name != p.Name {
		return nil, fmt.Errorf("isolate config: adapter for %s declares package %s", p.ImportPath, parsed.Name.Name)
	}
	valuePackage := ""
	for _, spec := range parsed.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		if path == "isolate" {
			valuePackage = "isolate"
			if spec.Name != nil {
				valuePackage = spec.Name.Name
			}
			if valuePackage == "." || valuePackage == "_" {
				return nil, fmt.Errorf("isolate config: adapter requires a named isolate import")
			}
		}
		// Generated sources may use the public API and the package's own imports.
		// They cannot acquire privileged internal imports through this build hook.
		if path != "isolate" && path != "unsafe" && !slices.Contains(p.Imports, path) {
			return nil, fmt.Errorf("isolate config: adapter for %s adds unsupported import %q", p.ImportPath, path)
		}
	}
	if valuePackage == "" {
		return nil, fmt.Errorf("isolate config: adapter requires an isolate import")
	}
	declarations := make(map[string]*ast.FuncDecl)
	for _, decl := range parsed.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			declarations[fn.Name.Name] = fn
		}
	}
	alternateAliases := make(map[string]bool)
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			fields := strings.Fields(comment.Text)
			if len(fields) > 2 && fields[0] == "//go:linkname" {
				alternateAliases[fields[1]] = true
			}
		}
	}
	for _, entry := range config.Entries {
		if entry.Package != p.ImportPath || (entry.ForTest != p.ForTest && !(entry.ForTest == "" && config.Sources[isolatePackageKey(p.ImportPath, p.ForTest)] == "")) {
			continue
		}
		accessor := declarations[entry.Value]
		valid := accessor != nil && accessor.Recv == nil && accessor.Type.TypeParams == nil && accessor.Type.Params.NumFields() == 0 && accessor.Body != nil && len(accessor.Body.List) == 1
		if valid {
			ret, ok := accessor.Body.List[0].(*ast.ReturnStmt)
			valid = ok && len(ret.Results) == 1
			if valid {
				ident, ok := ret.Results[0].(*ast.Ident)
				valid = ok && ident.Name == entry.Function
			}
		}
		if !valid {
			return nil, fmt.Errorf("isolate config: accessor %s must return the selected function %s directly", entry.Value, entry.Function)
		}
		invoker := declarations[entry.Invoke]
		if invoker == nil || invoker.Recv != nil || invoker.Type.TypeParams != nil || invoker.Body == nil {
			return nil, fmt.Errorf("isolate config: invoker %s must be a concrete function with a body", entry.Invoke)
		}
		for _, fn := range []*ast.FuncDecl{accessor, invoker} {
			if alternateAliases[fn.Name.Name] {
				return nil, fmt.Errorf("isolate config: adapter %s cannot alias another symbol", fn.Name.Name)
			}
		}
		// Let the ordinary Go type checker validate the ABI in the source package.
		// Linkname declarations in the generated main alone cannot establish this.
		source = append(source, []byte(fmt.Sprintf("\nvar _ func() any = %s\nvar _ func(func(...%s.Value) error, func(...%s.Value) error) error = %s\n", entry.Value, valuePackage, valuePackage, entry.Invoke))...)
	}
	return source, nil
}

func isolatePackageKey(path, forTest string) string {
	if forTest != "" {
		return path + " [" + forTest + ".test]"
	}
	return path
}
func (e isolateConfigEntry) packageKey() string { return isolatePackageKey(e.Package, e.ForTest) }
