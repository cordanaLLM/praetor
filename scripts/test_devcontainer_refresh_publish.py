#!/usr/bin/env python3
"""Hermetic regression tests for the devcontainer bundle refresh publisher (#338).

Every case builds a throwaway repository and a bare `origin` under a temporary directory and runs
scripts/devcontainer_refresh_publish.sh in it. `gh` is a stub on PATH that records its arguments
and the token it was handed and answers from GH_STUB_* settings, so no forge is contacted.

RefreshWiringTests reads .github/workflows/devcontainer-refresh.yml and pins the step that runs
the script. Without the token-less path, every scheduled run in a repository without the
PRAETOR_PR_TOKEN secret pushed the branch and then failed; a GITHUB_TOKEN fallback in the step
would hide the missing secret from the script and bring that failure back.
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
SCRIPT = ROOT / "scripts" / "devcontainer_refresh_publish.sh"
WORKFLOW = ROOT / ".github" / "workflows" / "devcontainer-refresh.yml"
BASE_BRANCH = "main"
REFRESH_BRANCH = "chore/devcontainer-bundle-refresh"
REPOSITORY = "example/praetor"
SERVER = "https://forge.example.invalid"
PUSH_TOKEN = "push-token-value-0001"
PR_TOKEN = "pr-token-value-0002"
COMPARE_URL = f"{SERVER}/{REPOSITORY}/compare/{BASE_BRANCH}...{REFRESH_BRANCH}?expand=1"
REPORT = "[DRIFT WITHIN BOUNDS] 136 commits and 5.3 days behind\n"
# The PRAETOR_PR_TOKEN secret as the step hands it over. Spelled as one mapping so no call site
# reads like a credential assignment to the secret scanner (make secrets).
WITH_PR_PAT = {"PR_TOKEN": PR_TOKEN}
# Git localizes its diagnostics, and the developer's global configuration may sign commits;
# pin both so the throwaway commits behave the same on every workstation.
BASE_ENV = {
    "LC_ALL": "C",
    "LANGUAGE": "C",
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_CONFIG_NOSYSTEM": "1",
    "GIT_AUTHOR_NAME": "Refresh Test",
    "GIT_AUTHOR_EMAIL": "refresh-test@example.invalid",
    "GIT_COMMITTER_NAME": "Refresh Test",
    "GIT_COMMITTER_EMAIL": "refresh-test@example.invalid",
}
GH_STUB = """#!/bin/sh
printf '%s\\n' "$*" >>"$GH_STUB_LOG"
printf '%s\\n' "${GH_TOKEN:-}" >>"$GH_STUB_TOKENS"
if [ "$2" = "${GH_STUB_FAIL:-}" ]; then
  echo "gh stub: refusing $1 $2" >&2
  exit 1
fi
if [ "$2" = list ]; then
  printf '%s' "${GH_STUB_OPEN:-}"
fi
"""


def git(*args: str, cwd: Path) -> str:
    """Run one Git command in a throwaway repository and return its stdout."""
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


def gnu_timeout() -> bool:
    """Report whether the `timeout` on PATH is GNU coreutils' (Windows ships an unrelated one)."""
    path = shutil.which("timeout")
    if not path:
        return False
    try:
        result = subprocess.run(
            (path, "--version"),
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=10,
        )
    except (OSError, subprocess.SubprocessError):
        return False
    return "GNU coreutils" in result.stdout


