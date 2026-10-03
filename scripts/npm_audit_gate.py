#!/usr/bin/env python3
"""Fail on a high or critical npm advisory in a committed lock unless a reviewed exception covers it.

The `Go Vulnerability & AST Security Scan` job in .github/workflows/security.yml runs this for
every pull request and daily, over each lock directory it names. For each directory it runs
`npm audit --package-lock-only --json` and collects the advisories at or above high.

An advisory passes only when .config/security/npm-audit-exceptions.json names it for that
package and that lock directory, with a reason and an expiry date no later than
MAX_EXCEPTION_DAYS from today. An expired exception fails like a missing one, so every
exception is reviewed again. An exception that matches nothing is reported, so a fixed
advisory's entry gets removed, but it does not fail the run.

Exit status: 0 when every advisory is covered, 1 when one is not, 2 when the exceptions file or
an audit report cannot be read.
"""

from __future__ import annotations

import argparse
import datetime
import json
import re
import shutil
import subprocess
import sys
from collections.abc import Callable
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_EXCEPTIONS = ROOT / ".config" / "security" / "npm-audit-exceptions.json"
GATED_SEVERITIES = frozenset({"high", "critical"})
MAX_EXCEPTION_DAYS = 90
AUDIT_TIMEOUT_SECONDS = 300
# Bounds for the loops over untrusted report and file contents (HISS-02).
MAX_PACKAGES = 20000
MAX_VIA = 1000
MAX_EXCEPTIONS = 200
ADVISORY_ID = re.compile(r"^(GHSA(-[23456789cfghjmpqrvwx]{4}){3}|npm:[0-9]+)$")
GHSA_URL = re.compile(r"/advisories/(GHSA(-[23456789cfghjmpqrvwx]{4}){3})$")
REQUIRED_FIELDS = ("advisory", "package", "locks", "reason", "expires")

Runner = Callable[[Path], tuple[int, str, str]]


class GateError(Exception):
    """An exceptions file or audit report that cannot be read (exit status 2)."""


