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