# The publisher is a bash script for a Linux runner that bounds every forge call with GNU
# timeout. Windows has no POSIX shell and its timeout.exe is a different program, and stock
# macOS has no `timeout`, so the suite says so there rather than reporting a pass it did not
# earn (HISS-21).
@unittest.skipUnless(
    shutil.which("bash") and gnu_timeout(),
    "the publisher is a bash script for a Linux runner and needs GNU timeout",
)
class RefreshPublishTests(unittest.TestCase):
    """Positive, negative and boundary coverage of the publisher's contract."""

    def setUp(self) -> None:
        self._directory = tempfile.TemporaryDirectory(prefix="praetor-refresh-test-")
        self.addCleanup(self._directory.cleanup)
        self.root = Path(self._directory.name)
        self.origin = self.root / "origin.git"
        self.work = self.root / "work"
        self.report = self.root / "freshness.txt"
        self.report.write_text(REPORT, encoding="utf-8")
        self.summary = self.root / "summary.md"
        self.gh_log = self.root / "gh.log"
        self.gh_tokens = self.root / "gh-tokens.log"
        self.stub_bin = self.root / "bin"
        self.stub_bin.mkdir()
        stub = self.stub_bin / "gh"
        stub.write_text(GH_STUB, encoding="utf-8")
        stub.chmod(0o755)
        self.seed()

    def seed(self) -> None:
        """Create origin with one commit on main that carries a bundle, and clone it."""
        git("init", "--quiet", "--bare", f"--initial-branch={BASE_BRANCH}", str(self.origin),
            cwd=self.root)
        git("clone", "--quiet", str(self.origin), str(self.work), cwd=self.root)
        bundle = self.work / ".devcontainer"
        bundle.mkdir()
        (bundle / "devcontainer.json").write_text("{}\n", encoding="utf-8")
        (bundle / "praetor-source.000.b64").write_text("old\n", encoding="utf-8")
        (self.work / "README.md").write_text("readme\n", encoding="utf-8")
        git("add", "--all", cwd=self.work)
        git("commit", "--quiet", "-m", "seed", cwd=self.work)
        git("push", "--quiet", "origin", f"HEAD:refs/heads/{BASE_BRANCH}", cwd=self.work)

    def regenerate(self) -> None:
        (self.work / ".devcontainer" / "praetor-source.000.b64").write_text(
            "new\n", encoding="utf-8")

    def run_publish(self, *args: str, **settings: str) -> subprocess.CompletedProcess[str]:
        arguments = args if args else (BASE_BRANCH, REFRESH_BRANCH, str(self.report))
        env = {
            **os.environ,
            **BASE_ENV,
            "PATH": f"{self.stub_bin}{os.pathsep}{os.environ.get('PATH', '')}",
            "GITHUB_REPOSITORY": REPOSITORY,
            "GITHUB_SERVER_URL": SERVER,
            "GITHUB_STEP_SUMMARY": str(self.summary),
            "PUSH_TOKEN": PUSH_TOKEN,
            "PR_TOKEN": "",
            "GH_STUB_LOG": str(self.gh_log),
            "GH_STUB_TOKENS": str(self.gh_tokens),
            **settings,
        }
        return subprocess.run(
            ("bash", str(SCRIPT), *arguments),
            cwd=self.work,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            timeout=120,
        )

    def gh_calls(self) -> list[str]:
        if not self.gh_log.exists():
            return []
        return self.gh_log.read_text(encoding="utf-8").splitlines()

    def gh_tokens_used(self) -> set[str]:
        if not self.gh_tokens.exists():
            return set()
        return set(self.gh_tokens.read_text(encoding="utf-8").splitlines())

    def refresh_tip(self) -> str:
        """Return the refresh branch tip on origin, or an empty string when it was not pushed."""
        return git("for-each-ref", "--format=%(objectname)", f"refs/heads/{REFRESH_BRANCH}",
                   cwd=self.origin)

    def output(self, result: subprocess.CompletedProcess[str]) -> str:
        return result.stdout + result.stderr

    # Positive: without PRAETOR_PR_TOKEN the run pushes, warns with the link and stays green.

    def test_without_a_pr_token_the_branch_is_pushed_and_linked(self) -> None:
        self.regenerate()

        result = self.run_publish()

        self.assertEqual(result.returncode, 0, self.output(result))
        self.assertIn("::warning::devcontainer-refresh: pushed " + REFRESH_BRANCH, result.stdout)
        self.assertIn(COMPARE_URL, result.stdout)
        self.assertNotEqual(self.refresh_tip(), "")
        calls = self.gh_calls()
        self.assertEqual(len(calls), 1, calls)
        self.assertTrue(calls[0].startswith("pr list "), calls)
        self.assertEqual(self.gh_tokens_used(), {PUSH_TOKEN})
        summary = self.summary.read_text(encoding="utf-8")
        self.assertIn(COMPARE_URL, summary)
        self.assertIn(REPORT.strip(), summary)

    def test_the_pushed_commit_is_signed_off_and_names_its_base(self) -> None:
        self.regenerate()
        base = git("rev-parse", "HEAD", cwd=self.work)

        self.run_publish()

        message = git("log", "-1", "--format=%B", self.refresh_tip(), cwd=self.origin)
        self.assertIn("Signed-off-by: Refresh Test <refresh-test@example.invalid>", message)
        self.assertIn(f"Regenerated from {base}", message)
        self.assertEqual(git("rev-parse", f"{self.refresh_tip()}~1", cwd=self.origin), base)

    def test_without_a_pr_token_an_open_pull_request_is_left_alone(self) -> None:
        """A GITHUB_TOKEN push would move the open pull request to a head no check ran on."""
        self.regenerate()

        result = self.run_publish(GH_STUB_OPEN="42")

        self.assertEqual(result.returncode, 0, self.output(result))
        self.assertIn("::warning::devcontainer-refresh: pull request #42", result.stdout)
        self.assertIn("Land #42", result.stdout)
        self.assertEqual(self.refresh_tip(), "")
        self.assertEqual([call.split()[:2] for call in self.gh_calls()], [["pr", "list"]])

    # Positive: with PRAETOR_PR_TOKEN the run opens or updates the pull request with it.

    def test_with_a_pr_token_the_pull_request_is_opened(self) -> None:
        self.regenerate()

        result = self.run_publish(**WITH_PR_PAT)

        self.assertEqual(result.returncode, 0, self.output(result))
        self.assertNotIn("::warning::", result.stdout)
        self.assertNotEqual(self.refresh_tip(), "")
        create = [call for call in self.gh_calls() if call.startswith("pr create ")]
        self.assertEqual(len(create), 1, self.gh_calls())
        self.assertIn(f"--base {BASE_BRANCH} --head {REFRESH_BRANCH}", create[0])
        self.assertEqual(self.gh_tokens_used(), {PR_TOKEN})

    def test_with_a_pr_token_the_open_pull_request_is_updated(self) -> None:
        self.regenerate()

        result = self.run_publish(**WITH_PR_PAT, GH_STUB_OPEN="7")

        self.assertEqual(result.returncode, 0, self.output(result))
        self.assertIn("updated pull request #7", result.stdout)
        verbs = [call.split()[:3] for call in self.gh_calls()]
        self.assertEqual(verbs, [["pr", "list", "--repo"], ["pr", "edit", "7"]])

    # Negative: a configured token that cannot publish, and every unreadable state, fail.

    def test_with_a_pr_token_a_refused_pull_request_fails_naming_the_branch(self) -> None:
        self.regenerate()

        result = self.run_publish(**WITH_PR_PAT, GH_STUB_FAIL="create")

        self.assertEqual(result.returncode, 1, self.output(result))
        self.assertIn(f"::error::devcontainer-refresh: pushed {REFRESH_BRANCH} but could not open",
                      result.stdout)

    def test_with_a_pr_token_a_refused_update_fails(self) -> None:
        self.regenerate()

        result = self.run_publish(**WITH_PR_PAT, GH_STUB_OPEN="7", GH_STUB_FAIL="edit")

        self.assertEqual(result.returncode, 1, self.output(result))
        self.assertIn("could not update pull request #7", result.stdout)

    def test_a_failed_listing_fails_before_anything_is_pushed(self) -> None:
        """An unread listing looks exactly like no open pull request; it must not push over one."""
        self.regenerate()

        result = self.run_publish(GH_STUB_FAIL="list")

        self.assertEqual(result.returncode, 1, self.output(result))
        self.assertIn("::error::devcontainer-refresh: could not list", result.stdout)
        self.assertEqual(self.refresh_tip(), "")

    def test_a_listing_that_is_not_a_number_is_refused(self) -> None:
        self.regenerate()

        result = self.run_publish(GH_STUB_OPEN="#42")

        self.assertEqual(result.returncode, 1, self.output(result))
        self.assertIn("not a number", result.stdout)
        self.assertEqual(self.refresh_tip(), "")

    def test_a_failed_push_fails_without_a_pr_token(self) -> None:
        self.regenerate()
        git("remote", "set-url", "origin", str(self.root / "missing.git"), cwd=self.work)

        result = self.run_publish()

        self.assertEqual(result.returncode, 1, self.output(result))
        self.assertIn("could not push", result.stdout)
        self.assertNotIn("::warning::", result.stdout)

    def test_a_missing_push_token_is_refused(self) -> None:
        self.regenerate()

        result = self.run_publish(PUSH_TOKEN="")

        self.assertEqual(result.returncode, 2, self.output(result))
        self.assertIn("PUSH_TOKEN is empty", result.stderr)
        self.assertEqual(self.gh_calls(), [])

    def test_a_missing_report_is_refused(self) -> None:
        self.regenerate()

        result = self.run_publish(BASE_BRANCH, REFRESH_BRANCH, str(self.root / "absent.txt"))

        self.assertEqual(result.returncode, 2, self.output(result))
        self.assertIn("does not exist", result.stderr)

    def test_wrong_argument_count_is_refused(self) -> None:
        for arguments in ((BASE_BRANCH, REFRESH_BRANCH), (BASE_BRANCH, REFRESH_BRANCH, "a", "b")):
            with self.subTest(count=len(arguments)):
                result = self.run_publish(*arguments)

                self.assertEqual(result.returncode, 2, self.output(result))
                self.assertIn("usage:", result.stderr)

    def test_an_empty_branch_argument_is_refused(self) -> None:
        result = self.run_publish(BASE_BRANCH, "", str(self.report))

        self.assertEqual(result.returncode, 2, self.output(result))
        self.assertIn("REFRESH_BRANCH is empty", result.stderr)

    # Boundary: no change, an untracked part, a change outside the bundle, and token handling.

    def test_an_unchanged_bundle_publishes_nothing(self) -> None:
        result = self.run_publish(**WITH_PR_PAT)

        self.assertEqual(result.returncode, 0, self.output(result))
        self.assertIn("nothing to publish", result.stdout)
        self.assertEqual(self.gh_calls(), [])
        self.assertEqual(self.refresh_tip(), "")

    def test_an_untracked_source_part_counts_as_a_change(self) -> None:
        (self.work / ".devcontainer" / "praetor-source.001.b64").write_text(
            "part\n", encoding="utf-8")

        result = self.run_publish()

        self.assertEqual(result.returncode, 0, self.output(result))
        changed = git("diff-tree", "--no-commit-id", "--name-only", "-r", self.refresh_tip(),
                      cwd=self.origin)
        self.assertEqual(changed.splitlines(), [".devcontainer/praetor-source.001.b64"])

    def test_changes_outside_the_bundle_are_not_committed(self) -> None:
        self.regenerate()
        (self.work / "README.md").write_text("edited\n", encoding="utf-8")

        result = self.run_publish()

        self.assertEqual(result.returncode, 0, self.output(result))
        changed = git("diff-tree", "--no-commit-id", "--name-only", "-r", self.refresh_tip(),
                      cwd=self.origin)
        self.assertEqual(changed.splitlines(), [".devcontainer/praetor-source.000.b64"])

    def test_tokens_are_never_passed_as_arguments_or_printed(self) -> None:
        for token_setting in ({}, WITH_PR_PAT):
            with self.subTest(pr_token=bool(token_setting)):
                self.setUp()
                self.regenerate()

                result = self.run_publish(**token_setting)

                self.assertEqual(result.returncode, 0, self.output(result))
                for token in (PUSH_TOKEN, PR_TOKEN):
                    self.assertNotIn(token, self.output(result))
                    self.assertNotIn(token, "\n".join(self.gh_calls()))


