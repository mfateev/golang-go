// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatecfg reads the per-directory configuration for statically
// linked isolate programs.
package isolatecfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const FileName = "isolate.json"

// Program identifies one source directory selected for the worker build.
// Name is the stable logical identity; Dir is the absolute source directory.
type Program struct {
	Name string
	Dir  string
}

type config struct {
	Name string `json:"name"`
}

// LoadDirectories reads exactly the named directories. It does not discover
// programs by walking the filesystem or decide whether a directory is a
// valid Go package; the caller must use the Go package loader for that.
func LoadDirectories(dirs []string) ([]Program, error) {
	programs := make([]Program, 0, len(dirs))
	byName := make(map[string]string, len(dirs))
	seenDir := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, fmt.Errorf("isolate directory %q: %w", dir, err)
		}
		if seenDir[abs] {
			return nil, fmt.Errorf("duplicate isolate directory %q", abs)
		}
		seenDir[abs] = true
		file := filepath.Join(abs, FileName)
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("isolate config %q: %w", file, err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var cfg config
		if err := dec.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("isolate config %q: %w", file, err)
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("isolate config %q: expected one JSON object", file)
			}
			return nil, fmt.Errorf("isolate config %q: %w", file, err)
		}
		if cfg.Name == "" || cfg.Name != strings.TrimSpace(cfg.Name) {
			return nil, fmt.Errorf("isolate config %q: name must be nonempty without surrounding whitespace", file)
		}
		if prior, ok := byName[cfg.Name]; ok {
			return nil, fmt.Errorf("duplicate isolate name %q in %q and %q", cfg.Name, prior, abs)
		}
		byName[cfg.Name] = abs
		programs = append(programs, Program{Name: cfg.Name, Dir: abs})
	}
	slices.SortFunc(programs, func(a, b Program) int { return strings.Compare(a.Name, b.Name) })
	return programs, nil
}
