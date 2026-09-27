// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"context"
	"go/ast"
	"go/token"
	"strconv"
)

// maxDelegationHops bounds how far a handler that only forwards its argument list is
// followed to the function that dispatches on it (HISS-02).
const maxDelegationHops = 4

// childEntry is one subcommand word a function dispatches on and the candidate functions
// that handle it, the called function of a return statement first. No candidate that names a
// function of the package means the dispatching function handles the word inline.
type childEntry struct {
	word     string
	handlers []string
}

// dispatchSites is what one function's body shows about the subcommand it reads from its
// argument list: the words it compares that argument against, the package-level maps it
// looks the argument up in, and the local functions it hands the whole list to.
type dispatchSites struct {
	entries   []childEntry
	mapRefs   []string
	delegates []string
}

// subjectTracker follows which expressions of one function hold the word at the head of its
// argument list: args[0], a variable assigned from it, or the first result of a call that
// takes the list (sub, rest := split(args); action := positionalAt(positional, 0, "")). A
// slice such as args[1:] is the next level's input and is deliberately not followed.
type subjectTracker struct {
	lists  map[string]bool
	values map[string]bool
}

// dispatchOf reads one function's dispatch sites, or returns nil when it takes no []string
// argument list.
func dispatchOf(fn *ast.FuncDecl) *dispatchSites {
	tracker := newSubjectTracker(fn)
	if fn.Body == nil || len(tracker.lists) == 0 {
		return nil
	}
	sites := &dispatchSites{}
	localMaps := map[string][]childEntry{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.AssignStmt:
			tracker.assign(typed, localMaps)
		case *ast.SwitchStmt:
			if tracker.subject(typed.Tag) {
				sites.entries = append(sites.entries, switchEntries(typed)...)
			}
		case *ast.IfStmt:
			sites.entries = append(sites.entries, tracker.ifEntries(typed)...)
		case *ast.IndexExpr:
			sites.mapLookup(typed, tracker, localMaps)
		case *ast.CallExpr:
			sites.delegation(typed, tracker)
		}
		return true
	})
	return sites
}

// newSubjectTracker starts a tracker from a function's []string parameters.
func newSubjectTracker(fn *ast.FuncDecl) *subjectTracker {
	tracker := &subjectTracker{lists: map[string]bool{}, values: map[string]bool{}}
	if fn.Type.Params == nil {
		return tracker
	}
	for _, field := range fn.Type.Params.List {
		if isStringSlice(field.Type) {
			for _, name := range field.Names {
				tracker.lists[name.Name] = true
			}
		}
	}
	return tracker
}

// isStringSlice reports whether a type expression is []string.
func isStringSlice(expr ast.Expr) bool {
	array, ok := expr.(*ast.ArrayType)
	if !ok || array.Len != nil {
		return false
	}
	elem, ok := array.Elt.(*ast.Ident)
	return ok && elem.Name == "string"
}

// assign follows one assignment: x := args[0] holds the word, the first result of a call that
// takes the list holds it too, and a local map literal is remembered for a later lookup.
func (s *subjectTracker) assign(stmt *ast.AssignStmt, localMaps map[string][]childEntry) {
	if len(stmt.Lhs) == 0 || len(stmt.Rhs) == 0 {
		return
	}
	target, ok := stmt.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	switch value := stmt.Rhs[0].(type) {
	case *ast.IndexExpr:
		if s.headOfList(value) {
			s.values[target.Name] = true
		}
	case *ast.CallExpr:
		if s.takesList(value) {
			s.values[target.Name], s.lists[target.Name] = true, true
		}
	case *ast.CompositeLit:
		if entries := mapLiteralEntries(value); len(entries) > 0 {
			localMaps[target.Name] = entries
		}
	}
}

// headOfList reports whether expr is list[0] of a tracked list.
func (s *subjectTracker) headOfList(expr *ast.IndexExpr) bool {
	list, ok := expr.X.(*ast.Ident)
	if !ok || !s.lists[list.Name] {
		return false
	}
	index, ok := expr.Index.(*ast.BasicLit)
	return ok && index.Kind == token.INT && index.Value == "0"
}

// takesList reports whether a call is handed a tracked list or word unchanged.
func (s *subjectTracker) takesList(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok && (s.lists[ident.Name] || s.values[ident.Name]) {
			return true
		}
	}
	return false
}

// subject reports whether expr holds the word at the head of the argument list.
func (s *subjectTracker) subject(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return s.values[typed.Name]
	case *ast.IndexExpr:
		return s.headOfList(typed)
	}
	return false
}

// switchEntries returns one entry per string literal a switch's cases compare against.
func switchEntries(stmt *ast.SwitchStmt) []childEntry {
	var entries []childEntry
	for _, clause := range stmt.Body.List {
		caseClause, ok := clause.(*ast.CaseClause)
		if !ok {
			continue
		}
		handlers := handlerCandidates(caseClause.Body)
		for _, expr := range caseClause.List {
			if word, ok := stringLiteral(expr); ok {
				entries = append(entries, childEntry{word: word, handlers: handlers})
			}
		}
	}
	return entries
}

