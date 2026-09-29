// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, root, dir, data string) string {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, FileName), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDirectories(t *testing.T) {
	root := t.TempDir()
	billing := writeConfig(t, root, "billing", `{"name":"billing"}`)
	orders := writeConfig(t, root, "orders", `{"name":"orders"}`)
	got, err := LoadDirectories([]string{orders, billing})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (Program{Name: "billing", Dir: billing}) || got[1] != (Program{Name: "orders", Dir: orders}) {
		t.Fatalf("LoadDirectories returned %#v", got)
	}
}

func TestLoadDirectoriesRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"empty name", `{"name":""}`, "name must be nonempty"},
		{"spaces", `{"name":" orders "}`, "without surrounding whitespace"},
		{"unknown field", `{"name":"orders","plugin":"orders.so"}`, "unknown field"},
		{"trailing object", `{"name":"orders"} {"name":"billing"}`, "expected one JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeConfig(t, t.TempDir(), "program", tt.data)
			_, err := LoadDirectories([]string{dir})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadDirectories error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadDirectoriesRejectsDuplicates(t *testing.T) {
	root := t.TempDir()
	first := writeConfig(t, root, "first", `{"name":"orders"}`)
	second := writeConfig(t, root, "second", `{"name":"orders"}`)
	if _, err := LoadDirectories([]string{first, second}); err == nil || !strings.Contains(err.Error(), "duplicate isolate name") {
		t.Fatalf("duplicate name error = %v", err)
	}
	if _, err := LoadDirectories([]string{first, first}); err == nil || !strings.Contains(err.Error(), "duplicate isolate directory") {
		t.Fatalf("duplicate directory error = %v", err)
	}
}
