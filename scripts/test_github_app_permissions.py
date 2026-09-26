#!/usr/bin/env python3
"""The GitHub App permission matrix documents exactly what the App manifest requests.

`.config/github-app/permissions.md` listed six scopes while `manifest.json` requested eight,
omitting both extra write scopes (`issues`, `statuses`), so an operator reviewing the matrix
before creating the App approved less than the manifest would grant. This pins the matrix to
the manifest: one row per `default_permissions` key, at the level the manifest requests.
"""

import json
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / ".config/github-app/manifest.json"
MATRIX = ROOT / ".config/github-app/permissions.md"
LEVELS = {"write": "Read & Write", "read": "Read-only"}
MAX_ROWS = 64
ROW = re.compile(r"^\| \*\*`([a-z_]+)`\*\* \| ([^|]+?) \|", re.M)


def matrix_rows(text):
    """(scope, access level) for every row of the permissions table, in document order."""
    rows = ROW.findall(text)
    if len(rows) > MAX_ROWS:
        raise ValueError(f"permission matrix has more than {MAX_ROWS} rows")
    return rows


def mismatches(rows, permissions):
    """Every way the documented rows differ from the manifest's default_permissions."""
    problems = []
    documented = {}
    for scope, level in rows:
        if scope in documented:
            problems.append(f"{scope} is documented twice")
        documented[scope] = level
    for scope, requested in sorted(permissions.items()):
        expected = LEVELS.get(requested)
        if expected is None:
            problems.append(f"{scope} requests unknown level {requested!r}")
        elif scope not in documented:
            problems.append(f"{scope} ({requested}) is requested but not documented")
        elif documented[scope] != expected:
            problems.append(f"{scope} is documented as {documented[scope]!r}, manifest requests {expected!r}")
    problems.extend(f"{scope} is documented but not requested"
                    for scope in sorted(documented.keys() - permissions.keys()))
    return problems


class GitHubAppPermissions(unittest.TestCase):
    def test_matrix_documents_every_requested_scope_at_its_level(self):
        permissions = json.loads(MANIFEST.read_text(encoding="utf-8"))["default_permissions"]
        rows = matrix_rows(MATRIX.read_text(encoding="utf-8"))
        self.assertEqual(len(rows), len(permissions))
        self.assertEqual(mismatches(rows, permissions), [])

    def test_missing_extra_and_duplicate_rows_are_reported(self):
        permissions = {"issues": "write", "metadata": "read"}
        self.assertEqual(mismatches([("metadata", "Read-only")], permissions),
                         ["issues (write) is requested but not documented"])
        rows = [("issues", "Read & Write"), ("metadata", "Read-only"), ("pages", "Read-only")]
        self.assertEqual(mismatches(rows, permissions), ["pages is documented but not requested"])
        rows = [("issues", "Read & Write"), ("issues", "Read & Write"), ("metadata", "Read-only")]
        self.assertEqual(mismatches(rows, permissions), ["issues is documented twice"])

    def test_read_and_write_levels_must_match_exactly(self):
        self.assertEqual(mismatches([("statuses", "Read-only")], {"statuses": "write"}),
                         ["statuses is documented as 'Read-only', manifest requests 'Read & Write'"])
        self.assertEqual(mismatches([("metadata", "Read & Write")], {"metadata": "read"}),
                         ["metadata is documented as 'Read & Write', manifest requests 'Read-only'"])
        self.assertEqual(mismatches([("checks", "Read & Write")], {"checks": "admin"}),
                         ["checks requests unknown level 'admin'"])
        self.assertEqual(mismatches([], {}), [])

    def test_row_parser_reads_only_table_scope_rows(self):
        text = ("| Scope | Access Level | Operational Rationale |\n| :--- | :--- | :--- |\n"
                "| **`checks`** | Read & Write | Publishes | x |\n"
                "The `contents` scope is mentioned in prose.\n")
        self.assertEqual(matrix_rows(text), [("checks", "Read & Write")])
        with self.assertRaises(ValueError):
            matrix_rows("| **`a`** | Read-only | x |\n" * (MAX_ROWS + 1))


if __name__ == "__main__":
    unittest.main()
