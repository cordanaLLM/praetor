#!/usr/bin/env python3
"""Hermetic tests for the npm audit gate (scripts/npm_audit_gate.py).

The gate's audit runner is injected, so no case starts npm or contacts the registry. Exceptions
files are written under a temporary directory. SecurityWiringTests reads
.github/workflows/security.yml and pins the step that runs the gate, so a step that went back
to plain `npm audit` or dropped a lock directory fails here instead of passing silently.
"""

from __future__ import annotations

import datetime
import importlib.util
import json
import re
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("npm_audit_gate", ROOT / "scripts" / "npm_audit_gate.py")
gate_module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate_module)

TODAY = datetime.date(2026, 10, 3)
BRACES = "GHSA-vfj7-8cjw-p6xm"
WORKFLOW = ROOT / ".github" / "workflows" / "security.yml"
GATED_LOCKS = ["tools/markdownlint", "tools/figures", "editors/vscode", "docs/presets/starlight"]


def advisory(name: str, ghsa: str = BRACES, severity: str = "high", source: int = 1240992) -> dict:
    return {"source": source, "name": name, "url": f"https://github.com/advisories/{ghsa}",
            "severity": severity, "range": "<=3.0.3"}


def report(*vias: object) -> str:
    """An npm audit --json report whose packages carry the given via entries."""
    vulnerabilities = {f"pkg{i}": {"severity": "high", "via": [via]} for i, via in enumerate(vias)}
    return json.dumps({"auditReportVersion": 2, "vulnerabilities": vulnerabilities})


def exception(**overrides: object) -> dict:
    entry = {"advisory": BRACES, "package": "braces", "locks": ["tools/markdownlint"],
             "reason": "only repository-declared globs reach it", "expires": "2026-11-02"}
    entry.update(overrides)
    return entry


class GateCase(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)

    def write_exceptions(self, *entries: object) -> Path:
        path = Path(self.tmp.name) / "exceptions.json"
        path.write_text(json.dumps({"exceptions": list(entries)}), encoding="utf-8")
        return path

    def run_gate(self, reports: dict, exceptions: Path, today: datetime.date = TODAY):
        def runner(lock_dir: Path):
            return 1, reports[lock_dir.relative_to(ROOT).as_posix()], ""
        return gate_module.gate(sorted(reports), exceptions, today, ROOT, runner)


class FindingTests(GateCase):
    def test_positive_covered_finding_passes(self) -> None:
        status, lines = self.run_gate({"tools/markdownlint": report(advisory("braces"))}, self.write_exceptions(exception()))
        self.assertEqual(status, 0, lines)
        self.assertIn("1 covered by a reviewed exception, 0 not", lines[-1])

    def test_negative_uncovered_finding_fails_and_names_it(self) -> None:
        status, lines = self.run_gate({"tools/markdownlint": report(advisory("braces"))}, self.write_exceptions())
        self.assertEqual(status, 1)
        self.assertIn(f"FAIL tools/markdownlint: braces {BRACES} has no reviewed exception", lines)

    def test_negative_exception_for_another_lock_does_not_cover(self) -> None:
        exceptions = self.write_exceptions(exception(locks=["docs/presets/starlight"]))
        status, _ = self.run_gate({"tools/markdownlint": report(advisory("braces"))}, exceptions)
        self.assertEqual(status, 1)

    def test_negative_exception_for_another_package_does_not_cover(self) -> None:
        status, _ = self.run_gate({"tools/markdownlint": report(advisory("braces"))}, self.write_exceptions(exception(package="micromatch")))
        self.assertEqual(status, 1)

    def test_boundary_exception_holds_through_its_expiry_day(self) -> None:
        reports, exceptions = {"tools/markdownlint": report(advisory("braces"))}, self.write_exceptions(exception())
        self.assertEqual(self.run_gate(reports, exceptions, datetime.date(2026, 11, 2))[0], 0)
        status, lines = self.run_gate(reports, exceptions, datetime.date(2026, 11, 3))
        self.assertEqual(status, 1)
        self.assertTrue(any("exception expired on 2026-11-02" in line for line in lines), lines)
        self.assertFalse(any("matched no finding" in line for line in lines), lines)

    def test_boundary_moderate_and_transitive_entries_are_not_gated(self) -> None:
        reports = {"tools/markdownlint": report(advisory("braces", severity="moderate"), "micromatch")}
        status, lines = self.run_gate(reports, self.write_exceptions())
        self.assertEqual(status, 0, lines)

    def test_boundary_critical_is_gated(self) -> None:
        status, _ = self.run_gate({"tools/markdownlint": report(advisory("braces", severity="critical"))}, self.write_exceptions())
        self.assertEqual(status, 1)

    def test_boundary_unused_exception_is_a_note_not_a_failure(self) -> None:
        status, lines = self.run_gate({"tools/markdownlint": report()}, self.write_exceptions(exception()))
        self.assertEqual(status, 0)
        self.assertTrue(lines[0].startswith("note: exception for braces"), lines)

    def test_boundary_advisory_without_ghsa_uses_npm_source(self) -> None:
        via = {"source": 4242, "name": "left-pad", "url": "https://www.npmjs.com/advisories/4242", "severity": "high"}
        exceptions = self.write_exceptions(exception(advisory="npm:4242", package="left-pad"))
        status, lines = self.run_gate({"tools/markdownlint": report(via)}, exceptions)
        self.assertEqual(status, 0, lines)


