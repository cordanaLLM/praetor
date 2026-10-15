// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRepoText_Positive_CRLFCheckoutReadsAsLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("```yaml\r\nkey: value\r\n```\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ReadRepoText(t, path)
	if want := "```yaml\nkey: value\n```\n"; got != want {
		t.Fatalf("ReadRepoText = %q, want %q", got, want)
	}
}

func lfFindings(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse planted source: %v", err)
	}
	return LFAssumptionFindings(fset, file)
}

func TestLFAssumptionFindings_Negative_RefusesRawSearchOfRepositoryText(t *testing.T) {
	src := `package p
func TestFences(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("..", "..", "docs", "adoption.md"))
	if len(strings.Split(string(data), "` + "```yaml\\n" + `")) < 2 {
		t.Fatal("no fences")
	}
}
func TestMarker(t *testing.T) {
	data, _ := os.ReadFile("../scripts/checkpoint.py")
	_ = strings.Contains(string(data), "\nMARKER\n")
}`
	got := lfFindings(t, src)
	if len(got) != 2 || !strings.Contains(got[0], "TestFences") || !strings.Contains(got[1], "TestMarker") {
		t.Fatalf("findings = %q, want one for TestFences and one for TestMarker", got)
	}
}

func TestLFAssumptionFindings_Positive_AcceptsNormalisedOrLocalText(t *testing.T) {
	src := `package p
func TestHelper(t *testing.T) {
	text := testsupport.ReadRepoText(t, filepath.Join("..", "..", "docs", "adoption.md"))
	_ = strings.Split(text, "\n")
}
func TestUtil(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("..", "x.md"))
	text, _ := util.NormalizeLineEndings(string(data))
	_ = strings.Split(text, "\n")
}
func TestReplace(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("..", "x.md"))
	_ = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}
func TestOwnTestdata(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("testdata", "x.md"))
	_ = strings.Split(string(data), "\n")
}
func TestNoLineSearch(t *testing.T) {
	data, _ := os.ReadFile(filepath.Join("..", "x.json"))
	_ = strings.Contains(string(data), "key")
}`
	if got := lfFindings(t, src); len(got) != 0 {
		t.Fatalf("findings = %q, want none", got)
	}
}

// TestRepositoryTests_Boundary_NoRawLFSearchOfCheckedInText runs the check over every test
// file of the module, so the next test that splits checked-in text on "\n" fails on every
// platform instead of on the Windows leg only (#847).
func TestRepositoryTests_Boundary_NoRawLFSearchOfCheckedInText(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %s has no go.mod: %v", root, err)
	}
	fset := token.NewFileSet()
	var findings []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		findings = append(findings, LFAssumptionFindings(fset, file)...)
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}
