// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package buildid_test

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds on the cross-binary build (HISS-02). One go build compiles all three binaries; the
// timeout covers a cold build cache on a slow runner.
const (
	binariesBuildTimeout = 5 * time.Minute
	binaryRunTimeout     = 30 * time.Second
	maxMainSourceFiles   = 256
)

// binary is one praetor executable and how it is asked for its version.
type binary struct {
	pkg    string
	name   string
	args   []string
	prefix string
}

// praetorBinaries are the three executables a release ships, by package. The release config's
// praetorctl and standardsctl builds share cmd/standardsctl.
var praetorBinaries = []binary{
	{pkg: "./cmd/standardsctl", name: "standardsctl", args: []string{"version"}, prefix: "praetorctl version "},
	{pkg: "./cmd/standards-mcp", name: "standards-mcp", args: []string{"-version"}, prefix: "standards-mcp "},
	{pkg: "./cmd/standards-lsp", name: "standards-lsp", args: []string{"-version"}, prefix: "standards-lsp "},
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// buildAll compiles every praetor binary with one go build and one ldflags value into a new
// directory, so all three carry the same injected version and the same VCS stamp.
func buildAll(t *testing.T, root, ldflags string) string {
	t.Helper()
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH; this test compiles the three praetor binaries")
	}
	out := t.TempDir()
	args := []string{"build", "-ldflags=" + ldflags, "-o", out + string(filepath.Separator)}
	for _, b := range praetorBinaries {
		args = append(args, b.pkg)
	}
	ctx, cancel := context.WithTimeout(t.Context(), binariesBuildTimeout)
	defer cancel()
	if _, err := util.RunCommand(ctx, root, goCommand, args...); err != nil {
		t.Fatalf("go build %v: %v", args, err)
	}
	return out
}

// reported runs b from dir and returns the identity it prints after its prefix.
func reported(t *testing.T, dir string, b binary) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), binaryRunTimeout)
	defer cancel()
	path := filepath.Join(dir, testsupport.ExecutableName(b.name))
	out, err := util.RunCommand(ctx, dir, path, b.args...)
	if err != nil {
		t.Fatalf("%s %v: %v", b.name, b.args, err)
	}
	identity, ok := strings.CutPrefix(out, b.prefix)
	if !ok {
		t.Fatalf("%s %v printed %q, want the prefix %q", b.name, b.args, out, b.prefix)
	}
	return identity
}

// fileIdentity is what buildid resolves from the build information embedded in the file.
func fileIdentity(t *testing.T, dir string, b binary) string {
	t.Helper()
	info, err := buildinfo.ReadFile(filepath.Join(dir, testsupport.ExecutableName(b.name)))
	if err != nil {
		t.Fatalf("read build information of %s: %v", b.name, err)
	}
	return buildid.Identify("", info).String()
}

// Positive, negative and boundary: standardsctl, standards-mcp and standards-lsp of one build
// report one identity (#666). An injected release reaches all three verbatim, which is what
// the release config's -X main.version does; before, standards-mcp and standards-lsp declared
// no main.version and reported v1.0.0 from every build. Without an injection, and with a blank
// one, each reports what its build information proves, and none reports v1.0.0.
func TestEveryBinaryReportsOneIdentityForOneBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles three binaries; skipped in -short mode")
	}
	root := moduleRoot(t)
	for _, tc := range []struct {
		name    string
		ldflags string
		release string
	}{
		{name: "injected release", ldflags: "-X main.version=v9.8.7-identity", release: "v9.8.7-identity"},
		{name: "no injection", ldflags: ""},
		{name: "blank injection", ldflags: "-X main.version="},
	} {
		dir := buildAll(t, root, tc.ldflags)
		want := tc.release
		if want == "" {
			want = fileIdentity(t, dir, praetorBinaries[0])
		}
		for _, b := range praetorBinaries {
			if got := reported(t, dir, b); got != want {
				t.Errorf("%s: %s reports %q, want %q", tc.name, b.name, got, want)
			}
			if got := fileIdentity(t, dir, b); tc.release == "" && got != want {
				t.Errorf("%s: %s carries build information for %q, want %q", tc.name, b.name, got, want)
			}
		}
		if want == "v1.0.0" {
			t.Errorf("%s: v1.0.0 is the literal that identified no build", tc.name)
		}
	}
}