class RefreshWiringTests(unittest.TestCase):
    """The workflow step that runs the publisher, pinned so its contract cannot drift."""

    STEP_MARKER = "      - name: Open Or Update The Refresh Pull Request\n"

    def setUp(self) -> None:
        self.document = WORKFLOW.read_text(encoding="utf-8")
        self.assertIn(self.STEP_MARKER, self.document, "the publishing step is no longer named")
        rest = self.document.split(self.STEP_MARKER, 1)[1]
        end = re.search(r"^      - name: ", rest, flags=re.MULTILINE)
        self.step = rest[: end.start()] if end else rest

    def test_the_pr_token_has_no_job_token_fallback(self) -> None:
        """A fallback hides the missing secret, so the script would try and fail to open the PR."""
        self.assertIn("PR_TOKEN: ${{ secrets.PRAETOR_PR_TOKEN }}\n", self.step)
        self.assertNotRegex(self.document, r"PRAETOR_PR_TOKEN\s*\|\|")

    def test_the_push_token_is_the_job_token(self) -> None:
        self.assertIn("PUSH_TOKEN: ${{ github.token }}\n", self.step)

    def test_the_step_runs_the_publisher_and_publishes_nothing_inline(self) -> None:
        """One implementation of the publishing logic (HISS-19): the script, which is tested."""
        self.assertIn("run: scripts/devcontainer_refresh_publish.sh ", self.step)
        self.assertNotIn("gh pr", self.step)
        self.assertNotIn("git push", self.step)

    def test_the_arguments_match_the_usage_line(self) -> None:
        run_line = re.search(r"run: scripts/devcontainer_refresh_publish\.sh (.+)\n", self.step)
        self.assertIsNotNone(run_line, "the step no longer runs the publisher")
        passed = re.findall(r'"\$([A-Z_]+)[^"]*"', run_line.group(1))
        usage = re.search(r'usage: \$0 ([A-Z_ ]+)"', SCRIPT.read_text(encoding="utf-8"))
        self.assertIsNotNone(usage, "the script no longer documents a usage line")

        self.assertEqual(passed, ["REFRESH_BASE_BRANCH", "REFRESH_BRANCH", "RUNNER_TEMP"])
        self.assertEqual(usage.group(1).split(), ["BASE_BRANCH", "REFRESH_BRANCH", "REPORT_FILE"])
        self.assertIn('"$RUNNER_TEMP/freshness.txt"', run_line.group(1))
        self.assertIn('> "$RUNNER_TEMP/freshness.txt"', self.document)

    def test_the_commit_identity_is_set_for_author_and_committer(self) -> None:
        """--signoff takes the committer identity; an unset one fails on a fresh runner."""
        for name in ("GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME",
                     "GIT_COMMITTER_EMAIL"):
            with self.subTest(variable=name):
                self.assertRegex(self.step, rf"\n          {name}: \$\{{\{{ vars\.PRAETOR_BOT_")


if __name__ == "__main__":
    unittest.main()
