#!/usr/bin/env python3
"""Hermetic regression tests for the DCO 1.1 sign-off gate.

Every case builds a throwaway repository under a temporary directory and runs the script
against it. Nothing outside that directory is read or written, and no remote is contacted.
"""

from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "dco_check.sh"
ZERO_SHA = "0" * 40
# Git localizes its diagnostics; pin the message catalogue so assertions on Git output
# hold on developer workstations with non-English locales.
BASE_ENV = {
    "LC_ALL": "C",
    "LANGUAGE": "C",
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_AUTHOR_NAME": "DCO Test",
    "GIT_AUTHOR_EMAIL": "dco-test@example.invalid",
    "GIT_COMMITTER_NAME": "DCO Test",
    "GIT_COMMITTER_EMAIL": "dco-test@example.invalid",
}


def git(*args: str, cwd: Path) -> str:
    """Run one Git command in the throwaway repository and return its stdout."""
    result = subprocess.run(
        ("git", *args),
        cwd=cwd,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env={**os.environ, **BASE_ENV},
        timeout=30,
    )
    return result.stdout.strip()


def commit(repository: Path, name: str, signed: bool) -> str:
    """Add one file and commit it, with or without the DCO trailer. Returns its SHA."""
    (repository / name).write_text(name, encoding="utf-8")
    git("add", "--", name, cwd=repository)
    message = f"add {name}"
    if signed:
        message += "\n\nSigned-off-by: DCO Test <dco-test@example.invalid>"
    git("commit", "--quiet", "-m", message, cwd=repository)
    return git("rev-parse", "HEAD", cwd=repository)


def seed(repository: Path) -> str:
    """Create the repository with one signed commit on main. Returns that commit's SHA."""
    repository.mkdir(parents=True)
    git("init", "--quiet", "--initial-branch=main", ".", cwd=repository)
    return commit(repository, "seed.txt", signed=True)


def run_check(
    repository: Path, event: str, base_ref: str, before: str, head: str
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ("bash", str(SCRIPT), event, base_ref, before, head),
        cwd=repository,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env={**os.environ, **BASE_ENV},
        timeout=60,
    )


@unittest.skipUnless(shutil.which("bash"), "the DCO gate is a bash script on a Linux runner")
class DcoCheckTests(unittest.TestCase):
    """Positive, negative and boundary coverage of the gate's public contract."""

    def setUp(self) -> None:
        self._directory = tempfile.TemporaryDirectory(prefix="praetor-dco-test-")
        self.addCleanup(self._directory.cleanup)
        self.repository = Path(self._directory.name) / "repository"
        self.first = seed(self.repository)

    # Positive: a push event inspects a non-empty range and accepts signed commits.

    def test_push_accepts_a_signed_range(self) -> None:
        head = commit(self.repository, "signed.txt", signed=True)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("all 1 commits", result.stdout)
        self.assertIn(f"{self.first}..{head}", result.stdout)

    def test_pull_request_accepts_a_signed_range(self) -> None:
        git("switch", "--quiet", "--create", "feature", cwd=self.repository)
        head = commit(self.repository, "feature.txt", signed=True)

        result = run_check(self.repository, "pull_request", "main", "", head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("all 1 commits", result.stdout)

    # Negative: an unsigned commit fails on both event shapes, and a bad invocation is refused.

    def test_push_rejects_an_unsigned_commit_reaching_the_branch_directly(self) -> None:
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(f"Commit {head} is missing 'Signed-off-by:'", result.stdout)
        self.assertIn("1 of 1 commits", result.stdout)

    def test_pull_request_rejects_an_unsigned_commit(self) -> None:
        git("switch", "--quiet", "--create", "feature", cwd=self.repository)
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "pull_request", "main", "", head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("1 of 1 commits", result.stdout)

    def test_a_missing_base_commit_is_reported_rather_than_passed(self) -> None:
        head = commit(self.repository, "signed.txt", signed=True)
        absent = "1" * 40

        result = run_check(self.repository, "push", "", absent, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("is not present in this checkout", result.stderr)

    def test_wrong_argument_count_is_refused(self) -> None:
        result = subprocess.run(
            ("bash", str(SCRIPT), "push"),
            cwd=self.repository,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env={**os.environ, **BASE_ENV},
            timeout=30,
        )

        self.assertEqual(result.returncode, 2)
        self.assertIn("usage:", result.stderr)

    def test_a_pull_request_without_a_base_ref_is_refused(self) -> None:
        result = run_check(self.repository, "pull_request", "", "", self.first)

        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("base ref", result.stderr)

    def test_an_empty_head_is_refused(self) -> None:
        result = run_check(self.repository, "push", "", self.first, "")

        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("head commit", result.stderr)

    # Boundary: the all-zero first push, an empty range, and a multi-commit range.

    def test_an_all_zero_before_is_skipped_with_a_printed_reason(self) -> None:
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "push", "", ZERO_SHA, head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("skipped", result.stdout)
        self.assertIn("all-zero 'before' commit", result.stdout)
        self.assertNotIn("missing 'Signed-off-by:'", result.stdout)

    def test_an_empty_before_is_treated_as_the_first_push(self) -> None:
        result = run_check(self.repository, "push", "", "", self.first)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("skipped", result.stdout)

    def test_an_empty_range_says_so_instead_of_claiming_commits_were_checked(self) -> None:
        result = run_check(self.repository, "push", "", self.first, self.first)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("introduces no commits", result.stdout)
        self.assertNotIn("all 0 commits", result.stdout)

    def test_one_unsigned_commit_among_several_fails_the_whole_range(self) -> None:
        commit(self.repository, "one.txt", signed=True)
        middle = commit(self.repository, "two.txt", signed=False)
        head = commit(self.repository, "three.txt", signed=True)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(f"Commit {middle} is missing", result.stdout)
        self.assertIn("1 of 3 commits", result.stdout)

    def test_a_bare_base_ref_is_used_when_no_origin_remote_exists(self) -> None:
        """A checkout without `origin` still resolves the base branch by its local name."""
        git("switch", "--quiet", "--create", "feature", cwd=self.repository)
        head = commit(self.repository, "feature.txt", signed=True)

        result = run_check(self.repository, "pull_request", "main", "", head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("main..", result.stdout)
        self.assertNotIn("origin/main..", result.stdout)


if __name__ == "__main__":
    unittest.main()
