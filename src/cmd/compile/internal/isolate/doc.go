// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolate owns generic isolate IR checks and effect transformations.
//
// Source provenance is recorded/exported by noder and ir. ScopeEffects runs
// after import, initial instantiation and body construction, before inlining.
// Unified IR and the inliner also call ScopeEffectsInBody for newly read or
// instantiated bodies, retaining the source declaration's provenance.
//
// Validate runs after inlining and before package initialization rewriting,
// compiler layout emission, escape analysis and SSA. SSA address/access lowering
// stays in ssagen; initializer and descriptor emission stays with its owner.
// Frontend guard tables stop changing before parallel backend compilation.
//
// External library selection and source adaptation belong to the SDK build
// driver. This package applies generic provenance, effect and global checks.
package isolate
