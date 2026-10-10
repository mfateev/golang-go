// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate_test

import (
	"context"
	"errors"
	"internal/isolatebridge"
	"isolate"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEffectFaultRevokesInitializationAndGoroutines(t *testing.T) {
	for _, stage := range []string{"initializer", "main", "child"} {
		t.Run(stage, func(t *testing.T) {
			var deferred atomic.Bool
			violate := func() {
				defer func() { deferred.Store(true); _ = recover() }()
				runtime.GC()
			}
			name := "effect-fault-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
			isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{MetadataVersion: isolate.MetadataVersion,
				NewState: func() (func(func()), error) {
					if stage == "initializer" {
						violate()
					}
					return func(fn func()) { fn() }, nil
				},
				Main: func() {
					if stage == "child" {
						blocked := make(chan struct{})
						go violate()
						<-blocked
					} else {
						violate()
					}
				},
			})
			program, _ := isolate.LookupProgram(name)
			i, err := isolate.New(isolate.Config{Program: program})
			if stage != "initializer" {
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Start(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-i.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("fault did not terminate instance")
				}
				err = i.Wait()
			} else if i != nil {
				t.Fatal("initializer fault returned instance")
			}
			var effect *isolate.EffectError
			if !errors.As(err, &effect) || effect.Operation != "runtime.GC" || !strings.Contains(effect.Stack, "TestEffectFaultRevokesInitializationAndGoroutines") {
				t.Fatalf("fault: %v", err)
			}
			if i != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := i.Kill(ctx)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				if err := i.Resume(); err == nil {
					t.Fatal("faulted instance resumed")
				}
			}
			if deferred.Load() {
				t.Fatal("application recover/defer ran")
			}
		})
	}
}

// Fatal checks remain active while an audited service uses the process owner.
// Service cleanup releases process locks before revocation discards the caller;
// application recovery and defers never run.
func TestEffectFaultDuringMetadataCleanup(t *testing.T) {
	var applicationDeferred atomic.Bool
	cleanup := make(chan struct{})
	name := "effect-metadata-" + strconv.FormatUint(ownerTestSequence.Add(1), 10)
	isolatebridge.RegisterProgram(name, isolatebridge.ProgramEntry{MetadataVersion: isolate.MetadataVersion,
		NewState: func() (func(func()), error) { return func(fn func()) { fn() }, nil },
		Main: func() {
			defer func() { applicationDeferred.Store(true); _ = recover() }()
			func() {
				owner := isolatebridge.EnterProcess()
				defer isolatebridge.LeaveProcess(owner)
				defer close(cleanup)
				runtime.GC()
			}()
		},
	})
	program, _ := isolate.LookupProgram(name)
	i, err := isolate.New(isolate.Config{Program: program})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-i.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("metadata fault did not terminate")
	}
	var effect *isolate.EffectError
	if !errors.As(i.Wait(), &effect) || effect.Operation != "runtime.GC" {
		t.Fatalf("fault: %v", i.Wait())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := i.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleanup:
	default:
		t.Fatal("trusted cleanup did not run")
	}
	if applicationDeferred.Load() {
		t.Fatal("application recovered after metadata cleanup")
	}
}
