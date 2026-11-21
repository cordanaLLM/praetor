# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Regression tests for the short-flag rule's shell-state veto."""

import sys
import unittest
from pathlib import Path

_HOOKS = Path(__file__).resolve().parent
if str(_HOOKS) not in sys.path:
    sys.path.insert(0, str(_HOOKS))

import block_evasion

_ROWS = (
    (r"printf '%s\n' 'git commit -n; set a message'", False, "literal-state-keyword"),
    ("'set' x; git commit -m x && tail -n 1", True, "quoted-state-command"),
    (r"echo 'git commit -n; progress 100%; retry'", False, "quoted-percent-progress"),
    ("echo x; git commit -n", True, "active-commit-after-semicolon"),
)


class BlockEvasionStateVeto(unittest.TestCase):
    """Allow literal output and retain the whole-command scan for quoted set."""

    def test_printed_state_keyword(self):
        """Each row is a short-flag veto boundary for quoted shell state text."""
        pattern = block_evasion.READ_ONLY_EXEMPT_PATTERNS[0]
        for command, blocked, row_id in _ROWS:
            with self.subTest(row_id):
                spans = block_evasion.command_segments(command, pattern)
                judged = block_evasion.without_read_only_words(command)
                self.assertIs(
                    block_evasion.search_command(pattern, judged, spans, pattern),
                    blocked,
                )


if __name__ == "__main__":
    unittest.main()
