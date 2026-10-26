// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package toolpins_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxScannedFiles bounds the number of Go files inspected during the walk (HISS-02).
const maxScannedFiles = 5000

var versionPinPattern = regexp.MustCompile(`^v?\d+\.\d+(\.\d+)?`)

// frozenTemplatesPins allow-lists the five frozen templates constants kept for API stability.
var frozenTemplatesPins = map[string]string{
	"GoSecurityGovulnVersion":   "frozen 2026-10-26 for API stability; live pin in internal/toolpins",
	"GoSecurityGosecVersion":    "frozen 2026-10-26 for API stability; live pin in internal/toolpins",
	"GoSecurityGitleaksVersion": "frozen 2026-10-26 for API stability; live pin in internal/toolpins",
	"GoSecurityXToolsVersion":   "frozen 2026-10-26 for API stability; live pin in internal/toolpins",
	"GoSecurityXToolsFloor":     "frozen 2026-10-26 for API stability; live pin in internal/toolpins",
}

type pinViolation struct {
	File  string
	Name  string
	Value string
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return name == "internal" || name == "testdata" || name == "vendor"
}

func isEligibleGoSource(relPath, baseName string) bool {
	if !strings.HasSuffix(baseName, ".go") || strings.HasSuffix(baseName, "_test.go") {
		return false
	}
	slashed := filepath.ToSlash(relPath)
	if strings.HasPrefix(slashed, "internal/") || strings.Contains(slashed, "/internal/") {
		return false
	}
	if strings.HasPrefix(slashed, "testdata/") || strings.Contains(slashed, "/testdata/") {
		return false
	}
	return !strings.HasPrefix(slashed, "vendor/") && !strings.Contains(slashed, "/vendor/")
}

func checkValueSpec(spec *ast.ValueSpec, filePath string, allowList map[string]string) []pinViolation {
	var violations []pinViolation
	for i, ident := range spec.Names {
		if !ident.IsExported() {
			continue
		}
		if _, allowed := allowList[ident.Name]; allowed {
			continue
		}
		if i >= len(spec.Values) {
			continue
		}
		lit, ok := spec.Values[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		unquoted, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		if versionPinPattern.MatchString(unquoted) {
			violations = append(violations, pinViolation{
				File:  filePath,
				Name:  ident.Name,
				Value: unquoted,
			})
		}
	}
	return violations
}

func inspectGoFile(fset *token.FileSet, filePath string, allowList map[string]string) ([]pinViolation, error) {
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	if util.IsGeneratedGoSource(node) {
		return nil, nil
	}
	var violations []pinViolation
	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valSpec, isVal := spec.(*ast.ValueSpec)
			if !isVal {
				continue
			}
			violations = append(violations, checkValueSpec(valSpec, filePath, allowList)...)
		}
	}
	return violations, nil
}

func handleWalkEntry(root, path string, d os.DirEntry, fset *token.FileSet, allowList map[string]string) ([]pinViolation, bool, error) {
	if d.IsDir() {
		if path != root && shouldSkipDir(d.Name()) {
			return nil, false, filepath.SkipDir
		}
		return nil, false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, false, fmt.Errorf("rel path %s: %w", path, err)
	}
	if !isEligibleGoSource(rel, d.Name()) {
		return nil, false, nil
	}
	v, err := inspectGoFile(fset, path, allowList)
	if err != nil {
		return nil, true, fmt.Errorf("inspect %s: %w", path, err)
	}
	return v, true, nil
}

func findPublicPinViolations(root string, allowList map[string]string) ([]pinViolation, error) {
	var allViolations []pinViolation
	fileCount := 0
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk entry %s: %w", path, walkErr)
		}
		v, counted, err := handleWalkEntry(root, path, d, fset, allowList)
		if err != nil {
			return err
		}
		if counted {
			fileCount++
			if fileCount > maxScannedFiles {
				return fmt.Errorf("scanned file count exceeded cap %d at %s", maxScannedFiles, path)
			}
		}
		allViolations = append(allViolations, v...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find public pin violations: %w", err)
	}
	return allViolations, nil
}

func TestPublicPinsGuard_RealRepository(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	violations, err := findPublicPinViolations(repoRoot, frozenTemplatesPins)
	if err != nil {
		t.Fatalf("scan repo for public pin violations: %v", err)
	}
	if len(violations) > 0 {
		for _, v := range violations {
			t.Errorf("unlisted exported pin %s = %q in public package %s; move to internal/toolpins", v.Name, v.Value, v.File)
		}
	}
}

func TestPublicPinsGuard_Planted_ExportedInPublicReported(t *testing.T) {
	tmp := t.TempDir()
	pkgDir := filepath.Join(tmp, "sample")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir sample: %v", err)
	}
	src := "package sample\n\nconst ScannerVersion = \"v1.2.3\"\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "sample.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}
	violations, err := findPublicPinViolations(tmp, frozenTemplatesPins)
	if err != nil {
		t.Fatalf("findPublicPinViolations: %v", err)
	}
	if len(violations) != 1 || violations[0].Name != "ScannerVersion" {
		t.Fatalf("want 1 violation for ScannerVersion, got %v", violations)
	}
}

func TestPublicPinsGuard_Planted_InternalPathIgnored(t *testing.T) {
	tmp := t.TempDir()
	internalDir := filepath.Join(tmp, "internal", "sample")
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	src := "package sample\n\nconst ScannerVersion = \"v1.2.3\"\n"
	if err := os.WriteFile(filepath.Join(internalDir, "sample.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write sample.go: %v", err)
	}
	violations, err := findPublicPinViolations(tmp, frozenTemplatesPins)
	if err != nil {
		t.Fatalf("findPublicPinViolations: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for internal/ package, got %v", violations)
	}
}

func TestPublicPinsGuard_Planted_AllowListedIgnored(t *testing.T) {
	tmp := t.TempDir()
	tmplDir := filepath.Join(tmp, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	src := "package templates\n\nconst GoSecurityGovulnVersion = \"v1.8.0\"\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "embed.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write embed.go: %v", err)
	}
	violations, err := findPublicPinViolations(tmp, frozenTemplatesPins)
	if err != nil {
		t.Fatalf("findPublicPinViolations: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations for allow-listed constant, got %v", violations)
	}
}

func TestPublicPinsGuard_Mutant_ExportedTemplatesPinReported(t *testing.T) {
	tmp := t.TempDir()
	tmplDir := filepath.Join(tmp, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	src := "package templates\n\nconst FooVersion = \"v1.2.3\"\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "embed.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write embed.go: %v", err)
	}
	violations, err := findPublicPinViolations(tmp, frozenTemplatesPins)
	if err != nil {
		t.Fatalf("findPublicPinViolations: %v", err)
	}
	if len(violations) != 1 || violations[0].Name != "FooVersion" {
		t.Fatalf("want 1 violation for mutant FooVersion, got %v", violations)
	}
}
