// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func newQuietFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func TestParseInterspersed_Positive_FlagsAfterPositionals(t *testing.T) {
	fs := newQuietFlagSet("flavor audit")
	flavor := fs.String("flavor", "auto", "")
	force := fs.Bool("force", false, "")

	positional, err := parseInterspersed(fs, []string{"./services/api", "--flavor=rust-systems", "--force"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(positional) != 1 || positional[0] != "./services/api" {
		t.Fatalf("expected the directory as the only positional, got %v", positional)
	}
	if *flavor != "rust-systems" {
		t.Errorf("a flag written after the positional must still bind, got %q", *flavor)
	}
	if !*force {
		t.Errorf("--force written after the positional must still bind")
	}
}

func TestParseInterspersed_Positive_SpaceSeparatedFlagValue(t *testing.T) {
	fs := newQuietFlagSet("bump canary")
	target := fs.String("target", "", "")

	positional, err := parseInterspersed(fs, []string{"github.com/spf13/cobra", "--target", "v1.9.1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *target != "v1.9.1" {
		t.Errorf("space-separated flag value mis-bound: %q", *target)
	}
	if len(positional) != 1 || positional[0] != "github.com/spf13/cobra" {
		t.Errorf("package positional mis-bound: %v", positional)
	}
}

func TestParseInterspersed_Positive_TerminatorMakesRestPositional(t *testing.T) {
	fs := newQuietFlagSet("term")
	name := fs.String("name", "", "")

	positional, err := parseInterspersed(fs, []string{"--name=a", "first", "--", "--not-a-flag", "second"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *name != "a" {
		t.Errorf("expected name=a, got %q", *name)
	}
	want := []string{"first", "--not-a-flag", "second"}
	if len(positional) != len(want) {
		t.Fatalf("expected %v, got %v", want, positional)
	}
	for i := range want {
		if positional[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, positional)
		}
	}
}

func TestParseInterspersed_Negative_UnknownFlagAndNilSet(t *testing.T) {
	fs := newQuietFlagSet("unknown")
	fs.String("flavor", "auto", "")

	if _, err := parseInterspersed(fs, []string{"dir", "--nope=1"}); err == nil {
		t.Error("an unknown flag after a positional must be rejected, not accepted as a positional")
	}
	if _, err := parseInterspersed(nil, []string{"x"}); err == nil {
		t.Error("expected an error for a nil flag set")
	}
}

func TestParseInterspersed_Boundary_EmptyAndOverflow(t *testing.T) {
	fs := newQuietFlagSet("empty")
	positional, err := parseInterspersed(fs, nil)
	if err != nil {
		t.Fatalf("parse of no arguments: %v", err)
	}
	if len(positional) != 0 {
		t.Errorf("expected no positionals, got %v", positional)
	}

	overflow := make([]string, maxCLIArgs+1)
	for i := range overflow {
		overflow[i] = "a"
	}
	_, err = parseInterspersed(newQuietFlagSet("overflow"), overflow)
	if err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Errorf("expected the argument-count bound to be enforced, got %v", err)
	}
}

func TestPositionalAt_3D(t *testing.T) {
	positional := []string{"first", "", "third"}

	if got := positionalAt(positional, 0, "fallback"); got != "first" {
		t.Errorf("expected first, got %q", got)
	}
	if got := positionalAt(positional, 1, "fallback"); got != "fallback" {
		t.Errorf("an empty positional must fall back, got %q", got)
	}
	if got := positionalAt(positional, 9, "fallback"); got != "fallback" {
		t.Errorf("an out-of-range index must fall back, got %q", got)
	}
	if got := positionalAt(nil, -1, "fallback"); got != "fallback" {
		t.Errorf("a negative index on a nil slice must fall back, got %q", got)
	}
}

// interspersedCase is one argv parsed by a FlagSet with --path and --log value flags and a
// --dry-run boolean flag.
type interspersedCase struct {
	name    string
	in      []string
	pos     []string
	path    string
	log     string
	dryRun  bool
	wantErr string
}

var interspersedCases = []interspersedCase{
	{name: "equals form", in: []string{"--path=/x", "target"}, pos: []string{"target"}, path: "/x"},
	{name: "space form keeps its value", in: []string{"--path", "/x", "target"}, pos: []string{"target"}, path: "/x"},
	{name: "flag after positional binds", in: []string{"target", "--log=msg"}, pos: []string{"target"}, path: ".", log: "msg"},
	{name: "bool flag does not swallow positional", in: []string{"--dry-run", "target"}, pos: []string{"target"}, path: ".", dryRun: true},
	{name: "bool then value flag after positional", in: []string{"dir", "--dry-run", "--path", "/x"}, pos: []string{"dir"}, path: "/x", dryRun: true},
	{name: "positionals keep their order", in: []string{"a", "--log=m", "b", "c"}, pos: []string{"a", "b", "c"}, path: ".", log: "m"},
	// BUG-895: the terminator itself must never be lost, so what follows stays positional.
	{name: "leading terminator", in: []string{"--", "--x"}, pos: []string{"--x"}, path: "."},
	{name: "terminator after a flag run", in: []string{"--log=a", "--", "pos", "--y"}, pos: []string{"pos", "--y"}, path: ".", log: "a"},
	{name: "terminator after a bool flag", in: []string{"--dry-run", "--", "--path", "x"}, pos: []string{"--path", "x"}, path: ".", dryRun: true},
	{name: "terminator after a positional", in: []string{"first", "--", "--dry-run"}, pos: []string{"first", "--dry-run"}, path: "."},
	{name: "second terminator is positional", in: []string{"--", "--"}, pos: []string{"--"}, path: "."},
	{name: "terminator as a value flag's value", in: []string{"--log", "--", "pos"}, pos: []string{"pos"}, path: ".", log: "--"},
	{name: "unknown flag after positional", in: []string{"dir", "--nope"}, wantErr: "flag provided but not defined: -nope"},
	{name: "unknown flag after terminator is positional", in: []string{"dir", "--", "--nope"}, pos: []string{"dir", "--nope"}, path: "."},
	{name: "trailing value flag without value", in: []string{"dir", "--path"}, wantErr: "flag needs an argument: -path"},
	{name: "empty input", in: []string{}, pos: []string{}, path: "."},
	{name: "lone dash is positional", in: []string{"-"}, pos: []string{"-"}, path: "."},
	{name: "trailing terminator", in: []string{"dir", "--"}, pos: []string{"dir"}, path: "."},
	{name: "lone terminator", in: []string{"--"}, pos: []string{}, path: "."},
}

func TestParseInterspersed_Table(t *testing.T) {
	for _, tc := range interspersedCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newQuietFlagSet("fixture")
			path := fs.String("path", ".", "")
			logMsg := fs.String("log", "", "")
			dryRun := fs.Bool("dry-run", false, "")
			pos, err := parseInterspersed(fs, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v (positional %q)", tc.wantErr, err, pos)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", tc.in, err)
			}
			if strings.Join(pos, "\x00") != strings.Join(tc.pos, "\x00") || len(pos) != len(tc.pos) {
				t.Errorf("positional: got %q, want %q", pos, tc.pos)
			}
			if strings.Join(fs.Args(), "\x00") != strings.Join(pos, "\x00") || fs.NArg() != len(pos) {
				t.Errorf("fs.Args() %q must equal the returned positionals %q", fs.Args(), pos)
			}
			if *path != tc.path || *logMsg != tc.log || *dryRun != tc.dryRun {
				t.Errorf("flags: path=%q log=%q dry-run=%v, want %q %q %v", *path, *logMsg, *dryRun, tc.path, tc.log, tc.dryRun)
			}
		})
	}
}

