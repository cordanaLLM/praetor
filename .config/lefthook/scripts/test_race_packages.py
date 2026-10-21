# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Pre-push hands the existing affected-package calculation to the signed gate."""

import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import checks


class RacePackages(unittest.TestCase):
    def test_push_scope_reaches_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scope\n\ngo 1.27\n")
            self.seed_packages(root)
            cases = [(["apps/x/app.ts", "apps/x/App.svelte", "apps/x/app.css", "docs/new.md"], "none"),
                     (["a/a.go"], "example.test/scope/a,example.test/scope/b"),
                     (["a/testdata/input.json"], "example.test/scope/a,example.test/scope/b"),
                     (["a/guide.md"], "example.test/scope/a,example.test/scope/b"),
                     (["a/assets/deleted.json"], "example.test/scope/a,example.test/scope/b"),
                     (["go.mod"], "example.test/scope/a,example.test/scope/b,example.test/scope/c")]
            for names, expected in cases:
                with self.subTest(names=names):
                    self.assert_gate_scope(root, names, expected)

    @staticmethod
    def seed_packages(root):
        for name in ("a", "b", "c"):
            (root / name).mkdir()
        (root / "a/assets").mkdir()
        (root / "a/assets/remaining.json").write_text("{}")
        (root / "a/guide.md").write_text("embedded")
        (root / "a/a.go").write_text('package a\nimport _ "embed"\n'
                                     '//go:embed guide.md assets/*\nvar Data string\n')
        (root / "b/b.go").write_text('package b\nimport _ "example.test/scope/a"\n')
        (root / "c/c.go").write_text("package c\n")

    def assert_gate_scope(self, root, names, expected):
        actual_run = checks.run
        calls = []
        def process(argv, **kwargs):
            if argv[:2] == ["go", "list"]:
                return actual_run(argv, **kwargs)
            calls.append(argv)
            return json.dumps({"timeout_seconds": 1500}).encode()
        governance = [[*checks.CLI, "audit"]]
        with patch("checks.governance_commands", return_value=governance), \
                patch("checks.parallel"), patch("checks.local_package_patterns", return_value=["./a"]), \
                patch("checks.golangci_lint_command", return_value=["lint"]), \
                patch("checks.run", side_effect=process):
            self.assertTrue(checks.source_checks(root, names))
        launched = next(cmd for cmd in calls if cmd[3:5] == ["gate", "run"])
        self.assertIn("--race-packages=" + expected, launched)
        verify = next(cmd for cmd in calls if cmd[3:5] == ["gate", "verify"])
        self.assertEqual(verify, [*checks.CLI, "gate", "verify", "--path=."])


if __name__ == "__main__":
    unittest.main()
