#!/usr/bin/env python3
"""Hermetic regression tests for the DCO 1.1 sign-off gate.

Every case builds a throwaway repository under a temporary directory and runs the script
against it. Nothing outside that directory is read or written, and no remote is contacted.

ComplianceWiringTests is the exception: it reads .github/workflows/compliance.yml and pins
the step that invokes the script. The script's own cases drive it with hand-written
arguments, so without that pin a re-added event condition or a swapped pair of adjacent
arguments would leave the whole suite green while every push run skipped the gate (#292).
"""

from __future__ import annotations

import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "dco_check.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "compliance.yml"
ZERO_SHA = "0" * 40
DEFAULT_BRANCH = "main"
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
# A commit message larger than one pipe buffer. The gate used to pipe the message into
# `grep -q` under `set -o pipefail`: grep exits at the first match, git takes SIGPIPE, and
# the pipeline status reported a correctly signed commit as unsigned once the message no
# longer fitted the buffer. 64 KiB is the Linux default, so a quarter of a megabyte is
# well past the threshold and far below the largest message already on main.
LARGE_MESSAGE_BYTES = 256 * 1024


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


def commit(repository: Path, name: str, signed: bool, body: str = "") -> str:
    """Add one file and commit it, with or without the DCO trailer. Returns its SHA."""
    (repository / name).write_text(name, encoding="utf-8")
    git("add", "--", name, cwd=repository)
    message = f"add {name}"
    if signed:
        message += "\n\nSigned-off-by: DCO Test <dco-test@example.invalid>"
    if body:
        message += "\n\n" + body
    message_file = repository.parent / "commit-message.txt"
    message_file.write_text(message, encoding="utf-8")
    git("commit", "--quiet", "--file", str(message_file), cwd=repository)
    return git("rev-parse", "HEAD", cwd=repository)


def seed(repository: Path) -> str:
    """Create the repository with one signed commit on main. Returns that commit's SHA."""
    repository.mkdir(parents=True)
    git("init", "--quiet", f"--initial-branch={DEFAULT_BRANCH}", ".", cwd=repository)
    return commit(repository, "seed.txt", signed=True)


