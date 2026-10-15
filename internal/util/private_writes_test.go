// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A private artefact's mode protects it on POSIX only. On Windows the file inherits its
// directory's DACL, which on a profile folder can grant other accounts (#101). The writers of
// this package set an owner-only DACL; a function elsewhere that creates a file or directory
// with a private mode itself must call RestrictFileAccess, RestrictPath or RestrictIn on it.

// privateCreateCalls are the creating calls whose last argument is the new entry's mode.
var privateCreateCalls = map[string]bool{"OpenFile": true, "WriteFile": true, "Mkdir": true, "MkdirAll": true}

// daclRestrictors are the calls that give a created entry the owner-only DACL.
var daclRestrictors = map[string]bool{"RestrictFileAccess": true, "RestrictPath": true, "RestrictIn": true}

// unrestrictedPrivateCreates reports each function of file that creates an entry with a mode
// granting no group or other access (a 0o600 or 0o700 literal) and restricts none.
func unrestrictedPrivateCreates(fset *token.FileSet, file *ast.File) []string {
	var findings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var create token.Pos
		restricts := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch name := selector.Sel.Name; {
			case daclRestrictors[name]:
				restricts = true
			case privateCreateCalls[name] && len(call.Args) > 0 && privateModeLiteral(call.Args[len(call.Args)-1]):
				if create == token.NoPos {
					create = call.Pos()
				}
			}
			return true
		})
		if create != token.NoPos && !restricts {
			findings = append(findings, fmt.Sprintf("%s: %s creates a private entry without RestrictFileAccess, RestrictPath or RestrictIn",
				fset.Position(create), fn.Name.Name))
		}
	}
	return findings
}

func privateModeLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return false
	}
	value, err := strconv.ParseInt(strings.ReplaceAll(lit.Value, "_", ""), 0, 64)
	return err == nil && (value == 0o600 || value == 0o700)
}

// buildsForWindows reports whether file, named name, is part of a windows/amd64 build.
func buildsForWindows(name string, file *ast.File) bool {
	base := strings.TrimSuffix(filepath.Base(name), ".go")
	for _, goos := range []string{"_linux", "_darwin", "_freebsd", "_openbsd", "_netbsd", "_android", "_ios", "_js", "_wasip1", "_plan9", "_solaris", "_aix", "_illumos", "_dragonfly"} {
		if strings.HasSuffix(base, goos) || strings.Contains(base, goos+"_") {
			return false
		}
	}
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			if !constraint.IsGoBuild(comment.Text) {
				continue
			}
			expr, err := constraint.Parse(comment.Text)
			if err != nil {
				return true
			}
			return expr.Eval(func(tag string) bool {
				return tag == "windows" || tag == "amd64" || strings.HasPrefix(tag, "go1.")
			})
		}
	}
	return true
}

func TestUnrestrictedPrivateCreates_Negative_RefusesPlantedCreates(t *testing.T) {
	src := `package p
func a(root *os.Root) error {
	_, err := root.OpenFile("x", os.O_CREATE, 0o600)
	return err
}
func b() error { return os.Mkdir("d", 0o700) }
func c() error { return os.WriteFile("f", nil, 0600) }`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := unrestrictedPrivateCreates(fset, file); len(got) != 3 {
		t.Fatalf("findings = %q, want one each for a, b and c", got)
	}
}

func TestUnrestrictedPrivateCreates_Positive_AcceptsRestrictedAndPublic(t *testing.T) {
	src := `package p
func a(root *os.Root) error {
	f, err := root.OpenFile("x", os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	return util.RestrictFileAccess(f, 0o600)
}
func b() error {
	if err := os.Mkdir("d", 0o700); err != nil {
		return err
	}
	return util.RestrictPath("d", 0o700)
}
func c() error { return os.WriteFile("f", nil, 0o644) }
func d(root *os.Root) error {
	if err := root.Mkdir("d", 0o700); err != nil {
		return err
	}
	return util.RestrictIn(root, "d", 0o700)
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "accepted.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := unrestrictedPrivateCreates(fset, file); len(got) != 0 {
		t.Fatalf("findings = %q, want none", got)
	}
}

func TestBuildsForWindows_3D(t *testing.T) {
	for src, want := range map[string]bool{
		"package p":                               true,
		"//go:build !windows\n\npackage p":        false,
		"//go:build unix\n\npackage p":            false,
		"//go:build windows\n\npackage p":         true,
		"//go:build !linux\n\npackage p":          true,
		"//go:build linux || darwin\n\npackage p": false,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "f.go", src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if got := buildsForWindows("f.go", file); got != want {
			t.Errorf("%q: buildsForWindows = %v, want %v", src, got, want)
		}
	}
	file, _ := parser.ParseFile(token.NewFileSet(), "x_linux.go", "package p", parser.ParseComments)
	if buildsForWindows("x_linux.go", file) {
		t.Error("a _linux.go file was counted in the Windows build")
	}
}

// TestRepositoryPrivateCreates_Boundary_EveryOneRestricted runs the check over every non-test
// file of the module that builds for Windows (#101).
func TestRepositoryPrivateCreates_Boundary_EveryOneRestricted(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod: %v", root, err)
	}
	own, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var findings []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch {
			case path == own, entry.Name() == ".git", entry.Name() == "testdata", entry.Name() == "vendor", entry.Name() == "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if buildsForWindows(path, file) {
			findings = append(findings, unrestrictedPrivateCreates(fset, file)...)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}
