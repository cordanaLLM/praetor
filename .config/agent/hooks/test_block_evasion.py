# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Regression tests for the short-flag rule's shell-state veto."""

import pytest

import block_evasion


@pytest.mark.parametrize(
    ("command", "blocked"),
    [
        (r"printf '%s\n' 'git commit -n; set a message'", False),
        ("'set' x; git commit -m x && tail -n 1", True),
        (r"echo 'git commit -n; progress 100%; retry'", False),
        ("echo x; git commit -n", True),
    ],
    ids=[
        "literal-state-keyword",
        "quoted-state-command",
        "quoted-percent-progress",
        "active-commit-after-semicolon",
    ],
)
def test_printed_state_keyword(command, blocked):
    """Allow literal output and retain the whole-command scan for quoted set."""
    pattern = block_evasion.READ_ONLY_EXEMPT_PATTERNS[0]
    spans = block_evasion.command_segments(command, pattern)
    judged = block_evasion.without_read_only_words(command)
    assert block_evasion.search_command(pattern, judged, spans, pattern) is blocked
