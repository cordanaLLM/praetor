// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// HISS-01 forbids a cyclic call graph. The per-file scanner reports a function that calls
// itself, which is the single-node cycle; a cycle through two or more functions is invisible
// to it, because no single file's AST shows the loop closing. The coverage catalog recorded
// that honestly as a gap, and this closes it.
//
// The scope is a package, and that is complete rather than convenient. A call cycle spanning
// two packages would need each to import the other, which the Go compiler rejects outright,
// so every reachable call cycle in a buildable program is inside one package.
//
// Backported from golusoris/sveltesentio#252, which built the equivalent import-graph check
// for TypeScript and verified it by planting cycles rather than by watching it pass.

const (
	// maxCallGraphFiles bounds how many Go files one scan feeds to the call graph (HISS-02).
	maxCallGraphFiles = 20000
	// maxCallGraphFuncs bounds the nodes in a single package's graph (HISS-02).
	maxCallGraphFuncs = 4000
	// maxCallGraphEdges bounds the edges in a single package's graph (HISS-02).
	maxCallGraphEdges = 20000
	// maxCycleNamesReported bounds how much of a cycle is named in one message.
	maxCycleNamesReported = 8
)

// callGraph is one package's plain-function call graph: an edge means the caller names the
// callee as a bare identifier somewhere in its body.
type callGraph struct {
	edges map[string]map[string]struct{}
	decl  map[string]token.Pos
	file  map[string]string
	order []string
	count int
}

func newCallGraph() *callGraph {
	return &callGraph{
		edges: make(map[string]map[string]struct{}),
		decl:  make(map[string]token.Pos),
		file:  make(map[string]string),
	}
}

