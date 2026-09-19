#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Tests for the HISS-21 portability driver.

The case that matters is the third one: a suite that exits zero having run nothing must
fail, because that is the state the driver exists to catch. A test proving the driver
passes a healthy suite proves almost nothing on its own.
"""

import contextlib
import importlib.util
import io
import re
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

_spec = importlib.util.spec_from_file_location(
    "portability_selftest", ROOT / "scripts" / "portability_selftest.py")
driver = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(driver)


SUITE_TEMPLATE = textwrap.dedent("""
    import unittest
    class T(unittest.TestCase):
    {body}
    if __name__ == "__main__":
        unittest.main(verbosity=2)
""")


def write_suite(directory, name, body):
    """Materialise a throwaway unittest module and return its path relative to directory."""
    path = Path(directory) / name
    path.write_text(SUITE_TEMPLATE.format(body=textwrap.indent(body, "    ")), encoding="utf-8")
    return Path(name)


@contextlib.contextmanager
def driver_over(directory, suites):
    """Point the driver at fixture suites inside a temporary root."""
    original_root, original_suites = driver.ROOT, driver.SUITES
    driver.ROOT, driver.SUITES = Path(directory), tuple(suites)
    try:
        yield
    finally:
        driver.ROOT, driver.SUITES = original_root, original_suites


def run_driver(directory, suites, argv):
    """Invoke the driver against fixtures and return (exit_code, captured_output)."""
    buffer = io.StringIO()
    with driver_over(directory, suites), contextlib.redirect_stdout(buffer):
        code = driver.main(argv)
    return code, buffer.getvalue()


class PortabilityDriver(unittest.TestCase):

    def test_passing_suite_is_accepted(self):
        """Positive: suites that pass and clear the floor exit zero."""
        with tempfile.TemporaryDirectory() as temp:
            suite = write_suite(temp, "ok.py", "def test_a(self): pass\ndef test_b(self): pass\n")
            code, output = run_driver(temp, [suite], ["--min-executed", "2"])
        self.assertEqual(code, 0, output)
        self.assertIn("2 executed", output)

    def test_failing_suite_is_rejected(self):
        """Negative: a real assertion failure fails the platform."""
        with tempfile.TemporaryDirectory() as temp:
            suite = write_suite(temp, "bad.py", "def test_a(self): self.fail('boom')\n")
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 1, output)
        self.assertIn("harness self-tests failed", output)

    def test_suite_that_ran_nothing_is_rejected(self):
        """The defect this driver exists for: every suite exits zero, nothing executed.

        unittest reports `OK (skipped=1)` and exit status 0 for a fully-skipped suite. Without
        the floor the platform reads as green while having checked nothing at all.
        """
        with tempfile.TemporaryDirectory() as temp:
            suite = write_suite(temp, "skipped.py", "def test_a(self): self.skipTest('no cgo')\n")
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 1, output)
        self.assertIn("below the floor", output)
        self.assertIn("0 tests executed", output)

    def test_skip_reason_is_printed(self):
        """A skip must name what is absent, or the operator cannot tell coverage was lost."""
        with tempfile.TemporaryDirectory() as temp:
            suite = write_suite(
                temp, "mixed.py",
                "def test_a(self): pass\ndef test_b(self): self.skipTest('race needs cgo')\n")
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 0, output)
        self.assertIn("race needs cgo", output)
        self.assertIn("1 executed, 1 skipped, 2 collected", output)

    def test_floor_boundary_is_inclusive(self):
        """Boundary: executed == floor passes, executed == floor - 1 fails."""
        with tempfile.TemporaryDirectory() as temp:
            suite = write_suite(temp, "two.py", "def test_a(self): pass\ndef test_b(self): pass\n")
            at_floor, _ = run_driver(temp, [suite], ["--min-executed", "2"])
            below_floor, _ = run_driver(temp, [suite], ["--min-executed", "3"])
        self.assertEqual(at_floor, 0)
        self.assertEqual(below_floor, 1)

    def test_suite_without_a_summary_line_is_rejected(self):
        """A suite that dies before unittest reports has run nothing, whatever it exits."""
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "silent.py"
            path.write_text("import sys\nsys.exit(0)\n", encoding="utf-8")
            code, output = run_driver(temp, [Path("silent.py")], ["--min-executed", "0"])
        self.assertEqual(code, 1, output)

    def test_missing_suite_is_rejected(self):
        """A suite that is not on disk must fail rather than be counted as nothing to do."""
        with tempfile.TemporaryDirectory() as temp:
            code, output = run_driver(temp, [Path("absent.py")], ["--min-executed", "0"])
        self.assertEqual(code, 1, output)
        self.assertIn("missing", output)

    def test_nested_summary_does_not_shadow_the_real_one(self):
        """The count must come from the suite's own summary, not a child's.

        These suites drive the real hooks, and a hook can run a nested unittest suite whose
        summary is echoed into this stream. Reading the first "Ran N tests" made the driver
        report "2 executed" for a run of 66 -- a number nothing measured, presented as one
        something did, which is the precise failure this driver exists to prevent.
        """
        with tempfile.TemporaryDirectory() as temp:
            body = ("def test_noisy(self):\n"
                    "    print('-' * 70)\n"
                    "    print('Ran 2 tests in 0.001s')\n"
                    "    print()\n"
                    "    print('OK (skipped=1)')\n"
                    "def test_b(self): pass\n"
                    "def test_c(self): pass\n")
            suite = write_suite(temp, "nested.py", body)
            code, output = run_driver(temp, [suite], ["--min-executed", "3"])
        self.assertEqual(code, 0, output)
        self.assertIn("3 executed, 0 skipped, 3 collected", output)

    def test_suites_match_the_makefile(self):
        """The driver must run exactly what `make hooks-test` runs, or the matrix tests less.

        Two lists of the same suites drift; this pins them together (HISS-19).
        """
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        target = re.search(r"^hooks-test:\n((?:\t.*\n)+)", makefile, re.M)
        self.assertIsNotNone(target, "hooks-test target not found in Makefile")
        declared = re.findall(r"python3 -B (\S+\.py)", target.group(1))
        # The Makefile names suites with slashes; str() of a Path uses the host separator, so on
        # Windows this compared ".config\\lefthook\\..." with ".config/lefthook/..." and failed.
        self.assertEqual([s.as_posix() for s in driver.SUITES], declared)

    def test_failing_test_names_survive_the_tail(self):
        """A failure is named even when later output pushes it out of the printed tail.

        The driver printed only a failing suite's last 25 lines. unittest reports each failure
        block in order and the hook suites print a lefthook banner per case, so an early failure
        was routinely cut off and the log said a suite failed without saying which test.
        """
        with tempfile.TemporaryDirectory() as temp:
            body = ("def test_a_fails_early(self): self.fail('early')\n"
                    "def test_b_errors(self):\n"
                    "    \"\"\"A docstring replaces the name on the outcome line.\"\"\"\n"
                    "    print('printed before the failure')\n"
                    "    raise RuntimeError('boom')\n"
                    "def test_c_noisy(self): print('\\n'.join(['noise'] * 60))\n")
            suite = write_suite(temp, "named.py", body)
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 1, output)
        self.assertIn("failed FAIL test_a_fails_early", output)
        self.assertIn("failed ERROR test_b_errors", output)
        self.assertNotIn("failed FAIL test_c_noisy", output)

    def test_every_failing_case_prints_its_own_traceback(self):
        """Every failure's own block reaches the log, not only the run's last 25 lines.

        A macOS run failed four cases in one suite and published one traceback (#135). The
        mechanism is block count times block length, not noise: unittest's printErrors emits
        every failure block after the last case has run, and run_suite composes
        stdout + stderr, so a case's own prints land ahead of the blocks and can never push
        one out of a tail. Four blocks of roughly eight lines each plus the summary cannot
        fit in 25 lines, and the first one falls out.

        Fail-before measured against origin/main's driver on this very fixture: 'marker
        alpha' absent, bravo, charlie and delta present. With failure_blocks: all four
        present. The earlier fixture here -- two failures and one noisy passing case --
        passed against both drivers and proved only that the function exists.
        """
        with tempfile.TemporaryDirectory() as temp:
            body = ("def test_a(self): self.fail('marker alpha')\n"
                    "def test_b(self): self.fail('marker bravo')\n"
                    "def test_c(self): self.fail('marker charlie')\n"
                    "def test_d(self): self.fail('marker delta')\n")
            suite = write_suite(temp, "blocks.py", body)
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 1, output)
        for marker in ("marker alpha", "marker bravo", "marker charlie", "marker delta"):
            self.assertIn(marker, output)
        # Boundary: the blocks are bounded, so a suite that prints nothing parseable still
        # publishes its tail rather than nothing at all.
        self.assertEqual(driver.failure_blocks("no unittest output here"), [])

    def test_passing_suite_names_no_failures(self):
        """Negative: a green suite prints no failure lines, even if its output mentions FAIL."""
        with tempfile.TemporaryDirectory() as temp:
            body = "def test_a(self): print('FAIL: not a unittest block')\n"
            suite = write_suite(temp, "quiet.py", body)
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 0, output)
        self.assertNotIn("failed ", output)


if __name__ == "__main__":
    unittest.main(verbosity=2)
