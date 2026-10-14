// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"slices"
	"strings"

	"cmd/go/internal/load"
)

// compilerForcedFlags is used for both the action hash and compiler invocation.
// Unrelated programs must not invalidate a dependency's cache entry. Imported
// globals and inlined bodies still need selection and entry information from
// the complete transitive import graph.
func compilerForcedFlags(p *load.Package) []string {
	const prefix = "-d=isolate"
	options := make(map[string]string)
	var flags []string
	for _, flag := range forcedGcflags {
		name, value, ok := strings.Cut(strings.TrimPrefix(flag, prefix), "=")
		if strings.HasPrefix(flag, prefix) && ok {
			switch name {
			case "packages", "effectentries", "effects", "metadata", "build":
				options[name] = value
				continue
			}
		}
		flags = append(flags, flag)
	}
	if len(options) == 0 {
		return flags
	}
	if options["packages"] != "" {
		// State selection may become empty after filtering. Checks must not.
		options["build"] = "1"
	}
	dependencies := make(map[string]bool)
	for _, dep := range load.PackageList([]*load.Package{p}) {
		dependencies[dep.ImportPath] = true
	}
	for _, name := range []string{"packages", "effectentries"} {
		value, ok := options[name]
		if !ok {
			continue
		}
		var relevant []string
		for _, item := range strings.Split(value, ":") {
			path := item
			if name == "effectentries" {
				if dot := strings.LastIndexByte(item, '.'); dot >= 0 {
					path = item[:dot]
				}
			}
			if dependencies[path] {
				relevant = append(relevant, item)
			}
		}
		slices.Sort(relevant)
		options[name] = strings.Join(slices.Compact(relevant), ":")
	}
	// Fold repeated manifests from multi-package test planning into their final
	// values. The same effective configuration then has one stable cache key.
	for _, name := range []string{"build", "effects", "metadata", "packages", "effectentries"} {
		if value, ok := options[name]; ok && value != "" {
			flags = append(flags, prefix+name+"="+value)
		}
	}
	return flags
}
