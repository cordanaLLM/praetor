#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Praetor-emitted YAML passes yamllint's default rules in strict mode.

The YAML sibling of scripts/test_emitted_hook_lint.py, run beside it by make hooks-lint. It
reuses that gate's pinned-tool resolution and hermetic lint runs (LintCase): each file is
linted from a copy in an empty temporary directory with `yamllint --strict -d default`, so no
project or user configuration can loosen the check, and a missing or mismatched yamllint is a
skip locally and a failure where PRAETOR_HOOK_LINT_BIN names the pinned toolchain.

An adopter whose own hooks run yamllint over the whole tree used to fail on YAML Praetor writes
into it (BUG-782). This gate covers:

- the label taxonomy adoption writes to .config/labels.yaml (forge.DefaultLabelTaxonomy),
  linted as praetor's own copy, which internal/forge/labels_test.go holds byte-equal to it.

Each path is read from the Go constant that names it, so a moved file is followed without
editing this list.
"""

import unittest

from test_emitted_hook_lint import LintCase, ROOT, go_constant

# The Go constant naming each emitted YAML file linted here as praetor's own copy, and the file
# declaring it.
OWN_COPIES = (("internal/adopt/ruleset.go", "labelsFile"),)


def own_copies():
    """Return {repository path: text} of every emitted YAML file praetor carries byte-equal."""
    files = {}
    for relative, name in OWN_COPIES:
        path = go_constant(relative, name)
        files[path] = (ROOT / path).read_text(encoding="utf-8")
    return files


class EmittedYamlTest(LintCase):
    """Positive: every emitted YAML file passes; negative: a long line appended fails."""

    def test_yamllint_accepts_emitted_yaml(self):
        files = own_copies()
        self.assertEqual(len(files), len(OWN_COPIES))
        self.assertLintPasses("yamllint", files)

    def test_yamllint_rejects_a_long_line_in_emitted_yaml(self):
        for path, text in own_copies().items():
            long_line = "overlong: " + "x" * 80 + "\n"
            self.assertLintFails("yamllint", {path: text + long_line}, "line-length")


class SourceTest(unittest.TestCase):
    """The linted paths come from the Go constants and exist."""

    def test_own_copies_come_from_go_constants(self):
        files = own_copies()
        self.assertIn(".config/labels.yaml", files)
        for path, text in files.items():
            self.assertTrue(text.startswith("---\n"), f"{path} has no document start")


if __name__ == "__main__":
    unittest.main(verbosity=2)
