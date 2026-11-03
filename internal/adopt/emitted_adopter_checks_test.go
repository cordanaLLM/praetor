// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/docstrings"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// emittedAdopterTree writes the same renderings pinned by the emitted fixture generator.
func emittedAdopterTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, text := range emittedHookRenderings(t) {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	assets, err := managedasset.Assets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(asset.Path)), string(asset.Data))
	}
	for _, rel := range checkpointBundle {
		text := mustRead(t, filepath.Join("..", "..", filepath.FromSlash(rel)))
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), text)
	}
	return root
}

// emittedPythonFindings applies the Pelorus rules through Python's AST and comment tokenizer.
func emittedPythonFindings(t *testing.T, root string) []string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable; emitted Python adopter rules cannot run on this platform")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	out, err := util.RunCommand(ctx, "", python, "-B", "testdata/emitted_python_checks.py", root)
	if err != nil {
		t.Fatalf("emitted Python checks: %v\n%s", err, out)
	}
	var findings []string
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("decode emitted Python findings: %v\n%s", err, out)
	}
	return findings
}

// TestEmittedAdopterPythonRules checks suppressions and empty handlers across the emitted tree.
func TestEmittedAdopterPythonRules(t *testing.T) {
	for _, finding := range emittedPythonFindings(t, emittedAdopterTree(t)) {
		t.Error(finding)
	}
}

// TestEmittedAdopterPythonRulesPlanted proves each rule rejects its defect in an emitted copy.
func TestEmittedAdopterPythonRulesPlanted(t *testing.T) {
	for _, fixture := range []struct{ name, text, want string }{
		{"reason", "value = 1  # nosemgrep: example.rule\n", "nosemgrep needs"},
		{"start", "# nosemgrep: example.rule - explanation\nvalue = 1\n", "start line"},
		{"except", "try:\n    value = 1\nexcept:\n    pass\n", "empty except"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := emittedAdopterTree(t)
			mustWrite(t, filepath.Join(root, "planted.py"), fixture.text)
			findings := emittedPythonFindings(t, root)
			if len(findings) != 1 || !strings.Contains(findings[0], fixture.want) {
				t.Fatalf("findings=%v; want the planted %s defect", findings, fixture.name)
			}
		})
	}
}

// TestEmittedAdopterPythonRulesBoundary accepts explained handlers and ignores markers in strings.
func TestEmittedAdopterPythonRulesBoundary(t *testing.T) {
	root := t.TempDir()
	text := "value = (  # nosemgrep: first.rule, second.rule - explanation\n    1\n)\n" +
		"try:\n    value = 1\nexcept OSError:\n    # Missing optional state is empty.\n    pass\n" +
		"text = 'nosemgrep'\n"
	mustWrite(t, filepath.Join(root, "boundary.py"), text)
	if findings := emittedPythonFindings(t, root); len(findings) != 0 {
		t.Fatalf("valid boundary rejected: %v", findings)
	}
}

// emittedDocstringFindings checks every Python, JavaScript and Go function, without diff exclusions.
func emittedDocstringFindings(root string) ([]string, error) {
	var findings []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		switch filepath.Ext(path) {
		case ".py", ".js", ".mjs", ".cjs", ".go":
		default:
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		functions, err := hiss.ExtractFileFunctions(rel, data)
		if err != nil {
			return err
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for _, fn := range functions {
			if !docstrings.HasDocstring(rel, data, lines, fn) {
				findings = append(findings, fmt.Sprintf("%s:%d: undocumented %s", rel, fn.Start, fn.Name))
			}
		}
		return nil
	})
	return findings, err
}

// TestEmittedAdopterDocstrings requires complete coverage of the adoption renderings.
func TestEmittedAdopterDocstrings(t *testing.T) {
	findings, err := emittedDocstringFindings(emittedAdopterTree(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		t.Error(finding)
	}
}

// TestEmittedAdopterDocstringsPlanted rejects an undocumented function added to each emitted language.
func TestEmittedAdopterDocstringsPlanted(t *testing.T) {
	for _, fixture := range []struct{ path, text string }{
		{"planted.py", "def planted():\n    pass\n"},
		{"planted.mjs", "function planted() {}\n"},
		{"planted.go", "package planted\nfunc planted() {}\n"},
	} {
		t.Run(fixture.path, func(t *testing.T) {
			root := emittedAdopterTree(t)
			mustWrite(t, filepath.Join(root, fixture.path), fixture.text)
			findings, err := emittedDocstringFindings(root)
			if err != nil || len(findings) != 1 || !strings.Contains(findings[0], "undocumented planted") {
				t.Fatalf("findings=%v err=%v; want the planted undocumented function", findings, err)
			}
		})
	}
}
