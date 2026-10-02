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
import os
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

    def test_failure_blocks_bounds(self):
        """Boundary: both bounds hold at the limit and one past it, in count and in length.

        The two constants are what keeps a noisy suite from burying the log, and neither was
        exercised anywhere in the repository: the case above proves the mechanism at four
        blocks of roughly eight lines, well inside both. A driver that dropped either bound,
        or that kept N-1 or N+1, passes every other case in this file.

        Replayed both directions here: at the limit nothing is dropped, one past it the
        surplus is, and the block that survives is cut to the length bound rather than to the
        whole block.
        """
        rule = "\n" + driver.CASE_RULE + "\n"

        def compose(count):
            return "preamble" + "".join(f"{rule}FAIL: case_{n} (m.T.case_{n})\nbody\n" for n in range(count))

        at_limit = driver.failure_blocks(compose(driver.MAX_FAILURE_BLOCKS))
        self.assertEqual(len(at_limit), driver.MAX_FAILURE_BLOCKS)
        self.assertIn(f"case_{driver.MAX_FAILURE_BLOCKS - 1}", at_limit[-1])
        over_limit = driver.failure_blocks(compose(driver.MAX_FAILURE_BLOCKS + 1))
        self.assertEqual(len(over_limit), driver.MAX_FAILURE_BLOCKS)
        self.assertNotIn(f"case_{driver.MAX_FAILURE_BLOCKS}", "\n".join(over_limit))

        def one_block(lines):
            return "preamble" + rule + "\n".join(f"line {n}" for n in range(lines))

        exact = driver.failure_blocks(one_block(driver.MAX_FAILURE_BLOCK_LINES))
        self.assertEqual(len(exact[0].splitlines()), driver.MAX_FAILURE_BLOCK_LINES)
        self.assertIn(f"line {driver.MAX_FAILURE_BLOCK_LINES - 1}", exact[0])
        cut = driver.failure_blocks(one_block(driver.MAX_FAILURE_BLOCK_LINES + 1))
        self.assertEqual(len(cut[0].splitlines()), driver.MAX_FAILURE_BLOCK_LINES)
        self.assertNotIn(f"line {driver.MAX_FAILURE_BLOCK_LINES}", cut[0])
        # Negative: a block is cut at the summary rule, so the run's trailing summary is never
        # counted against either bound.
        summary = "preamble" + rule + "FAIL: case_z (m.T.case_z)\nbody\n" + "-" * 70 + "\nRan 1 test in 0.1s\n\nFAILED"
        self.assertEqual(driver.failure_blocks(summary), ["FAIL: case_z (m.T.case_z)\nbody"])

    def test_passing_suite_names_no_failures(self):
        """Negative: a green suite prints no failure lines, even if its output mentions FAIL."""
        with tempfile.TemporaryDirectory() as temp:
            body = "def test_a(self): print('FAIL: not a unittest block')\n"
            suite = write_suite(temp, "quiet.py", body)
            code, output = run_driver(temp, [suite], ["--min-executed", "1"])
        self.assertEqual(code, 0, output)
        self.assertNotIn("failed ", output)


# Rewrites the page's table from its rendering instead of comparing.
UPDATE_TABLE_ENV = "PRAETOR_UPDATE_HOOK_TOOLCHAIN_TABLE"


