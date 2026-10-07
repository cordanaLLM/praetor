#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Run the harness self-tests on this platform and report what actually executed.

HISS-21 (platform neutrality) permits a gate to skip where a platform genuinely cannot
run it, on one condition: the skip is stated, not silent. A suite that quietly degrades
to zero executed tests and still exits zero is the failure this script exists to catch --
the same defect class as a memory reading nothing invented, or a duplicate scan scoring an
empty set. "All tests passed" and "no test ran" must never look alike.

So the pass condition is not the suites' exit codes alone. It is: every suite exited zero,
AND at least --min-executed tests actually ran across them. Every skip is printed with its
reason and counted, so a platform losing coverage shows up in the log as a number that
moved rather than as an unchanged green check.

This module also holds run_child, the one helper the self-tests start their children with:
a child that a signal ends is started once more, and both attempts are reported.
"""

import argparse
import inspect
import os
import re
import shutil
import signal
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The suites `make hooks-test` runs. Kept in this order so the output reads the same
# everywhere; the list is asserted against the Makefile by test_portability_selftest.py.
SUITES = (
    Path(".config/lefthook/scripts/test_security_scope.py"),
    Path(".config/lefthook/scripts/test_hooks.py"),
    Path(".config/lefthook/scripts/test_checkpoint.py"),
    Path("scripts/test_checkpoint_hooks.py"),
    Path("scripts/test_praetor_hook.py"),
)

# unittest's summary line, e.g. "Ran 66 tests in 41.2s" and "OK (skipped=5)".
#
# These suites run the real hooks, and a hook may run a nested unittest suite whose own summary
# is echoed into this stream. The FIRST "Ran N tests" can therefore belong to a child, not to
# the suite being measured -- which is how this driver once reported "2 executed" for a run of
# 66. The authoritative summary is the last one, and the outcome is read only after it.
RAN = re.compile(r"^Ran (\d+) tests? in ", re.M)
SKIPPED = re.compile(r"skipped=(\d+)")
# verbosity=2 prints one line per skip: "name (mod.Case.name) ... skipped 'reason'".
SKIP_REASON = re.compile(r"^(\S+) \([^)]*\) \.\.\. skipped ['\"](.*)['\"]$", re.M)
# unittest reports each failing case in a block headed by a rule of 70 "=" and then
# "FAIL: name (mod.Case.name)" or "ERROR: ...". The per-case "... FAIL" outcome line is not
# usable: a docstring replaces the name on it, and output the case prints lands between the
# name and the outcome. Anchoring on the rule keeps a test that merely prints "FAIL:" out.
FAILED_CASE = re.compile(r"^={70}\n(FAIL|ERROR): (\S+) \(", re.M)
# A per-suite timeout well above the slowest observed run; a hung suite must fail, not hang CI.
SUITE_TIMEOUT_SECONDS = 1800
# The same rule, used to cut the output into one block per failing case. Printing only the
# run's last 25 lines meant a suite that failed four cases published one traceback and left
# the other three to be attributed by reading the code (#135). Both bounds keep a noisy suite
# from burying the log; the tail is still printed when nothing parses as a block.
CASE_RULE = "=" * 70
SUMMARY_RULE = re.compile(r"^-{70}\nRan \d+ tests? in ", re.M)
MAX_FAILURE_BLOCKS = 12
MAX_FAILURE_BLOCK_LINES = 40

# The hook toolchain table of the HISS-21 page is rendered, never written by hand: three
# attempts to state in prose which tools the matrix covers each published a false list (#341).
# Its rows are the hook policy's own declarations (RESOLVED, BY_NAME and tool-floors.txt, read
# through .config/lefthook/scripts/toolchain.py) and its last column is the REQUIRED list of
# the workflow's Assert Hook Toolchain step.
HOOK_SCRIPTS = Path(".config/lefthook/scripts")
WORKFLOW = Path(".github/workflows/portability.yml")
PAGE = Path("docs/standards/hiss-21-platform-neutrality.md")
TABLE_START = "<!-- praetor:hook-toolchain:start -->"
TABLE_END = "<!-- praetor:hook-toolchain:end -->"
REQUIRED = re.compile(r"^ +REQUIRED: ([a-z0-9_.,-]+)$", re.M)
# What each resolved program must state before the policy runs it.
PROOFS = {"python": "Python {floor} or newer", "make": "GNU Make"}

# A child a self-test starts is started once more when a signal ends it (#809). A macOS leg
# lost the Python launcher of one scope-bridge call to SIGSEGV, with no output, and the same
# suite passed on every rerun. Every signal exit prints a report between the two marker lines
# below: the test, the child's kind, resolved path and stated version, and each attempt with
# its signal or exit code and its output. The driver repeats every report under the suite that
# printed it, so a pass after a retry is never silent. A crash is never a pass: a child that a
# signal ends twice fails its test with ChildCrashed. A nonzero exit without a signal is the
# child's answer and is returned at once. Windows ends no process by a signal: os.kill and a
# crash there each leave an exit code, which is returned like any other.
CHILD_REPORT_START = "praetor-selftest: child ended by a signal"
CHILD_REPORT_END = "praetor-selftest: end of child report"
# The start marker is not anchored to a line start: unittest prints a test's name or its dot
# without a newline before the test runs, so the first line of a report can follow it.
CHILD_REPORT = re.compile(
    rf"{re.escape(CHILD_REPORT_START)}$.*?^{re.escape(CHILD_REPORT_END)}$", re.M | re.S)
# Reports repeated per suite, and the tail quoted from each stream of an attempt. A crash
# prints little; the bounds keep a noisy one from burying the log.
MAX_CHILD_REPORTS = 8
MAX_CHILD_OUTPUT = 1500
MAX_CHILD_LINES = 20
# Children that state their version with another argument than --version.
VERSION_ARGS = {"go": ("version",)}
VERSION_LINE = re.compile(r"^[ \t]*(\S[^\r\n]*)", re.M)
MAX_VERSION_REASON = 240
# Frames searched upwards for the test that started a child (HISS-02).
MAX_CALLER_FRAMES = 32


def hook_toolchain():
    """Load the hook policy's toolchain module, which declares what the hooks depend on."""
    scripts = str(ROOT / HOOK_SCRIPTS)
    sys.path.insert(0, scripts)
    try:
        import toolchain
    finally:
        sys.path.remove(scripts)
    return toolchain