// reportCallCycles groups the scanned Go files by directory and reports each package's
// multi-function call cycles.
func reportCallCycles(ctx context.Context, rep *ScanReport, files []string, root string) {
	if len(files) > maxCallGraphFiles {
		rep.Truncated = true
		files = files[:maxCallGraphFiles]
	}
	byDir := make(map[string][]string)
	for i := 0; i < len(files) && i < maxCallGraphFiles; i++ {
		dir := filepath.Dir(files[i])
		byDir[dir] = append(byDir[dir], files[i])
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for i := 0; i < len(dirs) && i < maxCallGraphFiles; i++ {
		if ctx.Err() != nil {
			rep.Truncated = true
			return
		}
		reportPackageCycles(rep, byDir[dirs[i]], root)
	}
}

// reportPackageCycles builds one directory's graph and records every cycle it contains.
func reportPackageCycles(rep *ScanReport, files []string, root string) {
	fset := token.NewFileSet()
	graph := newCallGraph()
	for i := 0; i < len(files) && i < maxCallGraphFiles; i++ {
		file, err := parser.ParseFile(fset, files[i], nil, 0)
		if err != nil {
			// A file the scanner already reported on, or one this pass cannot parse, simply
			// contributes no edges. It must not abort the package: a graph missing one file
			// can still prove a cycle among the rest.
			continue
		}
		rel, relErr := filepath.Rel(root, files[i])
		if relErr != nil {
			rel = files[i]
		}
		graph.addFile(file, filepath.ToSlash(rel))
	}
	for _, cycle := range graph.cycles() {
		name := cycle[0]
		recordViolation(rep, "HISS-01", graph.file[name], fset.Position(graph.decl[name]).Line, name,
			fmt.Sprintf("Call cycle through %s; the call graph must form an acyclic DAG",
				strings.Join(cycle, " -> ")))
	}
}

// cycles returns one entry per strongly connected component of two or more functions, each
// as a deterministic name list closed back on its first element.
//
// A single-node component is not reported here even when it loops: direct recursion is the
// per-file scanner's finding, and reporting it twice would make one defect look like two.
func (g *callGraph) cycles() [][]string {
	state := newTarjanState(g)
	for i := 0; i < len(g.order) && i < maxCallGraphFuncs; i++ {
		if _, seen := state.index[g.order[i]]; !seen {
			state.strongConnect(g.order[i])
		}
	}
	sort.Slice(state.found, func(a, b int) bool { return state.found[a][0] < state.found[b][0] })
	return state.found
}

// tarjanState carries one run of Tarjan's strongly-connected-components search.
type tarjanState struct {
	graph   *callGraph
	index   map[string]int
	low     map[string]int
	onStack map[string]bool
	stack   []string
	next    int
	found   [][]string
}

func newTarjanState(g *callGraph) *tarjanState {
	return &tarjanState{
		graph:   g,
		index:   make(map[string]int),
		low:     make(map[string]int),
		onStack: make(map[string]bool),
	}
}

// walkFrame is one node's position in the explicit depth-first stack.
type walkFrame struct {
	name string
	succ []string
	next int
}

// strongConnect explores one root with an explicit stack.
//
// The textbook formulation of Tarjan recurses. This repository forbids recursion outright
// (HISS-01), and a checker for that invariant that violated it would be its own best
// counterexample, so the depth-first search carries its frames itself.
func (t *tarjanState) strongConnect(root string) {
	frames := []walkFrame{t.enter(root)}
	for i := 0; i < maxCallGraphEdges && len(frames) > 0; i++ {
		top := &frames[len(frames)-1]
		if top.next < len(top.succ) {
			callee := top.succ[top.next]
			top.next++
			switch {
			case t.indexMissing(callee):
				frames = append(frames, t.enter(callee))
			case t.onStack[callee]:
				t.low[top.name] = min(t.low[top.name], t.index[callee])
			}
			continue
		}
		name := top.name
		frames = frames[:len(frames)-1]
		if len(frames) > 0 {
			parent := frames[len(frames)-1].name
			t.low[parent] = min(t.low[parent], t.low[name])
		}
		if t.low[name] == t.index[name] {
			t.emit(name)
		}
	}
}

// enter numbers a node and pushes it onto the component stack.
func (t *tarjanState) enter(name string) walkFrame {
	t.index[name], t.low[name], t.next = t.next, t.next, t.next+1
	t.stack = append(t.stack, name)
	t.onStack[name] = true
	return walkFrame{name: name, succ: t.successors(name)}
}

// successors returns the deterministic callee list of one function.
func (t *tarjanState) successors(name string) []string {
	out := make([]string, 0, len(t.graph.edges[name]))
	for callee := range t.graph.edges[name] {
		// Keep the graph to functions this package declares. This is a bound, not a verdict:
		// an undeclared callee -- a builtin, or a name from elsewhere -- has no outgoing
		// edges, so it could never close a cycle even if it were walked. Deleting this line
		// changes no result, only how many isolated nodes the search visits, and saying so
		// is better than implying it decides something.
		if _, declared := t.graph.decl[callee]; declared {
			out = append(out, callee)
		}
	}
	sort.Strings(out)
	return out
}

func (t *tarjanState) indexMissing(name string) bool {
	_, seen := t.index[name]
	return !seen
}

// emit pops one component off the stack and keeps it when it holds a real cycle.
func (t *tarjanState) emit(root string) {
	var component []string
	for i := 0; i < maxCallGraphFuncs; i++ {
		last := len(t.stack) - 1
		if last < 0 {
			break
		}
		name := t.stack[last]
		t.stack = t.stack[:last]
		t.onStack[name] = false
		component = append(component, name)
		if name == root {
			break
		}
	}
	if len(component) < 2 {
		return
	}
	sort.Strings(component)
	if len(component) > maxCycleNamesReported {
		component = component[:maxCycleNamesReported]
	}
	t.found = append(t.found, append(component, component[0]))
}

// addFile records one file's top-level functions and the same-package functions they call.
func (g *callGraph) addFile(file *ast.File, rel string) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		// Methods are excluded deliberately: resolving a method call needs the receiver's
		// type, and guessing it would invent edges that do not exist. That remains a
		// declared gap rather than a silent approximation.
		if !ok || fn.Recv != nil || fn.Body == nil || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		if _, seen := g.decl[name]; !seen {
			if len(g.order) >= maxCallGraphFuncs {
				return
			}
			g.decl[name] = fn.Name.Pos()
			g.file[name] = rel
			g.order = append(g.order, name)
		}
		g.addCalls(name, fn)
	}
}

// addCalls adds an edge for every bare identifier this function calls.
func (g *callGraph) addCalls(caller string, fn *ast.FuncDecl) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		// A local of the same name shadows the function, so the call does not reach it.
		if !ok || declaresLocal(fn.Body, ident.Name) || ident.Name == caller {
			return true
		}
		if g.count >= maxCallGraphEdges {
			return false
		}
		if g.edges[caller] == nil {
			g.edges[caller] = make(map[string]struct{})
		}
		if _, dup := g.edges[caller][ident.Name]; !dup {
			g.edges[caller][ident.Name] = struct{}{}
			g.count++
		}
		return true
	})
}