class ReadErrorTests(GateCase):
    def test_negative_report_without_json_is_a_read_error(self) -> None:
        status, lines = self.run_gate({"tools/markdownlint": "npm ERR! network"}, self.write_exceptions())
        self.assertEqual(status, 2)
        self.assertIn("without a JSON report", lines[0])

    def test_negative_report_with_error_object_is_a_read_error(self) -> None:
        status, _ = self.run_gate({"tools/markdownlint": json.dumps({"error": {"code": "ENOLOCK"}})}, self.write_exceptions())
        self.assertEqual(status, 2)

    def test_negative_malformed_exceptions_are_read_errors(self) -> None:
        cases = {
            "missing reason": {k: v for k, v in exception().items() if k != "reason"},
            "advisory id": exception(advisory="CVE-2026-0001"),
            "empty locks": exception(locks=[]),
            "date": exception(expires="next month"),
            "beyond the cap": exception(expires="2027-01-02"),
            "blank reason": exception(reason="  "),
        }
        for name, entry in cases.items():
            with self.subTest(name):
                status, lines = self.run_gate({"tools/markdownlint": report()}, self.write_exceptions(entry))
                self.assertEqual(status, 2, lines)

    def test_boundary_expiry_exactly_at_the_cap_is_accepted(self) -> None:
        at_cap = (TODAY + datetime.timedelta(days=gate_module.MAX_EXCEPTION_DAYS)).isoformat()
        status, lines = self.run_gate({"tools/markdownlint": report()}, self.write_exceptions(exception(expires=at_cap)))
        self.assertEqual(status, 0, lines)

    def test_negative_exceptions_file_without_list_is_a_read_error(self) -> None:
        path = Path(self.tmp.name) / "exceptions.json"
        path.write_text(json.dumps({"exception": []}), encoding="utf-8")
        self.assertEqual(self.run_gate({"tools/markdownlint": report()}, path)[0], 2)


class CommittedListTests(unittest.TestCase):
    def test_committed_exceptions_validate(self) -> None:
        document = json.loads(gate_module.DEFAULT_EXCEPTIONS.read_text(encoding="utf-8"))
        entries = document["exceptions"]
        # Judged on the earliest expiry day, so the structure check never turns red by date alone;
        # the gate itself fails an expired exception that still matches a finding.
        earliest = min((datetime.date.fromisoformat(e["expires"]) for e in entries), default=TODAY)
        loaded = gate_module.load_exceptions(gate_module.DEFAULT_EXCEPTIONS, earliest)
        for entry in loaded:
            for lock in entry["locks"]:
                self.assertIn(lock, GATED_LOCKS, f"{entry['advisory']} names a lock the workflow does not audit")


class SecurityWiringTests(unittest.TestCase):
    def test_workflow_runs_the_gate_over_every_lock(self) -> None:
        text = WORKFLOW.read_text(encoding="utf-8")
        match = re.search(r"python3 -B scripts/npm_audit_gate\.py ([^\n]+)", text)
        self.assertIsNotNone(match, "security.yml no longer runs scripts/npm_audit_gate.py")
        self.assertEqual(match.group(1).split(), GATED_LOCKS)
        self.assertNotRegex(text, r"\n\s*\(cd [^)]*npm audit", "a plain npm audit loop bypasses the exceptions")
        for lock in GATED_LOCKS:
            self.assertTrue((ROOT / lock / "package-lock.json").is_file(), f"{lock} has no package-lock.json")


if __name__ == "__main__":
    unittest.main()