def asserted_tools(workflow):
    """Return the tools the workflow's Assert Hook Toolchain step requires on every leg."""
    found = REQUIRED.findall(workflow)
    if len(found) != 1:
        raise ValueError(f"{WORKFLOW.as_posix()}: expected one REQUIRED list, found {len(found)}")
    return found[0].split(",")


def declared_requirements(toolchain):
    """Return {tool: what the hook policy requires of it}, one entry per declared tool.

    The order is the policy's own: the programs it resolves from candidates, the programs it
    starts by name, then the linters of the floors file.
    """
    required = {}
    for tool, candidates in toolchain.RESOLVED.items():
        names = ", ".join(f"`{' '.join(candidate)}`" for candidate in candidates)
        proof = PROOFS[tool].format(floor=toolchain.PYTHON_FLOOR)
        required[tool] = f"the first of {names} on `PATH` that states {proof}"
    for tool in toolchain.BY_NAME:
        required[tool] = "found on `PATH` under this name"
    for tool, floor in toolchain.tool_floors().items():
        required[tool] = "installed; no version floor" if floor is None \
            else f"version {floor} or newer"
    return required


def toolchain_table(required, asserted):
    """Render the declared hook tools and what the matrix asserts for each as a Markdown table."""
    rows = ["| Tool | The hook policy requires | Platform Neutrality job |",
            "| :--- | :--- | :--- |"]
    for tool, requirement in required.items():
        coverage = "asserted on every leg" if tool in asserted else "reported, not asserted"
        rows.append(f"| `{tool}` | {requirement} | {coverage} |")
    return "\n".join(rows)


def current_toolchain_table():
    """Render the table from the declarations and the workflow of this checkout."""
    workflow = (ROOT / WORKFLOW).read_text(encoding="utf-8")
    return toolchain_table(declared_requirements(hook_toolchain()), asserted_tools(workflow))


def marked_table(page):
    """Return the table the page carries between its markers, and the text around it."""
    before, start, rest = page.partition(TABLE_START + "\n")
    table, end, after = rest.partition("\n" + TABLE_END)
    if not start or not end:
        raise ValueError(f"{PAGE.as_posix()}: no {TABLE_START} ... {TABLE_END} block")
    return table, before + start, end + after


