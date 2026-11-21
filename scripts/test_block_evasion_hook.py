#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Run the block_evasion pytest module under verify-all's hooks-test target."""

import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TARGET = ROOT / ".config/agent/hooks/test_block_evasion.py"


class BlockEvasionHookTests(unittest.TestCase):
    """Delegate to the pinned pytest module for short-flag veto regressions."""

    def test_block_evasion_pytest_module(self):
        """The pytest rows for quoted shell state text run in CI."""
        completed = subprocess.run(
            [sys.executable, "-B", "-m", "pytest", "-q", str(TARGET)],
            cwd=ROOT,
            check=False,
            timeout=60,
        )
        self.assertEqual(
            completed.returncode,
            0,
            f"pytest rc={completed.returncode} for {TARGET.as_posix()}",
        )


if __name__ == "__main__":
    unittest.main()