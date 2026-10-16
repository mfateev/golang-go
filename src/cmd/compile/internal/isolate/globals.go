// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"cmd/compile/internal/base"
	"cmd/compile/internal/ir"
	"cmd/compile/internal/typecheck"
	"cmd/compile/internal/types"
)

// Validate checks package bodies after inlining and before init rewriting and
// layout emission. Exported and instantiated bodies retain their source trust.
func Validate() {
	if base.Debug.IsolateGlobals != 0 {
		for _, fn := range typecheck.Target.Funcs {
			checkGlobalWrites(fn)
		}
	}
}

// checkGlobalWrites rejects direct writes from selected package code
// to process-owned globals in imported packages. The same selected package
// can run for the host and an isolate, so these writes cannot be allowed to
// silently modify the process copy during isolate execution.
func checkGlobalWrites(fn *ir.Func) {
	// Guarded helpers remain ordinary host functions. Their body cannot run
	// privately, including instantiated standard-library generic helpers.
	if fn.Pragma&ir.IsolateEffectGuarded != 0 || ir.IsolateEscapeHelper(fn) {
		return
	}
	check := func(lhs, stmt ir.Node) {
		name := globalRoot(lhs)
		if name == nil || name.Class != ir.PEXTERN {
			return
		}
		if processGlobal(name) {
			// The reference check below reports both reads and writes.
			return
		}
		pkg := name.Sym().Pkg
		if pkg != nil && pkg != types.LocalPkg && !base.IsolateImportSelected(pkg.Path) {
			base.ErrorfAt(stmt.Pos(), 0, "isolate: code writes unselected imported global %s.%s", pkg.Path, name.Sym().Name)
		}
	}
	// Every closure has its own entry in Target.Funcs.
	ir.VisitList(fn.Body, func(n ir.Node) {
		// Standard libraries also serve the host. Compulsory runtime checks
		// fence their process I/O globals only when executed privately.
		if name, ok := n.(*ir.Name); ok && processGlobal(name) && !(base.Flag.Std && base.Debug.IsolateHeap != 0) {
			base.ErrorfAt(fn.Pos(), 0, "isolate: code references process-owned global %s.%s", name.Sym().Pkg.Path, name.Sym().Name)
		}
		switch n := n.(type) {
		case *ir.AssignStmt:
			check(n.X, n)
		case *ir.AssignListStmt:
			for _, lhs := range n.Lhs {
				check(lhs, n)
			}
		case *ir.AssignOpStmt:
			check(n.X, n)
		case *ir.BinaryExpr:
			if n.Op() == ir.OCOPY {
				check(n.X, n)
			}
		case *ir.UnaryExpr:
			if n.Op() == ir.OCLEAR || n.Op() == ir.OCLOSE {
				check(n.X, n)
			}
		case *ir.CallExpr:
			if (n.Op() == ir.ODELETE || n.Op() == ir.OAPPEND) && len(n.Args) != 0 {
				check(n.Args[0], n)
			}
		case *ir.SendStmt:
			check(n.Chan, n)
		}
	})
}

// processGlobal identifies exported mutable process state whose value
// cannot safely be borrowed by selected isolate code. Even a read can hand
// out an alias that bypasses the direct-write check above.
func processGlobal(name *ir.Name) bool {
	if name.Class != ir.PEXTERN || name.Sym().Pkg == nil {
		return false
	}
	switch name.Sym().Pkg.Path {
	case "os":
		switch name.Sym().Name {
		case "Args", "Stdin", "Stdout", "Stderr":
			return true
		}
	case "flag":
		return name.Sym().Name == "CommandLine"
	}
	return false
}

// globalRoot follows the assignable part of an expression. Index
// operands and selector arguments are reads and do not make the target a write
// to those globals.
func globalRoot(n ir.Node) *ir.Name {
	switch n := n.(type) {
	case *ir.Name:
		return n
	case *ir.SelectorExpr:
		return globalRoot(n.X)
	case *ir.IndexExpr:
		return globalRoot(n.X)
	case *ir.SliceExpr:
		return globalRoot(n.X)
	case *ir.ParenExpr:
		return globalRoot(n.X)
	case *ir.StarExpr:
		return globalRoot(n.X)
	}
	return nil
}
