#!/usr/bin/env python3
"""Hermetic regression tests for the repository adoption sweep script.

The sweep used to end every command in `|| true` and then print `[PASS]` unconditionally,
so a run in which adoption, the needs scan and the epic all failed still reported success
and exited 0. These tests pin the reporting contract: `[PASS]` only when every step of a
repository succeeded, `[FAIL]` naming the first step that did not, and a non-zero exit as
soon as anything failed. They also pin that the script carries no repository list of its
own (ADR-0014): targets come from arguments or --targets-file, and none is a usage error.

A stub standardsctl stands in for the real binary through PRAETOR_STANDARDSCTL and logs its
arguments, so no test builds Go code, touches a forge or writes outside its temporary
directory.
"""

from __future__ import annotations

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "adopt_repos.sh"

TARGETS = ("acme/kit", "acme/app")

# Every stub appends its argument vector to $STUB_LOG, one call per line.
STUB_LOG_LINE = 'echo "$*" >> "$STUB_LOG"\n'

STUB_ALWAYS_OK = "#!/bin/sh\n" + STUB_LOG_LINE + "exit 0\n"

# Fails only the third step, which is the case that used to be reported as a pass.
STUB_FAILS_EPIC = (
    "#!/bin/sh\n"
    + STUB_LOG_LINE
    + """if [ "$1" = "needs" ] && [ "$2" = "epic" ]; then
    echo "stub: epic publication refused" >&2
    exit 1
fi
exit 0
"""
)

STUB_FAILS_EVERYTHING = "#!/bin/sh\n" + STUB_LOG_LINE + 'echo "stub: refused" >&2\nexit 1\n'


