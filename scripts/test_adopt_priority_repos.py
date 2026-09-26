#!/usr/bin/env python3
"""Hermetic regression tests for the priority adoption sweep script.

The sweep used to end every command in `|| true` and then print `[PASS]` unconditionally,
so a run in which adoption, the needs scan and the epic all failed still reported success
and exited 0. These tests pin the reporting contract: `[PASS]` only when every step of a
repository succeeded, `[FAIL]` naming the first step that did not, and a non-zero exit as
soon as anything failed.

A stub standardsctl stands in for the real binary through PRAETOR_STANDARDSCTL, so no test
builds Go code, touches a forge or writes outside its temporary directory.
"""

from __future__ import annotations

import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "adopt_priority_repos.sh"

# The sweep's repository list, read from the script so the tests do not restate it.
REPO_SUFFIXES = re.findall(r'"\$\{DEV_ROOT\}/([^"]+)"', SCRIPT.read_text(encoding="utf-8"))

STUB_ALWAYS_OK = "#!/bin/sh\nexit 0\n"

# Fails only the third step, which is the case that used to be reported as a pass.
STUB_FAILS_EPIC = """#!/bin/sh
if [ "$1" = "needs" ] && [ "$2" = "epic" ]; then
    echo "stub: epic publication refused" >&2
    exit 1
fi
exit 0
"""

STUB_FAILS_EVERYTHING = "#!/bin/sh\necho \"stub: refused\" >&2\nexit 1\n"


def write_stub(path: Path, body: str) -> Path:
    """Write an executable stub standardsctl and return its path."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body, encoding="utf-8")
    path.chmod(0o700)
    return path


def run_sweep(dev_root: Path, stub: Path) -> subprocess.CompletedProcess[str]:
    """Run the sweep against dev_root with stub standing in for standardsctl."""
    return subprocess.run(
        ["bash", str(SCRIPT), str(dev_root)],
        capture_output=True,
        text=True,
        env={**os.environ, "PRAETOR_STANDARDSCTL": str(stub), "LC_ALL": "C"},
        timeout=60,
        check=False,
    )


# The sweep is a bash operator script; Windows has no POSIX shell to run it in, so the
# suite states that rather than reporting a pass it did not earn (HISS-21).
@unittest.skipIf(sys.platform == "win32", "bash operator script; no POSIX shell on Windows")
class AdoptSweepTests(unittest.TestCase):
    def test_every_step_succeeding_reports_pass_and_exits_zero(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            root = Path(directory)
            dev_root = root / "dev"
            for suffix in REPO_SUFFIXES:
                (dev_root / suffix).mkdir(parents=True)
            stub = write_stub(root / "bin" / "standardsctl", STUB_ALWAYS_OK)

            result = run_sweep(dev_root, stub)

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("[PASS]"), len(REPO_SUFFIXES), result.stdout)
            self.assertNotIn("[FAIL]", result.stdout)
            self.assertIn("Priority Adoption Sweep Complete", result.stdout)

    def test_failed_epic_step_is_named_and_fails_the_run(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            root = Path(directory)
            dev_root = root / "dev"
            (dev_root / REPO_SUFFIXES[0]).mkdir(parents=True)
            stub = write_stub(root / "bin" / "standardsctl", STUB_FAILS_EPIC)

            result = run_sweep(dev_root, stub)

            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("failed at step: needs epic", result.stdout)
            self.assertNotIn("[PASS]", result.stdout)
            self.assertIn("Priority Adoption Sweep Failed", result.stderr)

    def test_first_failing_step_is_the_one_reported(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            root = Path(directory)
            dev_root = root / "dev"
            (dev_root / REPO_SUFFIXES[0]).mkdir(parents=True)
            stub = write_stub(root / "bin" / "standardsctl", STUB_FAILS_EVERYTHING)

            result = run_sweep(dev_root, stub)

            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn("failed at step: adopt", result.stdout)
            self.assertNotIn("failed at step: needs scan", result.stdout)
            self.assertIn("issue reconcile failed for owner", result.stdout)

    def test_no_repository_on_disk_warns_per_entry_and_exits_zero(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            root = Path(directory)
            dev_root = root / "dev"
            dev_root.mkdir(parents=True)
            stub = write_stub(root / "bin" / "standardsctl", STUB_ALWAYS_OK)

            result = run_sweep(dev_root, stub)

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stdout.count("[WARN]"), len(REPO_SUFFIXES), result.stdout)
            self.assertNotIn("[PASS]", result.stdout)
            self.assertNotIn("[FAIL]", result.stdout)

    def test_development_root_comes_from_the_environment_when_no_argument_is_given(self) -> None:
        with tempfile.TemporaryDirectory(prefix="praetor-sweep-test-") as directory:
            root = Path(directory)
            dev_root = root / "dev"
            (dev_root / REPO_SUFFIXES[0]).mkdir(parents=True)
            stub = write_stub(root / "bin" / "standardsctl", STUB_ALWAYS_OK)

            result = subprocess.run(
                ["bash", str(SCRIPT)],
                capture_output=True,
                text=True,
                env={
                    **os.environ,
                    "PRAETOR_STANDARDSCTL": str(stub),
                    "PRAETOR_DEV_ROOT": str(dev_root),
                    "LC_ALL": "C",
                },
                timeout=60,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn(str(dev_root / REPO_SUFFIXES[0]), result.stdout)
            self.assertEqual(result.stdout.count("[PASS]"), 1, result.stdout)


if __name__ == "__main__":
    unittest.main()