// ifEntries returns the words an if condition compares the subject against. Equality hands
// the word to the if body's handler; inequality names a word the function handles inline.
func (s *subjectTracker) ifEntries(stmt *ast.IfStmt) []childEntry {
	var entries []childEntry
	ast.Inspect(stmt.Cond, func(n ast.Node) bool {
		binary, ok := n.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
			return true
		}
		word, ok := comparedWord(s, binary)
		if !ok {
			return true
		}
		entry := childEntry{word: word}
		if binary.Op == token.EQL {
			entry.handlers = handlerCandidates(stmt.Body.List)
		}
		entries = append(entries, entry)
		return true
	})
	return entries
}

// comparedWord returns the literal a comparison sets against the subject.
func comparedWord(s *subjectTracker, binary *ast.BinaryExpr) (string, bool) {
	if s.subject(binary.X) {
		return stringLiteral(binary.Y)
	}
	if s.subject(binary.Y) {
		return stringLiteral(binary.X)
	}
	return "", false
}

// mapLookup records a lookup of the subject in a map: a local map literal contributes its
// entries directly, a package-level map by name.
func (d *dispatchSites) mapLookup(expr *ast.IndexExpr, s *subjectTracker, localMaps map[string][]childEntry) {
	table, ok := expr.X.(*ast.Ident)
	if !ok || !s.subject(expr.Index) {
		return
	}
	if entries, local := localMaps[table.Name]; local {
		d.entries = append(d.entries, entries...)
		return
	}
	d.mapRefs = append(d.mapRefs, table.Name)
}

// delegation records a local function the whole argument list is handed to.
func (d *dispatchSites) delegation(call *ast.CallExpr, s *subjectTracker) {
	callee, ok := call.Fun.(*ast.Ident)
	if !ok {
		return
	}
	for _, arg := range call.Args {
		if ident, isIdent := arg.(*ast.Ident); isIdent && s.lists[ident.Name] {
			d.delegates = append(d.delegates, callee.Name)
			return
		}
	}
}

// handlerCandidates lists the plain function calls in a block, the calls of return
// statements first, so the function a case returns into outranks a usage printer or helper
// the case calls on the way.
func handlerCandidates(stmts []ast.Stmt) []string {
	var returned, called []string
	for _, stmt := range stmts {
		_, isReturn := stmt.(*ast.ReturnStmt)
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent {
				if isReturn {
					returned = append(returned, ident.Name)
				} else {
					called = append(called, ident.Name)
				}
			}
			return true
		})
	}
	return append(returned, called...)
}

// mapLiteralEntries returns the string keys of a map literal and the function each maps to.
func mapLiteralEntries(lit *ast.CompositeLit) []childEntry {
	if _, isMap := lit.Type.(*ast.MapType); !isMap {
		return nil
	}
	var entries []childEntry
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		word, ok := stringLiteral(pair.Key)
		if !ok {
			continue
		}
		entry := childEntry{word: word}
		if handler, isIdent := pair.Value.(*ast.Ident); isIdent {
			entry.handlers = []string{handler.Name}
		}
		entries = append(entries, entry)
	}
	return entries
}

// stringLiteral returns the value of a string literal expression.
func stringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

// children returns the subcommand words function dispatches on, each mapped to the function
// that handles it ("" when handled inline). A function that dispatches on nothing but hands
// its whole argument list to a local function is followed to that function, as far as
// maxDelegationHops. Only word-shaped entries count: "-h" and "--help" are help flags, not
// subcommands. An empty result means function is a leaf and every later word is an operand.
func (t *sourceTree) children(ctx context.Context, dir, function string) (map[string]string, error) {
	decls, err := t.load(ctx, dir)
	if err != nil {
		return nil, err
	}
	current := function
	for hop := 0; hop < maxDelegationHops && current != ""; hop++ {
		decl := decls[current]
		if decl == nil || decl.dispatch == nil {
			return nil, nil
		}
		if resolved := resolveEntries(decls, decl.dispatch); len(resolved) > 0 {
			return resolved, nil
		}
		current = firstFunction(decls, decl.dispatch.delegates)
	}
	return nil, nil
}

// resolveEntries maps each word-shaped entry of sites to the first candidate that is a
// function of the package.
func resolveEntries(decls map[string]*declaration, sites *dispatchSites) map[string]string {
	entries := append([]childEntry(nil), sites.entries...)
	for _, name := range sites.mapRefs {
		if table := decls[name]; table != nil {
			entries = append(entries, table.mapEntries...)
		}
	}
	resolved := map[string]string{}
	for _, entry := range entries {
		if _, seen := resolved[entry.word]; seen || !wordPattern.MatchString(entry.word) {
			continue
		}
		resolved[entry.word] = firstFunction(decls, entry.handlers)
	}
	return resolved
}

// firstFunction returns the first name that is a plain function of the package, or "".
func firstFunction(decls map[string]*declaration, names []string) string {
	for _, name := range names {
		if decl := decls[name]; decl != nil && decl.function {
			return name
		}
	}
	return ""
}
