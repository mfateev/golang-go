// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"cmd/go/internal/fsys"
	"cmd/go/internal/load"
)

type isolateFunction struct {
	Package *load.Package
	Name    string
	Params  []ast.Expr
	Results []ast.Expr
	Imports map[string]string
}

func (f isolateFunction) fullName() string   { return f.Package.ImportPath + "." + f.Name }
func (f isolateFunction) valueName() string  { return "__isolate_value_" + f.Name }
func (f isolateFunction) invokeName() string { return "__isolate_invoke_" + f.Name }

// discoverIsolateFunctions scans only source files selected by the package
// loader, so build constraints and the host's module graph remain authoritative.
// Unmarked builds retain the ordinary compilation and registration paths.
func discoverIsolateFunctions(hosts []*load.Package) ([]isolateFunction, error) {
	var functions []isolateFunction
	for _, p := range load.PackageList(hosts) {
		if p.Standard {
			continue
		}
		for _, file := range slices.Concat(p.GoFiles, p.CgoFiles) {
			path := filepath.Join(p.Dir, file)
			data, err := fsys.ReadFile(path)
			if err != nil {
				return nil, err
			}
			if !bytes.Contains(data, []byte("//go:isolate")) {
				continue
			}
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, path, data, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			imports := make(map[string]string)
			for _, spec := range parsed.Imports {
				importPath, _ := strconv.Unquote(spec.Path.Value)
				name := ""
				if spec.Name != nil {
					name = spec.Name.Name
				} else {
					for _, dep := range p.Internal.Imports {
						if dep.ImportPath == importPath {
							name = dep.Name
							break
						}
					}
				}
				imports[name] = importPath
			}
			claimed := make(map[*ast.Comment]bool)
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Doc == nil {
					continue
				}
				var marker *ast.Comment
				for _, comment := range fn.Doc.List {
					if isIsolateDirective(comment.Text) {
						if marker != nil || strings.TrimSpace(comment.Text) != "//go:isolate" {
							return nil, fmt.Errorf("%s: expected one //go:isolate directive without arguments", fset.Position(comment.Pos()))
						}
						marker = comment
					}
				}
				if marker == nil {
					continue
				}
				claimed[marker] = true
				fail := func(reason string) error {
					return fmt.Errorf("%s: //go:isolate %s", fset.Position(marker.Pos()), reason)
				}
				if fn.Recv != nil || fn.Type.TypeParams != nil || fn.Body == nil || fn.Name.Name == "main" || fn.Name.Name == "init" {
					return nil, fail("requires a concrete, top-level function with a body other than main or init")
				}
				if slices.Contains(p.CgoFiles, file) || imports["."] != "" {
					return nil, fail("does not yet support cgo source or dot imports")
				}
				params := isolateFieldTypes(fn.Type.Params)
				for _, param := range params {
					if _, ok := param.(*ast.Ellipsis); ok {
						return nil, fail("does not support variadic arguments")
					}
					if selector, ok := param.(*ast.SelectorExpr); ok {
						if alias, ok := selector.X.(*ast.Ident); ok && imports[alias.Name] == "go.temporal.io/sdk/workflow" && selector.Sel.Name == "Context" {
							return nil, fail("uses ordinary Go arguments; Temporal workflow.Context is host-owned")
						}
					}
				}
				results := isolateFieldTypes(fn.Type.Results)
				if len(results) > 2 || len(results) != 0 && !isolateErrorType(results[len(results)-1]) {
					return nil, fail("requires no return values, error, or (result, error)")
				}
				functions = append(functions, isolateFunction{Package: p, Name: fn.Name.Name, Params: params, Results: results, Imports: imports})
			}
			for _, group := range parsed.Comments {
				for _, comment := range group.List {
					if isIsolateDirective(comment.Text) && !claimed[comment] {
						return nil, fmt.Errorf("%s: //go:isolate must immediately precede a function declaration", fset.Position(comment.Pos()))
					}
				}
			}
		}
	}
	slices.SortFunc(functions, func(a, b isolateFunction) int { return strings.Compare(a.fullName(), b.fullName()) })
	return functions, nil
}

