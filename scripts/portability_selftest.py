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
"""

import argparse
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The suites `make hooks-test` runs. Kept in this order so the output reads the same
# everywhere; the list is asserted against the Makefile by test_portability_selftest.py.
SUITES = (
    Path(".config/lefthook/scripts/test_security_scope.py"),
    Path(".config/lefthook/scripts/test_hooks.py"),
    Path(".config/lefthook/scripts/test_checkpoint.py"),
    Path("scripts/test_checkpoint_hooks.py"),
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


def run_suite(path):
    """Execute one suite and return (ok, ran, skipped, reasons, failed, tail)."""
    try:
        result = subprocess.run(
            [sys.executable, "-B", str(path)],
            cwd=ROOT, capture_output=True, text=True,
            timeout=SUITE_TIMEOUT_SECONDS, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        return False, 0, 0, [], [], f"{type(error).__name__}: {error}"
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
    return (result.returncode == 0 and ran_match is not None), ran, skipped, reasons, failed, tail


def report(name, ok, ran, skipped, reasons):
    """Print one suite's line plus every skip reason it gave."""
    status = "PASS" if ok else "FAIL"
    print(f"[{status}] {name}: {ran - skipped} executed, {skipped} skipped, {ran} collected")
    for test, reason in reasons:
        print(f"         skipped {test}: {reason}")


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
        ok, ran, skipped, reasons, failed, tail = run_suite(suite)
        report(suite.as_posix(), ok, ran, skipped, reasons)
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
