#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Run the block_evasion unittest module under verify-all's hooks-test target."""

import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TARGET = ROOT / ".config/agent/hooks/test_block_evasion.py"


class BlockEvasionHookTests(unittest.TestCase):
    """Delegate to the stdlib unittest rows for short-flag veto regressions."""

    def test_block_evasion_unittest_module(self):
        """The veto rows for quoted shell state text run in CI without a third-party runner."""
        completed = subprocess.run(
            [sys.executable, "-B", str(TARGET)],
            cwd=ROOT,
            check=False,
            timeout=60,
        )
        self.assertEqual(
            completed.returncode,
            0,
            f"(runner) rc={completed.returncode} for {TARGET.as_posix()}",
        )


if __name__ == "__main__":
    unittest.main()
