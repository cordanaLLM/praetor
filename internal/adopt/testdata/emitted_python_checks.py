# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
"""Check emitted Python without importing or executing the emitted programs.

Rule source: Pelorus suppression-marker and empty-except checks.
"""

import ast
import io
import json
import os
from pathlib import Path
import re
import sys
import tokenize

# Sized from current emitted tree (54 entries, 140,214 max file bytes, 609,296 scanned total bytes) (HISS-02).
MAX_EMITTED_WALK_ENTRIES = 512
MAX_EMITTED_FILE_BYTES = 512 * 1024
MAX_EMITTED_TOTAL_BYTES = 2 * 1024 * 1024


NOSEMGREP = re.compile(
    r"nosemgrep:\s*[A-Za-z0-9_.-]+(?:\s*,\s*[A-Za-z0-9_.-]+)*\s+-\s+\S.*"
)


def read_file_limited(path):
    """Read at most MAX_EMITTED_FILE_BYTES + 1 bytes; exit if over bound (HISS-02)."""
    with open(path, "rb") as f:
        raw = f.read(MAX_EMITTED_FILE_BYTES + 1)
    if len(raw) > MAX_EMITTED_FILE_BYTES:
        sys.stderr.write(
            f"emitted file {path} exceeds {MAX_EMITTED_FILE_BYTES} bytes\n"
        )
        sys.exit(1)
    return raw.decode("utf-8"), len(raw)


def python_findings(path, source=None):
    """Return suppression and empty-handler findings for one Python source file."""
    if source is None:
        source, _ = read_file_limited(path)
    tree = ast.parse(source, filename=str(path))
    nodes = list(ast.walk(tree))
    starts = {
        node.lineno for node in nodes if isinstance(node, (ast.stmt, ast.expr))
    }
    comments = [
        token for token in tokenize.generate_tokens(io.StringIO(source).readline)
        if token.type == tokenize.COMMENT
    ]
    findings = suppression_findings(comments, starts)
    for node in nodes:
        if isinstance(node, ast.ExceptHandler) and empty_handler(node):
            if not any(
                node.lineno <= token.start[0] <= node.end_lineno
                and token.string.lstrip("# ").strip()
                for token in comments
            ):
                findings.append(f"{node.lineno}: empty except without explanatory comment")
    return findings


def suppression_findings(comments, starts):
    """Require nosemgrep comments on a statement start with rule IDs and a reason."""
    findings = []
    for token in comments:
        marker = token.string.partition("nosemgrep")
        if not marker[1]:
            continue
        line = token.start[0]
        if line not in starts:
            findings.append(f"{line}: marker is not on a statement or expression start line")
        if not NOSEMGREP.fullmatch(marker[1] + marker[2]):
            findings.append(f"{line}: nosemgrep needs rule IDs and a reason")
    return findings


def empty_handler(node):
    """Recognize handlers containing only pass or ellipsis expressions."""
    return all(
        isinstance(statement, ast.Pass)
        or (
            isinstance(statement, ast.Expr)
            and isinstance(statement.value, ast.Constant)
            and statement.value.value is Ellipsis
        )
        for statement in node.body
    )


def main():
    """Walk an emitted tree and report findings as JSON; bounds or errors fail."""
    root = Path(sys.argv[1])
    findings = []
    visited = 0
    py_paths = []
    for dirpath, dirnames, filenames in os.walk(root):
        visited += 1
        if visited > MAX_EMITTED_WALK_ENTRIES:
            sys.stderr.write(
                f"emitted walk exceeds {MAX_EMITTED_WALK_ENTRIES} entries\n"
            )
            sys.exit(1)
        dirnames.sort()
        for filename in sorted(filenames):
            visited += 1
            if visited > MAX_EMITTED_WALK_ENTRIES:
                sys.stderr.write(
                    f"emitted walk exceeds {MAX_EMITTED_WALK_ENTRIES} entries\n"
                )
                sys.exit(1)
            if filename.endswith(".py"):
                py_paths.append(Path(dirpath) / filename)

    py_paths.sort()
    total_bytes = 0
    for path in py_paths:
        source, nbytes = read_file_limited(path)
        total_bytes += nbytes
        if total_bytes > MAX_EMITTED_TOTAL_BYTES:
            sys.stderr.write(
                f"emitted total read exceeds {MAX_EMITTED_TOTAL_BYTES} bytes\n"
            )
            sys.exit(1)
        findings.extend(
            f"{path.relative_to(root)}:{finding}"
            for finding in python_findings(path, source=source)
        )
    print(json.dumps(findings))


if __name__ == "__main__":
    main()
