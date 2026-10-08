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
- the .standards.yaml adoption, init and onboarding render (config.RenderManifest) and the
  .github/actionlint.yaml adoption creates (internal/adopt/actionlint.go), linted from their
  committed renderings under test_emitted_hook_lint.RENDERED, which
  TestEmittedHookFixturesMatchTheRendering keeps equal to the renderings;
- every YAML body flavor apply scaffolds, and adoption through it: each .yml or .yaml
  template under templates/ (templates/embed.go), and each template internal/flavor checks
  with a YAML validator whatever its name (.clang-format and .clang-tidy, which clang reads as
  YAML; the Visual Studio editor target writes the same .clang-tidy), rendered by dropping its
  leading template comment, the one action most of these bodies carry. A body whose actions
  branch on repository facts (the Node CI job, the Dart analyzer config) is linted from its
  committed renderings instead, one per distinct body, which
  TestBranchingYAMLTemplateRenderingsAreCommitted (templates/branching_test.go) keeps equal to
  every rendering the template can produce. A body with any other action, or a branching one
  with no committed rendering, fails the gate instead of being linted as unrendered text.

Each path is read from the Go constant that names it, so a moved file is followed without
editing this list.
"""

import re
import tempfile
import unittest
from pathlib import Path

from test_emitted_hook_lint import RENDERED, LintCase, ROOT, go_constant

# The Go constant naming each emitted YAML file linted here as praetor's own copy, and the file
# declaring it.
OWN_COPIES = (("internal/adopt/ruleset.go", "labelsFile"),)
# The Go constant naming the archetype catalog directory, and the file declaring it.
CATALOG = ("internal/config/lockdigest.go", "archetypeDirName")
# The Go constant naming each YAML file whose rendering is committed under RENDERED, and the
# file declaring it. The rendering is linted under its adopted path.
RENDERINGS = (
    ("internal/config/repository_policy.go", "ManifestFileName"),
    ("internal/adopt/actionlint.go", "actionlintConfigFile"),
    ("internal/adopt/reuse_gate.go", "reuseWorkflowFile"),
)
# The Go constants naming the embedded template directory and its go:embed pattern, and the
# file declaring them.
TEMPLATES = ("templates/embed.go", "Directory", "Pattern")
# The Go constant naming the directory, relative to the template directory, that holds the
# committed renderings of each branching template under <template path without .tmpl>/, and
# the file declaring it.
BRANCH_RENDERINGS = ("templates/branching_test.go", "renderedRoot")
# The Go file declaring each flavor file's template (Source) and content check (Validator), and
# the validators that parse the body as YAML: a template they check is YAML whatever its name.
FLAVOR_DEFINITIONS = "internal/flavor/definitions.go"
YAML_VALIDATORS = frozenset(("validYAMLMapping", "validClangTidyConfig", "validWorkflow"))
# One flavor file: its Source, then its Validator later in the same literal, which may hold
# one nested literal (Search: &ConfigSearch{...}) in between.
FLAVOR_ITEM = re.compile(r'Source:\s*"([^"]+)"(?:[^{}]|\{[^{}]*\})*?Validator:\s*(\w+)')
# A leading template comment, <%- /* ... */ -%>, renders to nothing and trims the white space
# after it (text/template's "-%>").
TEMPLATE_COMMENT = re.compile(r"\A<%- /\*.*?\*/ -%>\s*", re.S)
# Bounds the catalog walk: a lock pins at most 256 profiles and 256 facets.
MAX_CATALOG_FILES = 512
# Bounds the template walk; templates/ ships a few dozen bodies.
MAX_TEMPLATE_FILES = 256


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
        path = go_constant(relative, name)
        files[path] = (ROOT / RENDERED / path).read_text(encoding="utf-8")
    return files


def render_template(text):
    """Return the body a YAML template renders to, or raise if it carries another action."""
    body = TEMPLATE_COMMENT.sub("", text, count=1)
    if "<%" in body:
        raise AssertionError("template carries an action this gate cannot render")
    return body


def template_bodies(text, renderings, key):
    """Return {path: body} of what one YAML template renders to: its body under key, or, when
    the directory renderings holds committed renderings of it, each of them under key/<name>.
    """
    if not renderings.is_dir():
        return {key: render_template(text)}
    paths = sorted(p for p in renderings.iterdir() if p.is_file())
    if not paths or len(paths) > MAX_TEMPLATE_FILES:
        raise AssertionError(f"{renderings} holds {len(paths)} renderings")
    return {f"{key}/{p.name}": p.read_text(encoding="utf-8") for p in paths}


def yaml_validated_templates():
    """Return the template paths, relative to the template directory, of every flavor file
    internal/flavor checks with a YAML validator."""
    items = FLAVOR_ITEM.findall((ROOT / FLAVOR_DEFINITIONS).read_text(encoding="utf-8"))
    if not items or len(items) > MAX_TEMPLATE_FILES:
        raise AssertionError(f"{FLAVOR_DEFINITIONS} declares {len(items)} flavor templates")
    return {source for source, validator in items if validator in YAML_VALIDATORS}


def template_files():
    """Return {template path without .tmpl: rendered body} of every YAML template, and
    {template path without .tmpl/name: body} of each committed rendering of a branching one."""
    source, directory_name, pattern_name = TEMPLATES
    directory = ROOT / go_constant(source, directory_name)
    rendered = directory / go_constant(*BRANCH_RENDERINGS)
    paths = sorted(directory.glob(go_constant(source, pattern_name)))
    if not paths or len(paths) > MAX_TEMPLATE_FILES:
        raise AssertionError(f"{directory} holds {len(paths)} templates")
    validated = yaml_validated_templates()
    files = {}
    for path in paths:
        target = path.name.removesuffix(".tmpl")
        yaml_body = path.relative_to(directory).as_posix() in validated
        if yaml_body or target.endswith((".yml", ".yaml")):
            relative = path.relative_to(ROOT).parent.as_posix()
            renderings = rendered / path.relative_to(directory).parent / target
            files.update(
                template_bodies(
                    path.read_text(encoding="utf-8"),
                    renderings,
                    f"{relative}/{target}",
                )
            )
    return files


def emitted_files():
    """Return {path: text} of every emitted YAML file this gate lints."""
    files = {}
    for relative, name in OWN_COPIES:
        path = go_constant(relative, name)
        files[path] = (ROOT / path).read_text(encoding="utf-8")
    files.update(catalog_files())
    files.update(rendering_files())
    files.update(template_files())
    return files


class EmittedYamlTest(LintCase):
    """Positive: every emitted YAML file passes; negative: a long line appended fails."""

    def test_yamllint_accepts_emitted_yaml(self):
        files = emitted_files()
        expected = (
            len(OWN_COPIES)
            + len(catalog_files())
            + len(RENDERINGS)
            + len(template_files())
        )
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
            "templates/go/ci-go.yml",
            "templates/go/.golangci.yml",
            "templates/native/.clang-format",
            "templates/native/.clang-tidy",
            "templates/node/ci-node.yml/npm.yml",
            "templates/node/ci-node.yml/pnpm.yml",
            "templates/flutter/analysis_options.yaml/none.yaml",
        ):
            self.assertIn(path, files)
        for path, text in files.items():
            self.assertTrue(text.startswith("---\n"), f"{path} has no document start")

    def test_every_yaml_validated_flavor_template_is_linted(self):
        validated = yaml_validated_templates()
        self.assertIn("native/.clang-tidy.tmpl", validated)
        self.assertIn("osimage/.yamllint.yml.tmpl", validated)
        self.assertNotIn("native/.gitleaks.toml.tmpl", validated)
        linted = {path.removeprefix("templates/") for path in template_files()}
        for template in validated:
            target = template.removesuffix(".tmpl")
            covered = {path for path in linted if path.startswith(target + "/")}
            self.assertTrue(target in linted or covered, f"{template} is not linted")

    def test_render_template_drops_only_the_leading_comment(self):
        note = "<%- /*\nmaintainer note\n*/ -%>\n"
        self.assertEqual(
            render_template(note + "---\nkey: value\n"), "---\nkey: value\n"
        )
        self.assertEqual(render_template("---\nkey: value\n"), "---\nkey: value\n")
        with self.assertRaises(AssertionError):
            render_template(note + "---\nowner: <% .Owner %>\n")

    def test_template_bodies_reads_committed_renderings(self):
        branching = "<% if .Node.Lint %>---\nlint: true\n<% end %>"
        with tempfile.TemporaryDirectory() as tmp:
            renderings = Path(tmp) / "ci.yml"
            # Negative: a branching body with no committed rendering fails.
            with self.assertRaises(AssertionError):
                template_bodies(branching, renderings, "t/ci.yml")
            # Boundary: an empty rendering directory fails too.
            renderings.mkdir()
            with self.assertRaises(AssertionError):
                template_bodies(branching, renderings, "t/ci.yml")
            # Positive: each committed rendering is linted under its own name.
            (renderings / "a.yml").write_text("---\na: 1\n", encoding="utf-8")
            (renderings / "b.yml").write_text("---\nb: 2\n", encoding="utf-8")
            self.assertEqual(
                template_bodies(branching, renderings, "t/ci.yml"),
                {"t/ci.yml/a.yml": "---\na: 1\n", "t/ci.yml/b.yml": "---\nb: 2\n"},
            )
            # Positive: a body without renderings is linted as itself.
            self.assertEqual(
                template_bodies("---\nkey: value\n", Path(tmp) / "x.yml", "t/x.yml"),
                {"t/x.yml": "---\nkey: value\n"},
            )


if __name__ == "__main__":
    unittest.main(verbosity=2)