def failure_blocks(output):
    """Return one printable block per failing case, bounded in count and in length."""
    blocks = []
    for part in output.split("\n" + CASE_RULE + "\n")[1:]:
        if len(blocks) >= MAX_FAILURE_BLOCKS:
            break
        end = SUMMARY_RULE.search(part)
        body = part[:end.start()] if end else part
        blocks.append("\n".join(body.rstrip().splitlines()[:MAX_FAILURE_BLOCK_LINES]))
    return blocks


class ChildCrashed(AssertionError):
    """A self-test child that a signal ended on its attempt and on the retry."""


def exit_signal(returncode):
    """Return the name of the signal a return code reports, or None when the child exited."""
    if os.name == "nt" or returncode is None or returncode >= 0:
        return None
    try:
        return signal.Signals(-returncode).name
    except ValueError:
        return f"signal {-returncode}"


def child_version(path, name):
    """Return the first line the program at path states as its version, or why it states none.

    The probe is the hook policy's own (toolchain.stated_version): one bounded process.
    """
    toolchain = hook_toolchain()
    try:
        return toolchain.stated_version([path, *VERSION_ARGS.get(name, ("--version",))],
                                        VERSION_LINE) or "states no version"
    except toolchain.HookError as error:
        return "version unknown: " + " ".join(str(error).split())[:MAX_VERSION_REASON]


def child_identity(program):
    """Return which program a child runs: its kind, its resolved path and its stated version."""
    name = Path(str(program)).stem.lower()
    kind = "Python launcher" if name.startswith("python") or name == "py" else name
    found = shutil.which(str(program))
    if found is None:
        return f"{kind}, {program} (not on PATH)"
    path = os.path.realpath(found)
    return f"{kind}, {path}, {child_version(path, name)}"


def calling_test():
    """Return the id of the test whose frame started the child, or a note that none did."""
    frame = inspect.currentframe()
    for _ in range(MAX_CALLER_FRAMES):
        if frame is None:
            break
        owner = frame.f_locals.get("self", frame.f_locals.get("cls"))
        if isinstance(owner, unittest.TestCase):
            return owner.id()
        if isinstance(owner, type) and issubclass(owner, unittest.TestCase):
            return f"{owner.__module__}.{owner.__qualname__} (class setup)"
        frame = frame.f_back
    return "not started by a test"


def quoted(stream):
    """Return the tail of one captured stream as indented report lines."""
    if stream is None:
        return " (not captured)"
    text = stream.decode(errors="replace") if isinstance(stream, bytes) else stream
    if not text:
        return " (empty)"
    tail = text[-MAX_CHILD_OUTPUT:].splitlines()[-MAX_CHILD_LINES:]
    return "".join("\n      " + line for line in tail)


def child_report(argv, attempts, outcome):
    """Print one child report to stderr between the markers and return its body lines."""
    body = [f"  outcome: {outcome}", f"  test: {calling_test()}",
            f"  child: {child_identity(argv[0])}",
            "  argv: " + repr([str(part) for part in argv])[:MAX_CHILD_OUTPUT]]
    for number, attempt in enumerate(attempts, 1):
        name = exit_signal(attempt.returncode)
        ended = f"killed by {name}" if name else "exited"
        body.append(f"  attempt {number}: {ended} (return code {attempt.returncode})")
        body.append("    stdout:" + quoted(attempt.stdout))
        body.append("    stderr:" + quoted(attempt.stderr))
    print("", CHILD_REPORT_START, *body, CHILD_REPORT_END, sep="\n", file=sys.stderr, flush=True)
    return body


