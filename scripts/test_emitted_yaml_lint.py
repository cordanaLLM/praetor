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
  linted as praetor's own copy, which internal/forge/labels_test.go holds byte-equal to it;
- every profile and facet under .config/archetypes, which adoption copies byte for byte into
  the adopter's pinned catalog (internal/adopt/policy_catalog.go);
- the .standards.yaml adoption renders (renderManifest in internal/adopt), linted from
  internal/adopt/testdata/emitted/.standards.yaml, which
  TestEmittedManifestFixtureMatchesTheRendering keeps equal to the rendering.

Each path is read from the Go constant that names it, so a moved file is followed without
editing this list.
"""

import unittest
from pathlib import PurePosixPath

from test_emitted_hook_lint import LintCase, ROOT, go_constant

# The Go constant naming each emitted YAML file linted here as praetor's own copy, and the file
# declaring it.
OWN_COPIES = (("internal/adopt/ruleset.go", "labelsFile"),)
# The Go constant naming the archetype catalog directory, and the file declaring it.
CATALOG = ("internal/config/lockdigest.go", "archetypeDirName")
# The Go test constant naming each committed rendering, relative to its package directory,
# and the file declaring it. The rendering is linted under its adopted file name.
RENDERINGS = (("internal/adopt/manifest_render_test.go", "emittedManifestFixture"),)
# Bounds the catalog walk: a lock pins at most 256 profiles and 256 facets.
MAX_CATALOG_FILES = 512


def catalog_files():
    """Return {repository path: text} of every profile and facet adoption vendors."""
    directory = go_constant(*CATALOG)
    paths = sorted((ROOT / directory).rglob("*.yaml"))
    if not paths or len(paths) > MAX_CATALOG_FILES:
        raise AssertionError(f"{directory} holds {len(paths)} YAML files")
    return {
        p.relative_to(ROOT).as_posix(): p.read_text(encoding="utf-8") for p in paths
    }


def rendering_files():
    """Return {adopted path: text} of every committed rendering."""
    files = {}
    for relative, name in RENDERINGS:
        fixture = PurePosixPath(relative).parent / go_constant(relative, name)
        files[fixture.name] = (ROOT / fixture).read_text(encoding="utf-8")
    return files


def emitted_files():
    """Return {path: text} of every emitted YAML file this gate lints."""
    files = {}
    for relative, name in OWN_COPIES:
        path = go_constant(relative, name)
        files[path] = (ROOT / path).read_text(encoding="utf-8")
    files.update(catalog_files())
    files.update(rendering_files())
    return files


class EmittedYamlTest(LintCase):
    """Positive: every emitted YAML file passes; negative: a long line appended fails."""

    def test_yamllint_accepts_emitted_yaml(self):
        files = emitted_files()
        expected = len(OWN_COPIES) + len(catalog_files()) + len(RENDERINGS)
        self.assertEqual(len(files), expected)
        self.assertLintPasses("yamllint", files)

    def test_yamllint_rejects_a_long_line_in_each_emitted_file(self):
        long_line = "overlong: " + "x" * 80 + "\n"
        files = {path: text + long_line for path, text in emitted_files().items()}
        code, output = self.lint("yamllint", files)
        self.assertNotEqual(code, 0)
        self.assertEqual(output.count("(line-length)"), len(files), output)


class SourceTest(unittest.TestCase):
    """The linted paths come from the Go constants and exist."""

    def test_emitted_files_come_from_go_constants(self):
        files = emitted_files()
        for path in (
            ".config/labels.yaml",
            ".config/archetypes/framework.yaml",
            ".config/archetypes/facets/security-high.yaml",
            ".standards.yaml",
        ):
            self.assertIn(path, files)
        for path, text in files.items():
            self.assertTrue(text.startswith("---\n"), f"{path} has no document start")


if __name__ == "__main__":
    unittest.main(verbosity=2)