func TestParseInterspersed_Boundary_ExactlyMaxArgs(t *testing.T) {
	args := make([]string, maxCLIArgs)
	for i := range args {
		args[i] = "a"
	}
	pos, err := parseInterspersed(newQuietFlagSet("exact"), args)
	if err != nil || len(pos) != maxCLIArgs {
		t.Fatalf("exactly %d positionals must parse, got %d positionals, err %v", maxCLIArgs, len(pos), err)
	}
	fs := newQuietFlagSet("exact-flags")
	count := fs.Int("n", 0, "")
	flagArgs := make([]string, 0, maxCLIArgs)
	for i := 0; i < maxCLIArgs/2; i++ {
		flagArgs = append(flagArgs, "--n=1", "p")
	}
	if pos, err = parseInterspersed(fs, flagArgs); err != nil || len(pos) != maxCLIArgs/2 || *count != 1 {
		t.Fatalf("alternating flags and positionals at the bound: %d positionals, n=%d, err %v", len(pos), *count, err)
	}
}

// TestNoCommandParsesRawArgv fails when a command calls Parse on a FlagSet it created
// instead of routing argv through parseInterspersed. flag.FlagSet.Parse stops at the first
// positional and silently drops every flag after it (BUG-895).
func TestNoCommandParsesRawArgv(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("list package sources: %v (%d files)", err, len(files))
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "flagargs.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, pos := range rawFlagSetParses(file) {
			t.Errorf("%s: FlagSet.Parse on raw argv; use parseInterspersed", fset.Position(pos))
		}
	}
}