def run_npm_audit(lock_dir: Path) -> tuple[int, str, str]:
    """Run npm audit on one lock directory without installing anything."""
    # shutil.which honours PATHEXT, so Windows finds npm.cmd, which a bare "npm" would miss.
    npm = shutil.which("npm")
    if npm is None:
        raise GateError(f"{lock_dir}: npm is not on PATH")
    try:
        done = subprocess.run(
            [npm, "audit", "--package-lock-only", "--json"],
            cwd=lock_dir, capture_output=True, text=True, timeout=AUDIT_TIMEOUT_SECONDS, check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise GateError(f"{lock_dir}: npm audit did not run: {exc}") from exc
    return done.returncode, done.stdout, done.stderr


def advisory_id(via: dict) -> str:
    """Return an advisory's GHSA identifier, or npm:<source> when npm names no GHSA."""
    match = GHSA_URL.search(str(via.get("url", "")))
    if match:
        return match.group(1)
    return f"npm:{via.get('source')}"


def gated_findings(report: dict, lock: str) -> set[tuple[str, str, str]]:
    """Return (advisory, package, lock) for every advisory at or above high in one report."""
    vulnerabilities = report.get("vulnerabilities")
    if not isinstance(vulnerabilities, dict) or len(vulnerabilities) > MAX_PACKAGES:
        raise GateError(f"{lock}: npm audit report has no readable vulnerabilities object")
    findings = set()
    for entry in vulnerabilities.values():
        via = entry.get("via", []) if isinstance(entry, dict) else []
        for item in via[:MAX_VIA]:
            if isinstance(item, dict) and item.get("severity") in GATED_SEVERITIES:
                findings.add((advisory_id(item), str(item.get("name", "")), lock))
    return findings


def audit_lock(lock: str, root: Path, runner: Runner) -> set[tuple[str, str, str]]:
    """Audit one lock directory and return its gated findings."""
    code, stdout, stderr = runner(root / lock)
    try:
        report = json.loads(stdout)
    except json.JSONDecodeError as exc:
        raise GateError(f"{lock}: npm audit exited {code} without a JSON report: {stderr.strip()[-300:]}") from exc
    if not isinstance(report, dict) or "error" in report:
        raise GateError(f"{lock}: npm audit failed: {json.dumps(report)[:300]}")
    return gated_findings(report, lock)


def parse_exception(item: object, index: int, today: datetime.date) -> dict:
    """Validate one exception entry and return it with its expiry as a date."""
    if not isinstance(item, dict) or any(field not in item for field in REQUIRED_FIELDS):
        raise GateError(f"exception {index}: needs {', '.join(REQUIRED_FIELDS)}")
    if not ADVISORY_ID.match(str(item["advisory"])):
        raise GateError(f"exception {index}: advisory {item['advisory']!r} is not a GHSA or npm:<id> identifier")
    locks = item["locks"]
    if not isinstance(locks, list) or not locks or not all(isinstance(lock, str) and lock for lock in locks):
        raise GateError(f"exception {index}: locks must be a non-empty list of lock directories")
    if not str(item["reason"]).strip() or not str(item["package"]).strip():
        raise GateError(f"exception {index}: package and reason must not be empty")
    try:
        expires = datetime.date.fromisoformat(str(item["expires"]))
    except ValueError as exc:
        raise GateError(f"exception {index}: expires {item['expires']!r} is not YYYY-MM-DD") from exc
    if (expires - today).days > MAX_EXCEPTION_DAYS:
        raise GateError(f"exception {index}: expires {expires} is more than {MAX_EXCEPTION_DAYS} days away")
    return {**item, "expires": expires}


def load_exceptions(path: Path, today: datetime.date) -> list[dict]:
    """Read and validate the exceptions file."""
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise GateError(f"{path}: cannot read exceptions: {exc}") from exc
    entries = document.get("exceptions") if isinstance(document, dict) else None
    if not isinstance(entries, list) or len(entries) > MAX_EXCEPTIONS:
        raise GateError(f"{path}: needs an \"exceptions\" list of at most {MAX_EXCEPTIONS} entries")
    return [parse_exception(item, index, today) for index, item in enumerate(entries)]


def covering(finding: tuple[str, str, str], exceptions: list[dict]) -> dict | None:
    """Return the exception that names this finding's advisory, package and lock, if any."""
    advisory, package, lock = finding
    for exception in exceptions:
        if exception["advisory"] == advisory and exception["package"] == package and lock in exception["locks"]:
            return exception
    return None


def judge(findings: set[tuple[str, str, str]], exceptions: list[dict], today: datetime.date) -> tuple[list[str], list[str]]:
    """Return (failures, notes): uncovered or expired findings, and exceptions that matched nothing."""
    failures, used = [], set()
    for finding in sorted(findings):
        exception = covering(finding, exceptions)
        advisory, package, lock = finding
        if exception is None:
            failures.append(f"{lock}: {package} {advisory} has no reviewed exception")
            continue
        used.add(id(exception))
        if exception["expires"] < today:
            failures.append(f"{lock}: {package} {advisory} exception expired on {exception['expires']}; review it again")
    notes = [f"exception for {e['package']} {e['advisory']} matched no finding; remove it once the fix is locked"
             for e in exceptions if id(e) not in used]
    return failures, notes


def gate(locks: list[str], exceptions_path: Path, today: datetime.date, root: Path = ROOT,
         runner: Runner = run_npm_audit) -> tuple[int, list[str]]:
    """Audit every lock directory and return the exit status and the lines to print."""
    try:
        exceptions = load_exceptions(exceptions_path, today)
        findings = set()
        for lock in locks:
            findings |= audit_lock(lock, root, runner)
    except GateError as exc:
        return 2, [f"npm audit gate: {exc}"]
    failures, notes = judge(findings, exceptions, today)
    lines = [f"note: {note}" for note in notes] + [f"FAIL {failure}" for failure in failures]
    covered = len(findings) - len(failures)
    lines.append(f"npm audit gate: {len(locks)} locks, {len(findings)} high or critical findings, "
                 f"{covered} covered by a reviewed exception, {len(failures)} not")
    return (1 if failures else 0), lines


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("locks", nargs="+", help="lock directories relative to the repository root")
    parser.add_argument("--exceptions", type=Path, default=DEFAULT_EXCEPTIONS)
    parser.add_argument("--today", type=datetime.date.fromisoformat, default=datetime.datetime.now(datetime.timezone.utc).date(),
                        help="the date expiry is judged against (YYYY-MM-DD; default: today in UTC)")
    args = parser.parse_args(argv)
    status, lines = gate(args.locks, args.exceptions, args.today)
    print("\n".join(lines))
    return status


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
