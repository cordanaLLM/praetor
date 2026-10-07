// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"go/ast"
	slashpath "path"
	"path/filepath"
)

// A deadline-free context handed to a function of the same module, or to an allow-listed
// third-party constructor, is decided by files other than the caller's: the callee's body, the
// receiver's struct type, the module's go.mod. The walk reads one file at a time, so it records
// the finding as it always did and keeps beside it what would discharge it (ioProof). Once every
// Go file has been read, the package pass resolves each proof (moduleIndex) and drops the
// findings whose proof holds. A proof that cannot be resolved, or that fails, leaves its finding
// in the report, so every call this pass cannot judge is reported exactly as before.

// ioTarget is the function a deadline-free context reaches, as far as the calling file names it.
type ioTarget struct {
	// Dir is the slash-separated directory of the calling file, relative to the scan root, and
	// Pkg its package name: a bare call reaches a function of that package.
	Dir, Pkg string
	// ImportPath is the package a selector call names, empty for a call within the package.
	ImportPath string
	// RecvType is the base type of the enclosing method's receiver, for a call made through it.
	RecvType string
	// Struct and Field name the field a log/slog sink method is called on: a field of a
	// parameter or receiver of the package type Struct (loggerProof.sink).
	Struct, Field string
	// Name is the function, method or sink method called.
	Name string
}

// ioProof is a HISS-02 finding the package pass may discharge: the call at Violations[violation]
// carries a deadline-free context at each position in args, and target is what it calls.
type ioProof struct {
	violation int
	target    ioTarget
	args      []int
}

// callSite is what resolving a call's target needs to know about where the call is: the file's
// imports, directory and package, the enclosing method's receiver, and how many bindings of a
// name are in scope there (goScope.count). The walk and the callee walk share it, so a call
// resolves the same way wherever it is judged.
type callSite struct {
	im       GoImports
	dir, pkg string
	recvName string
	recvType string
	count    func(name string) int
}

// newCallSite describes a call inside fn, a function declaration of the file at dir, or outside
// any function when fn is nil.
func newCallSite(im GoImports, dir, pkg string, fn *ast.FuncDecl, count func(string) int) callSite {
	site := callSite{im: im, dir: dir, pkg: pkg, count: count, recvName: ReceiverName(fn)}
	if fn != nil && fn.Recv != nil && len(fn.Recv.List) > 0 {
		site.recvType, _ = ReceiverTypeName(fn.Recv.List[0].Type)
	}
	return site
}

// resolve names what call reaches for the package pass: the field a log/slog sink method is
// called on when the logger proof deferred it (sink), and otherwise the function target names.
func (s callSite) resolve(call *ast.CallExpr, sink sinkVerdict) (ioTarget, bool) {
	if sink.kind == fieldSink {
		return ioTarget{Dir: s.dir, Pkg: s.pkg, Struct: sink.Struct, Field: sink.Field, Name: sink.Field}, true
	}
	return s.target(call)
}

// target names the function call reaches: a bare identifier no binding shadows, a method called
// through the enclosing method's receiver, or a function of an imported package no binding
// shadows. Anything else names nothing the package pass can judge.
func (s callSite) target(call *ast.CallExpr) (ioTarget, bool) {
	base := ioTarget{Dir: s.dir, Pkg: s.pkg}
	switch fun := stripTypeArgs(call.Fun).(type) {
	case *ast.Ident:
		base.Name = fun.Name
		return base, s.count(fun.Name) == 0
	case *ast.SelectorExpr:
		return s.selectorTarget(base, fun)
	}
	return base, false
}

// selectorTarget resolves a selector callee (target).
func (s callSite) selectorTarget(base ioTarget, sel *ast.SelectorExpr) (ioTarget, bool) {
	base.Name = sel.Sel.Name
	x, ok := ast.Unparen(sel.X).(*ast.Ident)
	if !ok {
		return base, false
	}
	if s.isReceiver(x) {
		base.RecvType = s.recvType
		return base, true
	}
	path, bound := s.im.Path(x.Name)
	base.ImportPath = path
	return base, bound && s.count(x.Name) == 0
}

// isReceiver reports whether x is the enclosing method's receiver: its name, bound once where
// the call is, so no parameter or local hides it.
func (s callSite) isReceiver(x *ast.Ident) bool {
	return s.recvName != "" && s.recvName != "_" && s.recvType != "" && x.Name == s.recvName && s.count(x.Name) == 1
}

// slashDir returns the slash-separated directory of rel, a path relative to the scan root.
func slashDir(rel string) string {
	return slashpath.Dir(filepath.ToSlash(rel))
}

// spreads reports whether argument arg of call is spread into a variadic parameter, which the
// package pass never follows. Every argument before the spread one maps to its own parameter.
func spreads(call *ast.CallExpr, arg int) bool {
	return call.Ellipsis.IsValid() && arg == len(call.Args)-1
}

// deferProof keeps, beside the finding just recorded for call, what the package pass may prove
// to discharge it: the deadline-free contexts at args, and what call reaches (callSite.resolve,
// with the logger proof's verdict sink). A context spread into a variadic parameter gets none.
func (g *goScanner) deferProof(call *ast.CallExpr, args []int, sink sinkVerdict) {
	for i := 0; i < len(args); i++ {
		if spreads(call, args[i]) {
			return
		}
	}
	site := newCallSite(g.imports, slashDir(g.rel), g.pkg, g.enclosingFunc(), g.scope.count)
	target, ok := site.resolve(call, sink)
	if !ok {
		return
	}
	g.rep.ioProofs = append(g.rep.ioProofs, ioProof{violation: len(g.rep.Violations) - 1, target: target, args: args})
}

// resolveIOProofs drops each finding whose proof holds, once every Go file of the scan has been
// read; paths are those files and root the scan root. It stops proving when ctx ends, which
// leaves the remaining findings reported: an unfinished pass can only report more, never less.
func resolveIOProofs(ctx context.Context, rep *ScanReport, root string, paths []string) {
	proofs := rep.ioProofs
	rep.ioProofs = nil
	if len(proofs) == 0 {
		return
	}
	index := newModuleIndex(root, paths)
	discharged := make(map[int]bool)
	for i := 0; i < len(proofs) && i < MaxInfractionsCap; i++ {
		if ctx.Err() != nil {
			break
		}
		if index.discharges(proofs[i]) {
			discharged[proofs[i].violation] = true
		}
	}
	dropViolations(rep, discharged)
}

// dropViolations removes the violations at the indexes drop holds and takes them out of the
// breakdown, so the report reads as if they had never been recorded.
func dropViolations(rep *ScanReport, drop map[int]bool) {
	if len(drop) == 0 {
		return
	}
	kept := rep.Violations[:0]
	for i := 0; i < len(rep.Violations); i++ {
		if !drop[i] {
			kept = append(kept, rep.Violations[i])
			continue
		}
		rule := rep.Violations[i].RuleID
		if rep.Breakdown[rule]--; rep.Breakdown[rule] <= 0 {
			delete(rep.Breakdown, rule)
		}
	}
	rep.Violations = kept
}
