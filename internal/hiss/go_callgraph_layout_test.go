// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shape #140 reported: a recursive-descent validator whose three functions call each other,
// the closing edges inside if initialisers, in a package two directories below the scan root.
// A Windows run reported the cycle and a Linux run of an older build did not; these tests pin the
// finding, its path and its line, so every platform's leg must produce the same one.

// strictJSONValue dispatches on the next byte; scanObject and scanArray call back into it.
const strictJSONValue = `package strictjson

func scanValue(data []byte, i int) (int, error) {
	switch data[i] {
	case '{':
		return scanObject(data, i+1)
	case '[':
		return scanArray(data, i+1)
	}
	return i + 1, nil
}
`

// strictJSONContainers holds the two container scanners. Each reaches scanValue only from an
// if initialiser, the structural feature a simplified reproduction would miss.
const strictJSONContainers = `package strictjson

func scanObject(data []byte, i int) (int, error) {
	for i < len(data) && data[i] != '}' {
		if next, err := scanValue(data, i); err != nil {
			return next, err
		}
		i++
	}
	return i, nil
}

func scanArray(data []byte, i int) (int, error) {
	for i < len(data) && data[i] != ']' {
		if next, err := scanValue(data, i); err != nil {
			return next, err
		}
		i++
	}
	return i, nil
}
`

// strictJSONScanArrayLine is the line of scanArray's declaration in strictJSONContainers: the
// cycle is reported at its first function by name.
const strictJSONScanArrayLine = 13

// scanNestedSources writes each slash-separated path under a fresh root and returns the HISS-01
// findings of a scan of that root.
func scanNestedSources(t *testing.T, files map[string]string) []InvariantViolation {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Scan(context.Background(), root, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found []InvariantViolation
	for _, v := range report.Violations {
		if v.RuleID == "HISS-01" {
			found = append(found, v)
		}
	}
	return found
}

// Positive: the cycle is found in a nested package, named in sorted order, at a slash path
// relative to the root and at scanArray's line, whichever file of the package holds which
// function and whatever sibling package the walk meets first.
func TestCallGraphReportsTheStrictJSONCycleInANestedPackage(t *testing.T) {
	const want = "Call cycle through scanArray -> scanObject -> scanValue -> scanArray; the call graph must form an acyclic DAG"
	layouts := map[string]map[string]string{
		"containers after value": {
			"tooling/internal/strictjson/value.go":    strictJSONValue,
			"tooling/internal/strictjson/validate.go": strictJSONContainers,
		},
		"containers before value": {
			"tooling/internal/strictjson/a_value.go":   strictJSONValue,
			"tooling/internal/strictjson/0validate.go": strictJSONContainers,
		},
		"sibling package first": {
			"tooling/internal/aaa/leaf.go":            "package aaa\n\nfunc scanArray() int { return 1 }\n",
			"tooling/internal/strictjson/value.go":    strictJSONValue,
			"tooling/internal/strictjson/validate.go": strictJSONContainers,
		},
	}
	for name, files := range layouts {
		t.Run(name, func(t *testing.T) {
			var containers string
			for rel, body := range files {
				if body == strictJSONContainers {
					containers = rel
				}
			}
			for run := range 3 {
				found := scanNestedSources(t, files)
				if len(found) != 1 {
					t.Fatalf("run %d: want one cycle, got %d: %s", run, len(found), cycleMessages(found))
				}
				got := found[0]
				if got.Message != want || got.FilePath != containers || got.LineNumber != strictJSONScanArrayLine || got.Symbol != "scanArray" {
					t.Fatalf("run %d: got %s:%d %s %q, want %s:%d scanArray %q",
						run, got.FilePath, got.LineNumber, got.Symbol, got.Message, containers, strictJSONScanArrayLine, want)
				}
			}
		})
	}
}

// Negative: the same package without the calls back into scanValue is a DAG, so the positive
// test's finding comes from the if-initialiser edges and not from the names alone.
func TestCallGraphAcceptsTheStrictJSONShapeWithoutItsClosingEdges(t *testing.T) {
	acyclic := strings.ReplaceAll(strictJSONContainers, "scanValue(data, i)", "len(data[i:]), error(nil)")
	if acyclic == strictJSONContainers {
		t.Fatal("the fixture no longer holds the closing calls")
	}
	found := scanNestedSources(t, map[string]string{
		"tooling/internal/strictjson/value.go":    strictJSONValue,
		"tooling/internal/strictjson/validate.go": acyclic,
	})
	if len(found) != 0 {
		t.Fatalf("an acyclic package must not be reported, got: %s", cycleMessages(found))
	}
}