class HookToolchainTable(unittest.TestCase):
    """The HISS-21 page's tool table is the rendering of the declared list, never prose (#341)."""

    def test_hook_toolchain_table_matches_the_declarations(self):
        page = (ROOT / driver.PAGE).read_text(encoding="utf-8").replace("\r\n", "\n")
        table, before, after = driver.marked_table(page)
        rendered = driver.current_toolchain_table()
        if os.environ.get(UPDATE_TABLE_ENV) == "1":
            (ROOT / driver.PAGE).write_text(before + rendered + after, encoding="utf-8",
                                            newline="\n")
            return
        self.assertEqual(table, rendered, f"{driver.PAGE.as_posix()} differs from the declared "
                         f"hook toolchain; regenerate it with {UPDATE_TABLE_ENV}=1 "
                         "python3 -B scripts/test_portability_selftest.py")

    def test_the_table_names_every_declared_tool_once(self):
        # The page carries exactly this rendering (the case above), and the DeclaredPrograms
        # cases of .config/lefthook/scripts/test_hooks.py hold the declarations to what the
        # hook scripts start, so a program a hook starts cannot be missing from the page.
        toolchain = driver.hook_toolchain()
        declared = [*toolchain.RESOLVED, *toolchain.BY_NAME, *toolchain.tool_floors()]
        rows = driver.current_toolchain_table().splitlines()[2:]
        self.assertEqual([re.match(r"\| `([^`]+)` \|", row).group(1) for row in rows], declared)
        self.assertEqual(len(declared), len(set(declared)))
        self.assertIn("sh", declared)

    def test_the_workflow_requires_only_declared_tools_and_every_one_a_hook_needs(self):
        toolchain = driver.hook_toolchain()
        asserted = driver.asserted_tools((ROOT / driver.WORKFLOW).read_text(encoding="utf-8"))
        declared = [*toolchain.RESOLVED, *toolchain.BY_NAME, *toolchain.tool_floors()]
        self.assertEqual([tool for tool in asserted if tool not in declared], [])
        self.assertEqual(len(asserted), len(set(asserted)))
        # No hook starts without its interpreter, make and the shell that runs every job.
        self.assertEqual([tool for tool in (*toolchain.RESOLVED, "sh") if tool not in asserted], [])
        self.assertNotIn(str(ROOT / driver.HOOK_SCRIPTS), sys.path)

    def test_requirements_are_read_from_the_policy_declarations(self):
        class Policy:
            RESOLVED = {"python": (("python3",), ("py", "-3")), "make": (("make",),)}
            PYTHON_FLOOR = "3.10"
            BY_NAME = ("git",)

            @staticmethod
            def tool_floors():
                return {"shellcheck": "0.11.0", "hadolint": None}
        required = driver.declared_requirements(Policy)
        self.assertEqual(required, {
            "python": "the first of `python3`, `py -3` on `PATH` that states Python 3.10 or newer",
            "make": "the first of `make` on `PATH` that states GNU Make",
            "git": "found on `PATH` under this name",
            "shellcheck": "version 0.11.0 or newer",
            "hadolint": "installed; no version floor"})
        self.assertEqual(list(required), ["python", "make", "git", "shellcheck", "hadolint"])
        # Every program the real policy resolves has a stated proof.
        self.assertEqual(sorted(driver.PROOFS), sorted(driver.hook_toolchain().RESOLVED))

    def test_table_rows_follow_the_requirements_and_the_required_list(self):
        required = {"python": "states Python 3.10 or newer", "sh": "found on `PATH`",
                    "hadolint": "installed; no version floor"}
        table = driver.toolchain_table(required, ["python", "sh"])
        self.assertEqual(table.splitlines()[2:], [
            "| `python` | states Python 3.10 or newer | asserted on every leg |",
            "| `sh` | found on `PATH` | asserted on every leg |",
            "| `hadolint` | installed; no version floor | reported, not asserted |"])
        # Negative: nothing required, so nothing is claimed as asserted.
        self.assertNotIn("asserted on every leg", driver.toolchain_table(required, []))
        # Boundary: no declaration leaves the header alone.
        self.assertEqual(len(driver.toolchain_table({}, ["python"]).splitlines()), 2)

    def test_the_required_list_is_read_from_one_workflow_line(self):
        step = "        env:\n          REQUIRED: python,make,yamllint\n"
        self.assertEqual(driver.asserted_tools(step), ["python", "make", "yamllint"])
        for workflow in ("jobs: {}\n", step + step, "REQUIRED: python\n",
                         "          REQUIRED: python make\n"):
            with self.subTest(workflow=workflow), self.assertRaises(ValueError):
                driver.asserted_tools(workflow)

    def test_a_page_without_the_marked_block_is_refused(self):
        block = f"intro\n{driver.TABLE_START}\n| a |\n| b |\n{driver.TABLE_END}\noutro\n"
        table, before, after = driver.marked_table(block)
        self.assertEqual(table, "| a |\n| b |")
        self.assertEqual(before + table + after, block)
        for page in ("no markers\n", f"{driver.TABLE_START}\n| a |\n",
                     f"| a |\n{driver.TABLE_END}\n"):
            with self.subTest(page=page), self.assertRaises(ValueError):
                driver.marked_table(page)