def run_check(
    repository: Path,
    event: str,
    base_ref: str,
    before: str,
    head: str,
    default_branch: str = DEFAULT_BRANCH,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ("bash", str(SCRIPT), event, base_ref, before, head, default_branch),
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

    def branch(self, name: str) -> None:
        git("switch", "--quiet", "--create", name, cwd=self.repository)

    # Positive: a push event inspects a non-empty range and accepts signed commits.

    def test_push_accepts_a_signed_range(self) -> None:
        head = commit(self.repository, "signed.txt", signed=True)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("all 1 commits", result.stdout)
        self.assertIn(f"{self.first}..{head}", result.stdout)

    def test_pull_request_accepts_a_signed_range(self) -> None:
        self.branch("feature")
        head = commit(self.repository, "feature.txt", signed=True)

        result = run_check(self.repository, "pull_request", DEFAULT_BRANCH, "", head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("all 1 commits", result.stdout)

    def test_a_branch_creation_push_accepts_signed_commits(self) -> None:
        """The all-zero 'before' resolves to the default branch, not to a skip."""
        self.branch("lts-2.0")
        head = commit(self.repository, "lts.txt", signed=True)

        result = run_check(self.repository, "push", "", ZERO_SHA, head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("all 1 commits", result.stdout)
        self.assertIn(f"{DEFAULT_BRANCH}..{head}", result.stdout)

    # Negative: an unsigned commit fails on both event shapes, and a bad invocation is refused.

    def test_push_rejects_an_unsigned_commit_reaching_the_branch_directly(self) -> None:
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(f"Commit {head} is missing 'Signed-off-by:'", result.stdout)
        self.assertIn("1 of 1 commits", result.stdout)

    def test_pull_request_rejects_an_unsigned_commit(self) -> None:
        self.branch("feature")
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "pull_request", DEFAULT_BRANCH, "", head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("1 of 1 commits", result.stdout)

    def test_a_branch_creation_push_rejects_an_unsigned_commit(self) -> None:
        """The hole #292 was filed for, on the half that creates the branch.

        An `lts-*` branch is created by exactly this push, so a skip here would land
        unsigned commits on a protected branch with the gate green and no pull request
        ever inspecting them.
        """
        self.branch("lts-2.0")
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "push", "", ZERO_SHA, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(f"Commit {head} is missing 'Signed-off-by:'", result.stdout)
        self.assertNotIn("skipped", result.stdout)

    def test_a_missing_base_commit_is_reported_rather_than_passed(self) -> None:
        head = commit(self.repository, "signed.txt", signed=True)
        absent = "1" * 40

        result = run_check(self.repository, "push", "", absent, head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("is not present in this checkout", result.stderr)

    def test_a_missing_head_commit_is_reported_rather_than_passed(self) -> None:
        """The head side of the range is verified like the base side.

        `git rev-list` used to run in a process substitution, whose exit status escapes
        both `set -e` and `pipefail`: an unreadable range left the loop with nothing to
        read and the gate reporting "introduces no commits" with exit 0.
        """
        absent = "2" * 40

        result = run_check(self.repository, "push", "", self.first, absent)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("head commit", result.stderr)
        self.assertIn("is not present in this checkout", result.stderr)
        self.assertNotIn("introduces no commits", result.stdout)

    def test_an_unreadable_range_fails_instead_of_reporting_a_clean_one(self) -> None:
        """The enumeration's exit status is checked, whatever made it fail.

        Both endpoints exist here, so only a stub that refuses `rev-list` reaches the
        branch. A process substitution's status escapes `set -e` and `pipefail`, which
        left an unread range indistinguishable from an empty one.
        """
        head = commit(self.repository, "signed.txt", signed=True)
        stub = Path(self._directory.name) / "stub-bin"
        stub.mkdir()
        real = shutil.which("git")
        self.assertIsNotNone(real, "git is required to run these cases")
        (stub / "git").write_text(
            "#!/usr/bin/env bash\n"
            'for argument in "$@"; do\n'
            '  if [[ "$argument" == "rev-list" ]]; then\n'
            '    echo "stub: refusing to enumerate" >&2\n'
            "    exit 128\n"
            "  fi\n"
            "done\n"
            f'exec {real} "$@"\n',
            encoding="utf-8",
        )
        (stub / "git").chmod(0o755)

        result = subprocess.run(
            ("bash", str(SCRIPT), "push", "", self.first, head, DEFAULT_BRANCH),
            cwd=self.repository,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env={**os.environ, **BASE_ENV, "PATH": f"{stub}{os.pathsep}{os.environ['PATH']}"},
            timeout=60,
        )

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("could not be enumerated", result.stderr)
        self.assertNotIn("introduces no commits", result.stdout)

    def test_a_missing_default_branch_is_reported_rather_than_passed(self) -> None:
        self.branch("lts-2.0")
        head = commit(self.repository, "lts.txt", signed=True)

        result = run_check(self.repository, "push", "", ZERO_SHA, head, "absent-branch")

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("is not present in this checkout", result.stderr)

    def test_a_branch_creation_push_without_a_default_branch_is_refused(self) -> None:
        self.branch("lts-2.0")
        head = commit(self.repository, "lts.txt", signed=True)

        result = run_check(self.repository, "push", "", ZERO_SHA, head, "")

        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("names no default branch", result.stderr)
        self.assertNotIn("skipped", result.stdout)

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

    def test_the_previous_four_argument_invocation_is_refused(self) -> None:
        """The contract is five arguments; the four-argument form must not run silently."""
        result = subprocess.run(
            ("bash", str(SCRIPT), "push", "", ZERO_SHA, self.first),
            cwd=self.repository,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env={**os.environ, **BASE_ENV},
            timeout=30,
        )

        self.assertEqual(result.returncode, 2)
        self.assertIn("DEFAULT_BRANCH", result.stderr)

    def test_a_pull_request_without_a_base_ref_is_refused(self) -> None:
        result = run_check(self.repository, "pull_request", "", "", self.first)

        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("base ref", result.stderr)

    def test_an_empty_head_is_refused(self) -> None:
        result = run_check(self.repository, "push", "", self.first, "")

        self.assertEqual(result.returncode, 2, result.stdout)
        self.assertIn("head commit", result.stderr)

    # Boundary: the all-zero first push, an empty before, an empty range, a multi-commit
    # range and a commit message larger than one pipe buffer.

    def test_an_empty_before_is_measured_against_the_default_branch(self) -> None:
        """An empty 'before' is a misconfiguration, not a clean run.

        Swapping the two adjacent string arguments the workflow passes produces exactly
        this shape. It used to print the all-zero skip reason and exit 0, so the log did
        not reveal the misconfiguration either.
        """
        self.branch("lts-2.0")
        head = commit(self.repository, "unsigned.txt", signed=False)

        result = run_check(self.repository, "push", "", "", head)

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn(f"Commit {head} is missing 'Signed-off-by:'", result.stdout)

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

    def test_a_signed_commit_with_a_message_larger_than_a_pipe_buffer_is_accepted(self) -> None:
        """A squash merge concatenates every branch message; the trailer sits near the top."""
        filler = ("y" * 200 + "\n") * (LARGE_MESSAGE_BYTES // 201)
        head = commit(self.repository, "squashed.txt", signed=True, body=filler)

        result = run_check(self.repository, "push", "", self.first, head)

        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("all 1 commits", result.stdout)
        self.assertNotIn("missing 'Signed-off-by:'", result.stdout)

    def test_a_bare_base_ref_is_used_when_no_origin_remote_exists(self) -> None:
        """A checkout without `origin` still resolves the base branch by its local name."""
        self.branch("feature")
        head = commit(self.repository, "feature.txt", signed=True)

        result = run_check(self.repository, "pull_request", DEFAULT_BRANCH, "", head)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"{DEFAULT_BRANCH}..", result.stdout)
        self.assertNotIn(f"origin/{DEFAULT_BRANCH}..", result.stdout)


class ComplianceWiringTests(unittest.TestCase):
    """The workflow step that invokes the gate, pinned so a silent skip fails a test.

    The cases above drive the script directly, so they hold whatever the workflow passes.
    Both ways of re-opening #292 live here instead: gating the step on the pull request
    event again, and reordering the positional payload values.
    """

    #: Declaration order is the argument order the script's usage line documents.
    EXPECTED_ENV = (
        ("DCO_EVENT_NAME", "${{ github.event_name }}"),
        ("DCO_BASE_REF", "${{ github.base_ref }}"),
        ("DCO_BEFORE_SHA", "${{ github.event.before }}"),
        ("DCO_HEAD_SHA", "${{ github.event.pull_request.head.sha || github.sha }}"),
        ("DCO_DEFAULT_BRANCH", "${{ github.event.repository.default_branch }}"),
    )

    def setUp(self) -> None:
        self.step = self.dco_step()

    def dco_step(self) -> str:
        """Return the text of the step that runs the gate, without its trailing siblings."""
        document = WORKFLOW.read_text(encoding="utf-8")
        marker = "      - name: Verify DCO 1.1 Sign-Off on Commits\n"
        self.assertIn(marker, document, "the DCO step is no longer named as expected")
        rest = document.split(marker, 1)[1]
        end = re.search(r"^      - name: ", rest, flags=re.MULTILINE)
        return rest[: end.start()] if end else rest

    def test_the_step_carries_no_event_condition(self) -> None:
        """An `if:` here is how #292 skipped every push run while reporting green."""
        self.assertNotRegex(self.step, r"^\s+if:", "the DCO step must run on every event")

    def test_every_payload_value_is_passed_under_its_own_name(self) -> None:
        for name, expression in self.EXPECTED_ENV:
            with self.subTest(variable=name):
                self.assertIn(f"{name}: {expression}\n", self.step)

    def test_the_arguments_are_passed_in_the_documented_order(self) -> None:
        """Two adjacent plain strings swap without any case above noticing."""
        arguments = re.findall(r'"\$(DCO_[A-Z_]+)"', self.step)

        self.assertEqual(arguments, [name for name, _ in self.EXPECTED_ENV])

    def test_the_usage_line_documents_exactly_those_arguments(self) -> None:
        """The script and the step agree on the contract, in the same order."""
        usage = re.search(r'usage: \$0 ([A-Z_ ]+)"', SCRIPT.read_text(encoding="utf-8"))
        self.assertIsNotNone(usage, "the script no longer documents a usage line")

        documented = usage.group(1).split()

        self.assertEqual(documented,
                         [name.removeprefix("DCO_") for name, _ in self.EXPECTED_ENV])


if __name__ == "__main__":
    unittest.main()
