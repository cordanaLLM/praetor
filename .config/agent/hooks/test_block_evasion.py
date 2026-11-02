# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Replay policy fixtures without running their shell text or consulting live settings."""

import contextlib
import importlib.util
import io
import json
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location(
    "block_evasion", ROOT / ".config/agent/hooks/block_evasion.py",
)
GUARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GUARD)


class EvasionReplay(unittest.TestCase):
    """Replay pre-tool evasion rules against the engine corpus and privilege fixtures."""

    def test_admin_boolean_spellings(self):
        """Verify all true boolean spellings of gh pr merge --admin are matched."""
        pattern = next(p for p in GUARD.BLOCKED_PATTERNS if "--admin" in p)
        positions = (
            "gh pr merge 123 --admin={}",
            "gh --admin={} pr merge 123",
            "gh pr --admin={} merge 123",
        )
        for value in ("1", "t", "T", "true", "True", "TRUE", "tRuE"):
            for position in positions:
                with self.subTest(value=value, position=position):
                    self.assertTrue(GUARD.search(pattern, position.format(value)))
        for value in ("0", "false", "False", "trueish", "turtle", "10"):
            with self.subTest(value=value):
                self.assertFalse(GUARD.search(pattern, positions[0].format(value)))

    def test_wrapper_option_arities(self):
        """Verify wrapper and launcher option arities match privilege escalators."""
        replay = json.loads(
            (ROOT / "internal/agenthook/testdata/privilege-wrappers.json").read_text()
        )
        # Match the privilege rule itself: DEV-01 would otherwise mask missing wrappers.
        for wrapper in replay["commands"]:
            for escalator in ("sudo", "doas"):
                command = f"{wrapper} {escalator} praetorctl conform /srv/dev"
                with self.subTest(command=command):
                    self.assertTrue(GUARD.search(GUARD.BLOCKED_PATTERNS[-1], command))

        for launcher in replay.get("launchers", ()):
            command = f"{launcher} 'praetorctl' adopt /srv/dev"
            with self.subTest(launcher=command):
                self.assertTrue(GUARD.search(GUARD.BLOCKED_PATTERNS[-1], command))

        for command in replay["data_only"]:
            with self.subTest(data=command):
                self.assertFalse(GUARD.search(GUARD.BLOCKED_PATTERNS[-1], command))

    def test_engine_corpus(self):
        """Verify the Python guard agrees with every non-operator engine corpus case."""
        corpus = ROOT / "internal/agenthook/testdata/pre-tool/cases.json"
        cases = json.loads(corpus.read_text())
        for case in cases[:1024]:
            with self.subTest(case=case["name"]), mock.patch.object(
                GUARD, "check_admission", return_value=True,
            ), contextlib.redirect_stderr(io.StringIO()):
                stream = io.BytesIO(json.dumps(case["payload"]).encode())
                try:
                    command = GUARD.read_json_command(stream)
                    allowed = GUARD.audit_command(command)
                except (TypeError, ValueError):
                    allowed = False
                self.assertEqual(allowed, case["allow"] or case.get("operator", False))


if __name__ == "__main__":
    unittest.main()