// releaseConfig is the part of .goreleaser.yaml that decides what each binary reports.
type releaseConfig struct {
	Builds []struct {
		ID      string   `yaml:"id"`
		Main    string   `yaml:"main"`
		Ldflags []string `yaml:"ldflags"`
	} `yaml:"builds"`
}

// injectedVersionFlag is the -X flag the release config passes to every build.
const injectedVersionFlag = "-X main.version={{.Version}}"

// errNoInjectableVersion means a main package declares nothing -X main.version can write.
var errNoInjectableVersion = errors.New("no package-level var version")

// injectableVersion returns nil when files declare `var version` at package level as a
// string with an empty default: the only declaration -X main.version can write that also
// leaves a build without an injection to report what it can prove.
func injectableVersion(files []*ast.File) error {
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			if value, index := declaredVersion(gen); value != nil {
				return emptyStringDefault(gen.Tok, value, index)
			}
		}
	}
	return errNoInjectableVersion
}

// declaredVersion finds the spec in gen that names version, and the name's index in it.
func declaredVersion(gen *ast.GenDecl) (*ast.ValueSpec, int) {
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if i := slices.IndexFunc(value.Names, func(n *ast.Ident) bool { return n.Name == "version" }); i >= 0 {
			return value, i
		}
	}
	return nil, -1
}

// emptyStringDefault accepts a var of type string without a value, or with "" as its value.
func emptyStringDefault(tok token.Token, value *ast.ValueSpec, index int) error {
	if tok != token.VAR {
		return fmt.Errorf("version is declared with %s; the linker writes only a var", tok)
	}
	if len(value.Values) == 0 {
		if ident, ok := value.Type.(*ast.Ident); ok && ident.Name == "string" {
			return nil
		}
		return fmt.Errorf("version has type %v, want string", value.Type)
	}
	lit, ok := value.Values[index].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return errors.New("version default is not a string literal")
	}
	if unquoted, err := strconv.Unquote(lit.Value); err != nil || unquoted != "" {
		return fmt.Errorf("version defaults to %s; every build without an injection would report it", lit.Value)
	}
	return nil
}

func parseMainPackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for i := 0; i < len(entries) && i < maxMainSourceFiles; i++ {
		name := entries[i].Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	return files
}

// Positive and boundary: every build in the release config injects -X main.version, every
// main package it builds declares a version that flag can write, and the config builds all
// three binaries.
func TestReleaseConfigInjectsAVersionEveryBinaryDeclares(t *testing.T) {
	root := moduleRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg releaseConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var mains []string
	for _, build := range cfg.Builds {
		if !strings.Contains(strings.Join(build.Ldflags, " "), injectedVersionFlag) {
			t.Errorf("build %s does not pass %s: %v", build.ID, injectedVersionFlag, build.Ldflags)
		}
		if err := injectableVersion(parseMainPackage(t, filepath.Join(root, build.Main))); err != nil {
			t.Errorf("build %s (%s): %v", build.ID, build.Main, err)
		}
		mains = append(mains, build.Main)
	}
	for _, b := range praetorBinaries {
		if !slices.Contains(mains, b.pkg) {
			t.Errorf("the release config does not build %s; builds: %v", b.pkg, mains)
		}
	}
}

// Negative: the checker refuses each declaration that made a binary report v1.0.0 -- a const,
// a non-empty default, a differently named variable -- and accepts both injectable forms.
func TestInjectableVersionRefusesWhatMinusXCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		source string
		ok     bool
	}{
		{`const version = "v1.0.0"`, false},
		{`const lspVersion = "v1.0.0"`, false},
		{`var mcpVersion = "v1.0.0"`, false},
		{`var version = "v1.0.0"`, false},
		{`var version int`, false},
		{`var version = ""`, true},
		{`var version string`, true},
		{`var name, version = "praetor", ""`, true},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "main.go", "package main\n"+tc.source+"\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := injectableVersion([]*ast.File{file}); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.source, err, tc.ok)
		}
	}
}
