// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"testing"

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
    audited = any("audit" in command for command in commands)
    assert audited == expected, (name, audited, expected, commands)
`

func TestFixtureDatesPythonOnlyHookSelection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join("..", "..", ".config", "lefthook", "scripts", "checks.py")
	if output, err := util.RunCommand(t.Context(), "", "python3", "-c", dateHookSelection, source, root); err != nil {
		t.Fatalf("Python-only source check selection: %v\n%s", err, output)
	}
}
