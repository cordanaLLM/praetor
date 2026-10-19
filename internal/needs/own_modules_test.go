// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package needs

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

// Part 1: A requirement is the repository's own when its module path is declared by a go.mod
// inside the same checkout, whether resolved through go.work or a walk.
func TestOwnModulesInCheckout_Positive(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, root, "a/go.mod", "module example.com/r/a\n\ngo 1.24\n")
	writeFixture(t, root, "a/a.go", "package a\n")

	bMod := "module example.com/r/b\n\ngo 1.24\n\nrequire (\n\texample.com/r/a v0.1.0\n\tgithub.com/x/y v1.0.0\n)\n"
	writeFixture(t, root, "b/go.mod", bMod)
	writeFixture(t, root, "b/b.go", "package b\n\nimport (\n\t_ \"example.com/r/a\"\n\t_ \"github.com/x/y\"\n)\n")

	report, err := NewGoAnalyzer().Analyze(t.Context(), filepath.Join(root, "b"), Target{})
	if err != nil {
		t.Fatalf("scan b failed: %v", err)
	}
	if len(report.Dependencies) != 1 || report.Dependencies[0].Package != "github.com/x/y" {
		t.Fatalf("scan of b = %+v, want only github.com/x/y", report.Dependencies)
	}

	// Same with a go.work using ./a and ./b.
	writeFixture(t, root, "go.work", "go 1.24\n\nuse (\n\t./a\n\t./b\n)\n")
	reportWork, err := NewGoAnalyzer().Analyze(t.Context(), filepath.Join(root, "b"), Target{})
	if err != nil {
		t.Fatalf("scan b with go.work failed: %v", err)
	}
	if len(reportWork.Dependencies) != 1 || reportWork.Dependencies[0].Package != "github.com/x/y" {
		t.Fatalf("scan of b with go.work = %+v, want only github.com/x/y", reportWork.Dependencies)
	}
}

// Negative: a requirement not declared by any go.mod in the checkout stays listed as a dependency.
func TestOwnModulesInCheckout_Negative(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, root, "a/go.mod", "module example.com/r/a\n\ngo 1.24\n")

	bMod := "module example.com/r/b\n\ngo 1.24\n\nrequire (\n\texample.com/r/a v0.1.0\n\texample.com/other/a v1.0.0\n\tgithub.com/x/y v1.0.0\n)\n"
	writeFixture(t, root, "b/go.mod", bMod)
	writeFixture(t, root, "b/b.go", "package b\n\nimport (\n\t_ \"example.com/r/a\"\n\t_ \"example.com/other/a\"\n\t_ \"github.com/x/y\"\n)\n")

	report, err := NewGoAnalyzer().Analyze(t.Context(), filepath.Join(root, "b"), Target{})
	if err != nil {
		t.Fatalf("scan b failed: %v", err)
	}
	pkgs := make(map[string]bool)
	for i := 0; i < len(report.Dependencies) && i < 100; i++ {
		pkgs[report.Dependencies[i].Package] = true
	}
	if !pkgs["example.com/other/a"] {
		t.Errorf("undeclared requirement example.com/other/a was dropped: %v", pkgs)
	}
	if !pkgs["github.com/x/y"] {
		t.Errorf("github.com/x/y was dropped: %v", pkgs)
	}
	if pkgs["example.com/r/a"] {
		t.Errorf("checkout own module example.com/r/a was not dropped: %v", pkgs)
	}
}

// Boundary: discovery over its bound fails the scan with the bound named, not a partial result.
func TestOwnModulesInCheckout_Boundary(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, root, "b/go.mod", "module example.com/r/b\n\ngo 1.24\n\nrequire github.com/x/y v1.0.0\n")

	var sb strings.Builder
	sb.WriteString("go 1.24\n\nuse (\n")
	for i := 0; i <= gomanifest.MaxDiscoveredModules; i++ {
		fmt.Fprintf(&sb, "\t./dir%d\n", i)
	}
	sb.WriteString(")\n")
	writeFixture(t, root, "go.work", sb.String())

	_, err := NewGoAnalyzer().Analyze(t.Context(), filepath.Join(root, "b"), Target{})
	if err == nil {
		t.Fatal("expected discovery over bound to fail the scan")
	}
	boundStr := strconv.Itoa(gomanifest.MaxDiscoveredModules)
	if !strings.Contains(err.Error(), boundStr) {
		t.Fatalf("error %q does not name the bound %s", err.Error(), boundStr)
	}
}