func TestRawFlagSetParses_DetectsDirectParse(t *testing.T) {
	const src = `package p
import "flag"
func a(args []string) { fs := flag.NewFlagSet("a", flag.ContinueOnError); _ = fs.Parse(args) }
func b(fs *flag.FlagSet, args []string) { _ = fs.Parse(args) }
func c(args []string) { var fs = flag.NewFlagSet("c", flag.ContinueOnError); _, _ = parseInterspersed(fs, args) }
`
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if got := len(rawFlagSetParses(file)); got != 2 {
		t.Fatalf("want the 2 direct Parse calls reported, got %d", got)
	}
}

// rawFlagSetParses returns the position of every X.Parse call in file where X was assigned
// from flag.NewFlagSet or declared as a *flag.FlagSet parameter.
//
// Known gap: a FlagSet returned by a helper constructor (fs := newFlags()) or reached
// through a selector (c.fs.Parse(args)) is not followed. No command binds one that way
// today; a command that starts to must extend this guard and its fixture first.
func rawFlagSetParses(file *ast.File) []token.Pos {
	flagSets := flagSetNames(file)
	var found []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && isParseOn(call, flagSets) {
			found = append(found, call.Pos())
		}
		return true
	})
	return found
}

// flagSetNames returns the identifiers file binds to a *flag.FlagSet.
func flagSetNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			recordFlagSetNames(names, node.Lhs, node.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(node.Names))
			for i, id := range node.Names {
				lhs[i] = id
			}
			recordFlagSetNames(names, lhs, node.Values)
		case *ast.Field:
			if types.ExprString(node.Type) == "*flag.FlagSet" {
				for _, id := range node.Names {
					names[id.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// isParseOn reports whether call is X.Parse(...) on an identifier bound to a FlagSet.
func isParseOn(call *ast.CallExpr, flagSets map[string]bool) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parse" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	return ok && flagSets[recv.Name]
}

func recordFlagSetNames(flagSets map[string]bool, lhs, rhs []ast.Expr) {
	if len(lhs) != len(rhs) {
		return
	}
	for i, expr := range rhs {
		if id, ok := lhs[i].(*ast.Ident); ok && isNewFlagSetCall(expr) {
			flagSets[id.Name] = true
		}
	}
}

func isNewFlagSetCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && types.ExprString(call.Fun) == "flag.NewFlagSet"
}