# Where each floor of .config/lefthook/tool-floors.txt takes its version from.
HOOK_LINT_INPUT = ".config/hook-lint/requirements.in"
HOOK_LINT_LOCK = ".config/hook-lint/requirements.txt"
ACTIONLINT_FILE = ".github/actionlint.yaml"
SHELLCHECK_PIN = re.compile(r"^ +SHELLCHECK_VERSION: (\S+)$", re.M)
LOCK_PYTHON = re.compile(r"--python-version=(\d+\.\d+)\b")
# Files that would install a linter in this repository: the workflows and the container.
INSTALL_SOURCES = (".github/workflows/*.yml", ".devcontainer/devcontainer.json",
                   ".devcontainer/Dockerfile.praetor")


def floor_gap(toolchain, tool, installed, floors):
    """Return why installed does not meet tool's floor, or "" when it does."""
    floor = floors.get(tool)
    if floor is None:
        return f"{tool} has no floor"
    version = installed.removeprefix("v")
    if re.fullmatch(r"\d+(\.\d+)*", version) is None:
        return f"{tool} version {installed} is not a version"
    if toolchain.below(version, floor):
        return f"{tool} {installed} is below the declared floor {floor}"
    return ""


class HookToolFloorSources(unittest.TestCase):
    """A floor needs a source in this repository, and that source is held to the floor (#343)."""

    def text(self, relative):
        return (ROOT / relative).read_text(encoding="utf-8")

    def test_the_versions_this_repository_installs_meet_the_floors(self):
        toolchain = driver.hook_toolchain()
        floors = toolchain.tool_floors()
        self.assertEqual(sorted(floors), ["actionlint", "hadolint", "shellcheck", "yamllint"])
        # The lint lock input is read by the reader the floors file is read by.
        parsed = [toolchain.requirement(line) for line in self.text(HOOK_LINT_INPUT).splitlines()]
        pins = {name: version for name, operator, version in filter(None, parsed)
                if operator == "=="}
        self.assertEqual(floor_gap(toolchain, "yamllint", pins["yamllint"], floors), "")
        released = SHELLCHECK_PIN.findall(self.text(driver.WORKFLOW))
        self.assertEqual(len(released), 1, released)
        self.assertEqual(floor_gap(toolchain, "shellcheck", released[0], floors), "")
        self.assertIn(f"actionlint v{floors['actionlint']}", self.text(ACTIONLINT_FILE))
        # The interpreter floor is the release the lint lock is compiled for.
        self.assertEqual(LOCK_PYTHON.findall(self.text(HOOK_LINT_LOCK)), [toolchain.PYTHON_FLOOR])

    def test_a_linter_without_a_source_has_no_floor(self):
        # Nothing here installs hadolint or names a version of it, so its line declares no
        # floor. Once a file below does, derive the floor from that file.
        floors = driver.hook_toolchain().tool_floors()
        self.assertEqual([tool for tool, floor in floors.items() if floor is None], ["hadolint"])
        named = [path.relative_to(ROOT).as_posix()
                 for pattern in INSTALL_SOURCES for path in sorted(ROOT.glob(pattern))
                 if "hadolint" in path.read_text(encoding="utf-8")]
        self.assertEqual(named, [])
        self.assertGreater(len(list(ROOT.glob(INSTALL_SOURCES[0]))), 3)

    def test_floor_gaps_are_named(self):
        toolchain = driver.hook_toolchain()
        floors = {"shellcheck": "0.11.0", "yamllint": "1.38.0", "hadolint": None}
        cases = (("shellcheck", "0.11.0", ""), ("shellcheck", "v0.11.0", ""),
                 ("yamllint", "1.39.0", ""),
                 ("shellcheck", "0.9.0", "below the declared floor 0.11.0"),
                 ("yamllint", "1.37.9", "below the declared floor 1.38.0"),
                 ("hadolint", "2.14.0", "has no floor"), ("actionlint", "1.7.12", "has no floor"),
                 ("shellcheck", "stable", "is not a version"))
        for tool, installed, want in cases:
            with self.subTest(tool=tool, installed=installed):
                gap = floor_gap(toolchain, tool, installed, floors)
                self.assertEqual(gap == "", want == "", gap)
                self.assertIn(want, gap)


if __name__ == "__main__":
    unittest.main(verbosity=2)
