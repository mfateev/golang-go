// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatepolicy_test

import (
	"internal/isolatepolicy"
	"testing"
)

func TestEffectManifest(t *testing.T) {
	for _, name := range []string{
		"isolate.New", "internal/isolatebridge.New", "time.NewTicker", "time.Tick",
		"os.Getenv", "os.(*File).Read", "os.(*File).Write-fm", "syscall.Syscall.abi0",
		"golang.org/x/sys/unix.RawSyscall", "net.(*Dialer).DialContext",
		"net/http.(*Client).Do", "runtime.AddCleanup[go.shape.*uint8,go.shape.int]",
		"weak.Pointer[go.shape.struct { p *example.org/a/b.Value }].Value",
		"unique.Make[go.shape.string]", "time.LoadLocation", "crypto/rand.Prime",
	} {
		if !isolatepolicy.ForbiddenSymbol(name) {
			t.Errorf("allowed forbidden symbol %s", name)
		}
	}
	for _, name := range []string{
		"os.Exit", "os.IsNotExist", "os.FileMode.String", "syscall.Exit", "syscall.Errno.Error",
		"net.ParseIP", "net/url.Parse", "net/http.NewRequest", "time.FixedZone", "time.LoadLocationFromTZData",
		"time.AfterFunc", "math/rand.Int", "flag.NewFlagSet", "flag.(*FlagSet).Parse", "bytes.(*Buffer).Write",
		"example.org/jobs.Work", "runtime.mallocgc", "reflect.Value.Call",
		"crypto/rand.Read", "crypto/rand.Text", "crypto/rand.Int",
	} {
		if isolatepolicy.ForbiddenSymbol(name) {
			t.Errorf("denied supported symbol %s", name)
		}
	}
}
