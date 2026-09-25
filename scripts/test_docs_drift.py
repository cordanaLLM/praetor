#!/usr/bin/env python3
"""Tests for the documentation-drift check."""

import contextlib
import io
import os
import sys
import unittest
from unittest import mock

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
            "tools/docsurface/catalog.mjs",
            "tools/docsurface/verify.mjs",
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

    def test_changed_files_uses_canonical_bounded_runner(self):
        """Git execution carries an explicit deadline and shared output ceiling."""
        calls = []

        def runner(args, **kwargs):
            calls.append((args, kwargs))
            return b"README.md\0docs/index.md\0"

        self.assertEqual(
            docs_drift.changed_files("a" * 40, "b" * 40, runner=runner),
            ["README.md", "docs/index.md"],
        )
        self.assertEqual(calls[0][0], [
            "git", "diff", "--name-only", "-z", f"{'a' * 40}..{'b' * 40}", "--",
        ])
        self.assertEqual(calls[0][1], {
            "timeout": docs_drift.GIT_DIFF_TIMEOUT_SECONDS,
            "max_output": docs_drift.MAX_DIFF_OUTPUT_BYTES,
        })

    def test_changed_files_preserves_timeout_overflow_and_start_failures(self):
        """Infrastructure failures cannot become an empty, passing diff."""
        for message in (
            "checkpoint command timed out",
            "checkpoint command output exceeded its byte limit",
            "git checkpoint process failed (FileNotFoundError)",
            "git exited 2; checkpoint unverified",
        ):
            with self.subTest(message=message):
                def runner(_args, **_kwargs):
                    raise docs_drift.HookError(message)

                escaped = message.replace("(", "\\(").replace(")", "\\)")
                with self.assertRaisesRegex(docs_drift.HookError, escaped):
                    docs_drift.changed_files("a" * 40, "b" * 40, runner=runner)

    def test_changed_files_rejects_malformed_inventory(self):
        """A partial final path is not silently accepted after a transport failure."""
        with self.assertRaisesRegex(docs_drift.HookError, "malformed"):
            docs_drift.changed_files(
                "a" * 40,
                "b" * 40,
                runner=lambda _args, **_kwargs: b"README.md",
            )

    def test_changed_files_file_count_boundary_fails_closed(self):
        """Exactly the path cap passes; one more never truncates to apparent success."""
        exact = b"".join(f"f-{index}\0".encode() for index in range(docs_drift.MAX_DIFF_FILES))
        over = exact + b"overflow\0"
        self.assertEqual(len(docs_drift.changed_files(
            "a" * 40, "b" * 40, runner=lambda _args, **_kwargs: exact,
        )), docs_drift.MAX_DIFF_FILES)
        with self.assertRaisesRegex(docs_drift.HookError, "exceeds"):
            docs_drift.changed_files(
                "a" * 40, "b" * 40, runner=lambda _args, **_kwargs: over,
            )

    def _main_with(self, environment):
        with mock.patch.dict(os.environ, environment, clear=True), \
                contextlib.redirect_stdout(io.StringIO()), \
                contextlib.redirect_stderr(io.StringIO()):
            return docs_drift.main()

    @mock.patch.object(docs_drift, "changed_files",
                       side_effect=docs_drift.HookError("checkpoint command timed out"))
    def test_main_reports_git_infrastructure_failure_as_error(self, _changed):
        """A bounded-runner failure exits 2 instead of passing as an empty diff."""
        self.assertEqual(self._main_with({"BASE_SHA": "a" * 40, "HEAD_SHA": "b" * 40}), 2)

    @mock.patch.object(docs_drift, "changed_files", return_value=["scripts/docs_drift.py"])
    def test_main_undocumented_surface_fails_without_opt_out(self, _changed):
        """A mapped surface without its guide still blocks through the bounded inventory."""
        self.assertEqual(self._main_with({"BASE_SHA": "a" * 40, "HEAD_SHA": "b" * 40}), 1)

    @mock.patch.object(docs_drift, "changed_files", return_value=["scripts/docs_drift.py"])
    def test_main_keeps_documented_pull_request_opt_out(self, _changed):
        """The reviewed 'no docs needed: <reason>' opt-out keeps its documented behavior."""
        environment = {
            "BASE_SHA": "a" * 40,
            "HEAD_SHA": "b" * 40,
            "PR_BODY": "no docs needed: test-only change",
        }
        self.assertEqual(self._main_with(environment), 0)

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
