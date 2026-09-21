#!/usr/bin/env python3
"""Tests for the documentation-drift check."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import docs_drift  # noqa: E402


class DocsDrift(unittest.TestCase):
    """Three dimensions over the surface map."""

    def test_positive_surface_without_docs_is_reported(self):
        """A changed archetype with no guide edit is the case the gate exists for."""
        found = docs_drift.violations([".config/archetypes/os-image.yaml"])
        self.assertEqual(len(found), 1, found)
        self.assertIn("archetype catalog", found[0])

    def test_positive_surface_with_its_docs_passes(self):
        """The same change with its guide is clean."""
        self.assertEqual(docs_drift.violations([
            ".config/archetypes/os-image.yaml",
            "docs/guides/archetype-authoring.md",
        ]), [])

    def test_negative_internal_change_is_not_accused(self):
        """A change touching no listed surface needs no documentation.

        This is what keeps the check worth reading. A gate that fires on every refactor is
        one people learn to ignore, and then it protects nothing.
        """
        self.assertEqual(docs_drift.violations([
            "internal/worktree/worktree.go",
            "internal/util/util.go",
            "cmd/standardsctl/main.go",
            "internal/adopt/adopt_test.go",
        ]), [])

    def test_negative_adr_does_not_satisfy_a_surface(self):
        """An ADR records a decision; it does not describe a surface.

        Accepting one would let 'I wrote it down somewhere' satisfy 'the guide still matches
        the behaviour', which is the drift this check exists to catch.
        """
        found = docs_drift.violations([
            ".config/archetypes/web-package.yaml",
            "docs/adr/0200-some-decision.md",
        ])
        self.assertEqual(len(found), 1, found)

    def test_boundary_unrelated_doc_does_not_satisfy_the_surface(self):
        """Touching some other guide is not touching the one that describes this surface."""
        found = docs_drift.violations([
            "internal/config/effective_load.go",
            "docs/guides/onboarding.md",
        ])
        self.assertEqual(len(found), 1, found)
        self.assertIn("effective policy", found[0])

    def test_text_register_pair(self):
        """Both files of the register policy map to its guide, and only to it."""
        for surface in ("internal/config/register.go", "internal/config/register_render.go"):
            found = docs_drift.violations([surface, "docs/guides/effective-policy.md"])
            self.assertEqual(len(found), 1, surface)
            self.assertIn("text register policy", found[0])
            self.assertEqual(docs_drift.violations([surface, "docs/guides/text-register.md"]), [])
        # A test file of the same package is not a user-discoverable surface.
        self.assertEqual(docs_drift.violations(["internal/config/register_test.go"]), [])

    def test_readme_renderer_and_audit_share_one_documented_contract(self):
        """Every producer or verifier of the managed README block maps to its guide."""
        surfaces = [
            "internal/readmegovernance/readme.go",
            "internal/adopt/governance.go",
            "cmd/standardsctl/audit_readme.go",
        ]
        for surface in surfaces:
            found = docs_drift.violations([surface])
            self.assertEqual(len(found), 1, surface)
            self.assertIn("managed README governance contract", found[0])
            self.assertEqual(docs_drift.violations([
                surface, "docs/guides/adoption-verification.md",
            ]), [])

    def test_markdown_gate_surfaces_share_one_documented_contract(self):
        """The locked runner, emitter, selector, and workflow map to their operator guide."""
        surfaces = [
            "tools/markdownlint/verify.mjs",
            "internal/adopt/documentation.go",
            "internal/cifilter/filter.go",
            ".github/workflows/praetor-docs.yml",
        ]
        for surface in surfaces:
            found = docs_drift.violations([surface])
            self.assertEqual(len(found), 1, surface)
            self.assertIn("Markdown documentation governance", found[0])
            self.assertEqual(docs_drift.violations([
                surface, "docs/guides/documentation-governance.md",
            ]), [])

    def test_boundary_several_surfaces_report_separately(self):
        """Two unrelated surfaces in one change produce two messages, not one."""
        self.assertEqual(len(docs_drift.violations([
            ".config/archetypes/os-image.yaml",
            "lefthook.yml",
        ])), 2)

    def test_boundary_empty_diff_is_clean(self):
        self.assertEqual(docs_drift.violations([]), [])

    def test_every_mapped_docs_target_exists(self):
        """A surface mapped to a document that does not exist can never be satisfied.

        The map would then fail every change to that surface with no way to clear it, which is
        the shape of a declared requirement nothing can meet.
        """
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        for _, docs_pat, label in docs_drift.SURFACE_MAP:
            literal = docs_pat.strip("^$").replace("\\.", ".")
            if any(ch in literal for ch in "()|["):
                continue
            path = os.path.join(root, literal)
            exists = os.path.isfile(path) or os.path.isdir(path.rstrip("/"))
            self.assertTrue(exists, f"{label}: maps to {literal}, which does not exist")


if __name__ == "__main__":
    unittest.main()