func isIsolateDirective(text string) bool {
	return text == "//go:isolate" || strings.HasPrefix(text, "//go:isolate ") || strings.HasPrefix(text, "//go:isolate\t")
}

func isolateFieldTypes(fields *ast.FieldList) []ast.Expr {
	var result []ast.Expr
	if fields != nil {
		for _, field := range fields.List {
			for range max(1, len(field.Names)) {
				result = append(result, field.Type)
			}
		}
	}
	return result
}

func isolateErrorType(expr ast.Expr) bool {
	name, ok := expr.(*ast.Ident)
	return ok && name.Name == "error"
}

// isolateInvokerSource creates typed slots in the original package, where
// private argument/result types remain accessible. Conversion is supplied by
// the SDK inside the isolate; invocation is a normal, statically typed call.
func isolateInvokerSource(p *load.Package, functions []isolateFunction) ([]byte, error) {
	aliases := make(map[string]string)
	var body strings.Builder
	for _, fn := range functions {
		if fn.Package != p {
			continue
		}
		fmt.Fprintf(&body, "//go:linkname %s\nfunc %s() any { return %s }\n", fn.valueName(), fn.valueName(), fn.Name)
		fmt.Fprintf(&body, "//go:linkname %s\nfunc %s(decode, encode func(...isolateGeneratedBridge.Value) error) error {\n", fn.invokeName(), fn.invokeName())
		var args, slots []string
		for i, param := range fn.Params {
			var encoded bytes.Buffer
			if err := format.Node(&encoded, token.NewFileSet(), param); err != nil {
				return nil, err
			}
			expr, err := parser.ParseExpr(encoded.String())
			if err != nil {
				return nil, err
			}
			ast.Inspect(expr, func(node ast.Node) bool {
				if selector, ok := node.(*ast.SelectorExpr); ok {
					if ident, ok := selector.X.(*ast.Ident); ok {
						if path := fn.Imports[ident.Name]; path != "" {
							alias, exists := aliases[path]
							if !exists {
								alias = fmt.Sprintf("isolateGeneratedImport%d", len(aliases))
								aliases[path] = alias
							}
							ident.Name = alias
						}
					}
				}
				return true
			})
			encoded.Reset()
			if err := format.Node(&encoded, token.NewFileSet(), expr); err != nil {
				return nil, err
			}
			arg := fmt.Sprintf("arg%d", i)
			fmt.Fprintf(&body, "var %s %s\n", arg, encoded.String())
			args = append(args, arg)
			slots = append(slots, fmt.Sprintf("isolateGeneratedBridge.Value{Value:%s, Pointer:&%s}", arg, arg))
		}
		fmt.Fprintf(&body, "if err := decode(%s); err != nil { return err }\n", strings.Join(slots, ","))
		switch len(fn.Results) {
		case 0:
			fmt.Fprintf(&body, "%s(%s)\nreturn encode()\n", fn.Name, strings.Join(args, ","))
		case 1:
			fmt.Fprintf(&body, "if err := %s(%s); err != nil { return err }; return encode()\n", fn.Name, strings.Join(args, ","))
		case 2:
			fmt.Fprintf(&body, "result, err := %s(%s)\nif err != nil { return err }\nreturn encode(isolateGeneratedBridge.Value{Value:result, Pointer:&result})\n", fn.Name, strings.Join(args, ","))
		}
		body.WriteString("}\n")
	}
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\nimport (\nisolateGeneratedBridge %q\n_ %q\n", p.Name, "internal/isolatebridge", "unsafe")
	paths := make([]string, 0, len(aliases))
	for path := range aliases {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		fmt.Fprintf(&source, "%s %q\n", aliases[path], path)
	}
	source.WriteString(")\n")
	source.WriteString(body.String())
	return format.Source([]byte(source.String()))
}
