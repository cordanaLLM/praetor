# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
"""Check emitted Python without importing or executing the emitted programs.

Rule source: Pelorus suppression-marker and empty-except checks.
"""

import ast
import io
import json
from pathlib import Path
import re
import sys
import tokenize


NOSEMGREP = re.compile(
    r"nosemgrep:\s*[A-Za-z0-9_.-]+(?:\s*,\s*[A-Za-z0-9_.-]+)*\s+-\s+\S.*"
)


def python_findings(path):
    """Return suppression and empty-handler findings for one Python source file."""
    source = path.read_text(encoding="utf-8")
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
    """Walk an emitted tree and report findings as JSON; parse or I/O errors fail."""
    root = Path(sys.argv[1])
    findings = []
    for path in sorted(root.rglob("*.py")):
        findings.extend(
            f"{path.relative_to(root)}:{finding}" for finding in python_findings(path)
        )
    print(json.dumps(findings))


if __name__ == "__main__":
    main()