class Sweep:
    """One temporary development root, stub binary and call log."""

    def __init__(self, directory: str, stub_body: str) -> None:
        root = Path(directory)
        self.dev_root = root / "dev"
        self.dev_root.mkdir(parents=True)
        self.log = root / "calls.log"
        self.stub = root / "bin" / "standardsctl"
        self.stub.parent.mkdir(parents=True)
        self.stub.write_text(stub_body, encoding="utf-8")
        self.stub.chmod(0o700)

    def make_repos(self, *targets: str) -> None:
        for target in targets:
            (self.dev_root / target).mkdir(parents=True)

    def calls(self) -> list[str]:
        if not self.log.exists():
            return []
        return self.log.read_text(encoding="utf-8").splitlines()

    def run(self, *args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        base = {k: v for k, v in os.environ.items() if k != "PRAETOR_DEV_ROOT"}
        return subprocess.run(
            ["bash", str(SCRIPT), *args],
            capture_output=True,
            text=True,
            env={**base, "PRAETOR_STANDARDSCTL": str(self.stub), "STUB_LOG": str(self.log), "LC_ALL": "C", **(env or {})},
            timeout=60,
            check=False,
        )


# The sweep is a bash operator script; Windows has no POSIX shell to run it in, so the
# suite states that rather than reporting a pass it did not earn (HISS-21).
@unittest.skipIf(sys.platform == "win32", "bash operator script; no POSIX shell on Windows")
class AdoptSweepTests(unittest.TestCase):
    def test_every_step_succeeding_reports_pass_and_exits_zero(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            sweep.make_repos(*TARGETS)

            result = sweep.run("--dev-root", str(sweep.dev_root), *TARGETS)

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("[PASS]"), len(TARGETS), result.stdout)
            self.assertNotIn("[FAIL]", result.stdout)
            self.assertIn("Adoption Sweep Complete", result.stdout)
            self.assertEqual(len(sweep.calls()), 3 * len(TARGETS), sweep.calls())
            self.assertFalse(any(call.startswith("issue reconcile") for call in sweep.calls()))

    def test_failed_epic_step_is_named_and_fails_the_run(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_FAILS_EPIC)
            sweep.make_repos(TARGETS[0])

            result = sweep.run("--dev-root", str(sweep.dev_root), TARGETS[0])

            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("failed at step: needs epic", result.stdout)
            self.assertNotIn("[PASS]", result.stdout)
            self.assertIn("Adoption Sweep Failed", result.stderr)

    def test_first_failing_step_is_the_one_reported(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_FAILS_EVERYTHING)
            sweep.make_repos(TARGETS[0])

            result = sweep.run("--dev-root", str(sweep.dev_root), "--reconcile", TARGETS[0])

            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("failed at step: adopt", result.stdout)
            self.assertNotIn("failed at step: needs scan", result.stdout)
            self.assertIn("issue reconcile failed", result.stdout)

    def test_missing_repository_warns_per_entry_and_exits_zero(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)

            result = sweep.run("--dev-root", str(sweep.dev_root), *TARGETS)

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("[WARN]"), len(TARGETS), result.stdout)
            self.assertNotIn("[PASS]", result.stdout)
            self.assertEqual(sweep.calls(), [])

    def test_development_root_comes_from_the_environment_when_no_flag_is_given(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            sweep.make_repos(TARGETS[0])

            result = sweep.run(TARGETS[0], env={"PRAETOR_DEV_ROOT": str(sweep.dev_root)})

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn(str(sweep.dev_root / TARGETS[0]), result.stdout)
            self.assertEqual(result.stdout.count("[PASS]"), 1, result.stdout)

    def test_no_target_prints_usage_and_runs_nothing(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            sweep.make_repos(*TARGETS)
            empty = Path(directory) / "empty.txt"
            empty.write_text("# nothing selected\n\n", encoding="utf-8")

            for args in ((), ("--dev-root", str(sweep.dev_root)), ("--targets-file", str(empty)), ("--reconcile",)):
                result = sweep.run(*args)
                self.assertEqual(result.returncode, 2, (args, result.stdout, result.stderr))
                self.assertIn("Usage: scripts/adopt_repos.sh", result.stderr)
            self.assertEqual(sweep.calls(), [], "a run without targets must not invoke praetorctl")

    def test_invalid_invocations_exit_two(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            for args in (("--unknown", "x"), ("--targets-file",), ("--targets-file", str(Path(directory) / "missing.txt"))):
                result = sweep.run(*args)
                self.assertEqual(result.returncode, 2, (args, result.stdout, result.stderr))
            self.assertEqual(sweep.calls(), [])
            help_result = sweep.run("--help")
            self.assertEqual(help_result.returncode, 0, help_result.stderr)
            self.assertIn("--targets-file FILE", help_result.stdout)

    def test_targets_file_and_arguments_combine(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            sweep.make_repos(*TARGETS)
            absolute = Path(directory) / "elsewhere" / "tool"
            absolute.mkdir(parents=True)
            targets = Path(directory) / "targets.txt"
            targets.write_text(f"# fleet\n  {TARGETS[0]}  \n\n{absolute}\n", encoding="utf-8")

            result = sweep.run("--dev-root", str(sweep.dev_root), "--targets-file", str(targets), TARGETS[1])

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("[PASS]"), 3, result.stdout)
            adopted = [call for call in sweep.calls() if call.startswith("adopt ")]
            self.assertEqual(
                adopted,
                [
                    f"adopt --path={sweep.dev_root / TARGETS[1]} --force",
                    f"adopt --path={sweep.dev_root / TARGETS[0]} --force",
                    f"adopt --path={absolute} --force",
                ],
            )

    def test_reconcile_uses_the_configured_scope_or_the_given_list(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            sweep = Sweep(directory, STUB_ALWAYS_OK)
            sweep.make_repos(TARGETS[0])

            plain = sweep.run("--dev-root", str(sweep.dev_root), "--reconcile", TARGETS[0])
            listed = sweep.run("--dev-root", str(sweep.dev_root), "--reconcile-repos=acme/kit,acme/app", TARGETS[0])

            self.assertEqual(plain.returncode, 0, plain.stdout + plain.stderr)
            self.assertEqual(listed.returncode, 0, listed.stdout + listed.stderr)
            reconciles = [call for call in sweep.calls() if call.startswith("issue reconcile")]
            self.assertEqual(
                reconciles,
                ["issue reconcile --dry-run=false", "issue reconcile --dry-run=false --repos=acme/kit,acme/app"],
            )


if __name__ == "__main__":
    unittest.main()