def run_child(argv, **options):
    """Run one child of a self-test as subprocess.run does; start it once more after a signal.

    Options are subprocess.run's, and ``timeout`` is required: a hung child must fail its
    test, not hold the suite (HISS-02). Standard input comes from ``input``, which the retry
    sends again; ``check`` applies to the attempt that counts. A child that exits, with any
    code, is returned at once. One that a signal ends is started again and a report of both
    attempts is printed; the retry's result is returned when it exits. When a signal ends the
    retry too, ChildCrashed fails the calling test with the same report: a crash is never a
    pass.
    """
    if options.get("timeout") is None:
        raise ValueError("run_child needs a timeout: a hung child must fail its test (HISS-02)")
    check = options.pop("check", False)
    first = subprocess.run(argv, check=False, **options)
    final = first
    if exit_signal(first.returncode) is not None:
        try:
            final = subprocess.run(argv, check=False, **options)
        except (OSError, subprocess.TimeoutExpired) as error:
            child_report(argv, [first], f"the retry did not finish: {error}")
            raise
        crashed = exit_signal(final.returncode) is not None
        outcome = "a signal ended the retry too" if crashed else \
            f"the retry exited {final.returncode}"
        body = child_report(argv, [first, final], outcome)
        if crashed:
            raise ChildCrashed("\n".join([f"{argv[0]}: a signal ended both attempts (#809)",
                                          *body]))
    if check and final.returncode:
        raise subprocess.CalledProcessError(final.returncode, argv, final.stdout, final.stderr)
    return final


def child_reports(output):
    """Return the child reports a suite printed, in order."""
    return [match.group(0) for match in CHILD_REPORT.finditer(output)]


def run_suite(path):
    """Execute one suite and return (ok, ran, skipped, reasons, failed, tail, children)."""
    try:
        result = subprocess.run(
            [sys.executable, "-B", str(path)],
            cwd=ROOT, capture_output=True, text=True,
            timeout=SUITE_TIMEOUT_SECONDS, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        return False, 0, 0, [], [], f"{type(error).__name__}: {error}", []
    output = result.stdout + result.stderr
    summaries = list(RAN.finditer(output))
    ran_match = summaries[-1] if summaries else None
    ran = int(ran_match.group(1)) if ran_match else 0
    # Read the skip count from the status line that follows the final summary, so a nested
    # suite's "OK (skipped=N)" cannot be mistaken for this one's.
    tail_text = output[ran_match.end():] if ran_match else ""
    skipped_match = SKIPPED.search(tail_text)
    skipped = int(skipped_match.group(1)) if skipped_match else 0
    reasons = SKIP_REASON.findall(output)
    failed = list(dict.fromkeys(FAILED_CASE.findall(output)))
    tail = "\n\n".join(failure_blocks(output)) or "\n".join(output.splitlines()[-25:])
    # No summary line means the suite died before unittest reported: treat as failure even
    # if the exit code says otherwise, because zero tests is not a pass.
    ok = result.returncode == 0 and ran_match is not None
    return ok, ran, skipped, reasons, failed, tail, child_reports(output)


def report(name, ok, ran, skipped, reasons, children=()):
    """Print one suite's line, every skip reason it gave and every child report it printed."""
    status = "PASS" if ok else "FAIL"
    print(f"[{status}] {name}: {ran - skipped} executed, {skipped} skipped, {ran} collected")
    for test, reason in reasons:
        print(f"         skipped {test}: {reason}")
    if not children:
        return
    shown = min(len(children), MAX_CHILD_REPORTS)
    print(f"         signal exits of a child: {len(children)}, the first {shown} reported below")
    for block in children[:MAX_CHILD_REPORTS]:
        print(block)
    print(f"::warning title=Self-test child ended by a signal::{name}: {len(children)} child "
          "process(es) ended by a signal; each report in this step's log names the child and "
          "both attempts (#809)")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--min-executed", type=int, default=1,
        help="fail if fewer than this many tests actually executed across all suites",
    )
    args = parser.parse_args(argv)

    total_executed = 0
    failures = []
    for suite in SUITES:
        if not (ROOT / suite).is_file():
            print(f"[FAIL] {suite.as_posix()}: missing")
            failures.append(suite.as_posix())
            continue
        ok, ran, skipped, reasons, failed, tail, children = run_suite(suite)
        report(suite.as_posix(), ok, ran, skipped, reasons, children)
        total_executed += ran - skipped
        if not ok:
            failures.append(suite.as_posix())
            for kind, test in failed:
                print(f"         failed {kind} {test}")
            print(tail)

    print(f"\nTotal executed: {total_executed} (floor {args.min_executed})")
    if failures:
        print(f"::error::harness self-tests failed on this platform: {', '.join(failures)}")
        return 1
    if total_executed < args.min_executed:
        print(
            f"::error::only {total_executed} tests executed on this platform, below the floor of "
            f"{args.min_executed}. Every suite exited zero, so this is a silent loss of coverage, "
            f"not a failure: the gate ran and checked almost nothing."
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
