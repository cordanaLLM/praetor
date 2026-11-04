// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// pythonPublicDocstrings parses source without executing it, reporting every public definition
// whose first statement is not a nonempty docstring, including classes and async functions.
const pythonPublicDocstrings = `import ast
import sys

tree = ast.parse(sys.stdin.read())
definitions = (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)
missing = [node for node in ast.walk(tree)
           if isinstance(node, definitions) and not node.name.startswith("_")
           and not ast.get_docstring(node)]
for node in missing:
    print(str(node.lineno) + ": " + node.name + " lacks a docstring")
sys.exit(bool(missing))
`

// checkPythonPublicDocstrings checks text with Python's parser under the interceptor timeout.
func checkPythonPublicDocstrings(t *testing.T, python, text string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), interceptorWallClock)
	defer cancel()
	stdinCtx, err := util.WithCommandStdin(ctx, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return util.RunCommand(stdinCtx, "", python, "-B", "-c", pythonPublicDocstrings)
}

// Positive: every Python hook rendered by adoption documents all public functions and classes.
func TestEmittedPythonHooksHavePublicDocstrings(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH; rendered Python docstrings cannot be checked on this leg")
	}
	checked := 0
	for rel, text := range emittedHookRenderings(t) {
		if filepath.Ext(rel) != ".py" {
			continue
		}
		checked++
		t.Run(rel, func(t *testing.T) {
			if out, err := checkPythonPublicDocstrings(t, python, text); err != nil {
				t.Fatalf("rendered public docstrings: %v\n%s", err, out)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no rendered Python hooks were checked")
	}
}

// Negative and boundary: comments are not docstrings; private definitions need none, while
// public classes, async functions and methods must be checked without executing hook code.
func TestEmittedPythonHooksHavePublicDocstrings_ParserCases(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH; Python docstring parser cases cannot run on this leg")
	}
	for _, tc := range []struct {
		name, text, missing string
	}{
		{"documented", "def public():\n    '''Documented.'''\n    raise RuntimeError()\n", ""},
		{"function", "def public():\n    # Only a comment.\n    pass\n", "public"},
		{"class", "class Public:\n    pass\n", "Public"},
		{"async", "async def public():\n    pass\n", "public"},
		{"method", "class Public:\n    '''Documented.'''\n    def method(self):\n        pass\n", "method"},
		{"private", "def _private():\n    pass\nclass _Private:\n    pass\n", ""},
		{"empty", "def public():\n    ''''''\n", "public"},
		{"invalid", "def public(\n", "SyntaxError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := checkPythonPublicDocstrings(t, python, tc.text)
			if err != nil {
				out += "\n" + err.Error()
			}
			if tc.missing == "" {
				if err != nil || out != "" {
					t.Fatalf("documented or private source refused: %v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(out, tc.missing) {
				t.Fatalf("want refusal naming %s, got %v\n%s", tc.missing, err, out)
			}
		})
	}
}
