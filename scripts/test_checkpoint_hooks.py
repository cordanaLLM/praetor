#!/usr/bin/env python3
"""Real Lefthook lifecycle integration and bounded native continuation tests."""

import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(".config/agent/hooks/checkpoint.py")
SPEC = importlib.util.spec_from_file_location("checkpoint_adapter", ROOT / SCRIPT)
ADAPTER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ADAPTER)


class LifecycleOutput(unittest.TestCase):
    def test_stop_requires_checkpoint_and_repeat_reports_blocked(self):
        report = {"enabled": True, "due": True, "actions": ["commit"]}
        with mock.patch.object(ADAPTER, "checkpoint", return_value=report):
            first = ADAPTER.respond({"hook_event_name": "Stop"})
            self.assertEqual(first["decision"], "block")
            repeated = ADAPTER.respond({"hook_event_name": "Stop", "stop_hook_active": True})
            self.assertIs(repeated["continue"], False)
            self.assertIn("Checkpoint remains incomplete", repeated["systemMessage"])

    def test_tool_feedback_preserves_original_tool_result(self):
        with mock.patch.object(ADAPTER, "checkpoint", return_value={
                "enabled": True, "due": True, "actions": ["commit"]}):
            result = ADAPTER.respond({"hook_event_name": "PostToolUse"})
            self.assertNotIn("decision", result)
            self.assertNotIn("continue", result)
            self.assertIn("additionalContext", result["hookSpecificOutput"])

    def test_clean_disabled_and_failed_checks_remain_distinct(self):
        for enabled in (True, False):
            with mock.patch.object(ADAPTER, "checkpoint", return_value={
                    "enabled": enabled, "due": False, "actions": []}):
                self.assertEqual(ADAPTER.respond({"hook_event_name": "Stop"}), {})
        with mock.patch.object(ADAPTER, "checkpoint", side_effect=ValueError("probe failed")):
            result = ADAPTER.respond({"hook_event_name": "Stop"})
            self.assertEqual(result["decision"], "block")
            self.assertIn("could not be verified", result["reason"])

    def test_missing_execution_marker_and_invalid_input_rejected(self):
        with mock.patch.object(ADAPTER, "run_bounded", return_value=b"skipped job exited zero\n"):
            with self.assertRaisesRegex(ValueError, "execution result"):
                ADAPTER.checkpoint("stop")
        for payload in ([], {}, {"hook_event_name": "Stop", "stop_hook_active": "yes"}):
            with self.assertRaises(ValueError):
                ADAPTER.respond(payload)

    def test_process_output_and_time_limits_are_enforced_while_running(self):
        import sys
        with self.assertRaisesRegex(ADAPTER.HookError, "byte limit"):
            ADAPTER.run_bounded([sys.executable, "-c", "import os,time; os.write(1,b'x'*4096); time.sleep(10)"],
                                timeout=2, max_output=1024)
        with self.assertRaisesRegex(ADAPTER.HookError, "timed out"):
            ADAPTER.run_bounded([sys.executable, "-c", "import time; time.sleep(10)"],
                                timeout=0.05)
        output = ADAPTER.run_bounded([sys.executable, "-c", "import os; os.write(1,b'pass'); os.write(2,b'note')"],
                                     timeout=2, max_output=8)
        self.assertEqual(output, b"pass")


class NativeLefthook(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-checkpoint-hooks-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q", "-b", "checkpoint/fixture")
        self.git("config", "user.name", "Checkpoint Fixture")
        self.git("config", "user.email", "fixture@example.test")
        for path in (".config/lefthook", ".config/agent/hooks"):
            shutil.copytree(ROOT / path, self.root / path)
        shutil.copy(ROOT / "lefthook.yml", self.root / "lefthook.yml")
        policy = json.loads((ROOT / ".config/agent/checkpoint.json").read_text())
        policy["publish"] = False
        (self.root / ".config/agent/checkpoint.json").write_text(json.dumps(policy))
        (self.root / ".gitignore").write_text("/.workingdir/\n")
        (self.root / "README.md").write_text("fixture\n")
        self.git("add", ".")
        self.git("commit", "-q", "-s", "-m", "chore: initialize fixture")

    def git(self, *args):
        env = dict(os.environ)
        for name in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"):
            env.pop(name, None)
        result = subprocess.run(["git", *args], cwd=self.root, env=env,
                                capture_output=True, timeout=20, check=False)
        self.assertEqual(result.returncode, 0, result.stderr.decode())
        return result.stdout

    def invoke(self, payload):
        result = subprocess.run(["python3", "-B", str(self.root / SCRIPT)], cwd=self.root,
                                input=json.dumps(payload), text=True, capture_output=True,
                                timeout=60, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_actual_job_and_adapter_observe_dirty_work_without_staging(self):
        (self.root / "README.md").write_text("changed\n")
        (self.root / ".workingdir").mkdir()
        (self.root / ".workingdir/secret.txt").write_text("PRIVATE_SENTINEL\n")
        head = self.git("rev-parse", "HEAD")
        index = self.git("ls-files", "--stage")
        result = self.invoke({"hook_event_name": "Stop"})
        self.assertEqual(result["decision"], "block")
        self.assertIn("Praetor checkpoint due:", result["reason"])
        self.assertNotIn("could not be verified", result["reason"])
        self.assertNotIn("PRIVATE_SENTINEL", json.dumps(result))
        self.assertEqual(index, self.git("ls-files", "--stage"))
        self.assertEqual(head, self.git("rev-parse", "HEAD"))
        self.assertEqual((self.root / "README.md").read_text(), "changed\n")

    def test_private_only_and_unrelated_receipt_do_not_require_commit(self):
        (self.root / ".workingdir").mkdir()
        (self.root / ".workingdir/state.txt").write_text("local\n")
        (self.root / ".standards-receipt.json").write_text("unrelated\n")
        self.assertEqual(self.invoke({"hook_event_name": "Stop"}), {})


if __name__ == "__main__":
    unittest.main()
