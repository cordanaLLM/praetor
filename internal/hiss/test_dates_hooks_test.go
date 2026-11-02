// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Exercise only the existing pure command selector, extracted with Python's AST.
// No hook, gate subprocess, environment helper or module initialization runs.
const dateHookSelection = `import ast
import sys
from pathlib import Path
source = Path(sys.argv[1]).read_text(encoding="utf-8")
node = next(n for n in ast.parse(source).body if isinstance(n, ast.FunctionDef) and n.name == "governance_commands")
namespace = {"CLI": ["standardsctl"], "context_changed": lambda _: False,
             "audit_scope": lambda *args: []}
exec(compile(ast.Module(body=[node], type_ignores=[]), "hook-selector", "exec"), namespace)
for name, source_changed, expected in [("scripts/test_dates.py", False, True),
                                      ("scripts/dates.py", False, True),
                                      ("docs/guide.md", False, False),
                                      ("a.go", True, True)]:
    commands = namespace["governance_commands"](Path(sys.argv[2]), [name], source_changed)
    prefix = [*namespace["CLI"], "audit"]
    audited = any(command[:len(prefix)] == prefix for command in commands)
    assert audited == expected, (name, audited, expected, commands)
`

// TestFixtureDatesPythonOnlyHookSelection pins fixture dates python only hook selection.
func TestFixtureDatesPythonOnlyHookSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", ".config", "lefthook", "scripts", "checks.py")
	if output, err := runDateHookSelection(t, source, root); err != nil {
		t.Fatalf("Python-only source check selection: %v\n%s", err, output)
	}
}

// TestFixtureDatesHookSelectorRejectsFlavorOnly pins fixture dates hook selector rejects flavor only.
func TestFixtureDatesHookSelectorRejectsFlavorOnly(t *testing.T) {
	source := filepath.Join("..", "..", ".config", "lefthook", "scripts", "checks.py")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Plant loss of the source audit while leaving flavor audit scheduled.
	mutated := strings.Replace(string(data), `[*CLI, "audit", *scope], `, "", 1)
	if mutated == string(data) {
		t.Fatal("source audit mutation did not apply")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"positive/missing-source-audit", mutated, true},
		{"negative/syntax-error", "def governance_commands(:\n", false},
		{"negative/missing-selector", "def other():\n    return []\n", false},
		{"boundary/unmutated", string(data), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checks.py")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			output, runErr := runDateHookSelection(t, path, root)
			if got := hookSelectorRejectedMissingAudit(runErr); got != tc.want {
				t.Fatalf("selector rejection = %v (want %v): %v\n%s", got, tc.want, runErr, output)
			}
		})
	}
}

// hookSelectorRejectedMissingAudit asserts hook rejection when audit steps are missing.
func hookSelectorRejectedMissingAudit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "AssertionError: ('scripts/test_dates.py', False, True")
}

// runDateHookSelection runs hook selector evaluation for date checks.
func runDateHookSelection(t *testing.T, source, root string) (string, error) {
	t.Helper()
	return util.RunCommand(t.Context(), "", testsupport.PythonInterpreter(t), "-c", dateHookSelection, source, root)
}

// TestFixtureDatesHookSelectionPythonWithoutPython3 pins fixture dates hook selection python without python3.
func TestFixtureDatesHookSelectionPythonWithoutPython3(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	bin := t.TempDir()
	if err := os.Symlink(interpreter, filepath.Join(bin, "python")); err != nil {
		t.Skipf("interpreter alias requires symlink support: %v", err)
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", ".config", "lefthook", "scripts", "checks.py")
	if output, err := runDateHookSelection(t, source, root); err != nil {
		t.Fatalf("python-only PATH: %v\n%s", err, output)
	}
}
