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
            for event in ("Stop", "AfterAgent"):
                first = ADAPTER.respond({"hook_event_name": event})
                self.assertEqual(first["decision"], "block")
                repeated = ADAPTER.respond({"hook_event_name": event, "stop_hook_active": True})
                self.assertIs(repeated["continue"], False)
                self.assertIn("Checkpoint remains incomplete", repeated["systemMessage"])

    def test_tool_feedback_preserves_original_tool_result(self):
        with mock.patch.object(ADAPTER, "checkpoint", return_value={
                "enabled": True, "due": True, "actions": ["commit"]}):
            for event in ("PostToolUse", "AfterTool"):
                result = ADAPTER.respond({"hook_event_name": event})
                self.assertNotIn("decision", result)
                self.assertNotIn("continue", result)
                self.assertEqual(result["hookSpecificOutput"]["hookEventName"], event)
                self.assertIn("additionalContext", result["hookSpecificOutput"])

    def test_clean_disabled_and_failed_checks_remain_distinct(self):
        for event in ("Stop", "AfterAgent", "PostToolUse", "AfterTool"):
            for enabled in (True, False):
                with mock.patch.object(ADAPTER, "checkpoint", return_value={
                        "enabled": enabled, "due": False, "actions": []}):
                    self.assertEqual(ADAPTER.respond({"hook_event_name": event}), {})
            with mock.patch.object(ADAPTER, "checkpoint", side_effect=ValueError("probe failed")):
                result = ADAPTER.respond({"hook_event_name": event})
                if event in ("Stop", "AfterAgent"):
                    self.assertEqual(result["decision"], "block")
                    self.assertIn("could not be verified", result["reason"])
                else:
                    self.assertIn("could not be verified", result["hookSpecificOutput"]["additionalContext"])

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
        policy["commit_after_files"] = 1
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

    def run_registered(self, settings, key, payload):
        spec = json.loads((ROOT / settings).read_text())
        hook = spec["hooks"][key][0]
        command = hook["hooks"][0]["command"]
        nested = self.root / "nested path with spaces"
        nested.mkdir(exist_ok=True)
        result = subprocess.run(["/bin/sh", "-c", command], cwd=nested,
                                input=json.dumps(payload), text=True,
                                capture_output=True, timeout=60, check=False)
        return result

    def test_native_settings_units_and_registered_commands(self):
        claude = json.loads((ROOT / ".claude/settings.json").read_text())["hooks"]
        gemini = json.loads((ROOT / ".gemini/settings.json").read_text())["hooks"]
        self.assertEqual(claude["PreToolUse"][0]["hooks"][0]["timeout"], 15)
        self.assertEqual(claude["PostToolUse"][0]["hooks"][0]["timeout"], 60)
        self.assertEqual(claude["Stop"][0]["hooks"][0]["timeout"], 60)
        self.assertEqual(gemini["BeforeTool"][0]["hooks"][0]["timeout"], 15000)
        self.assertEqual(gemini["AfterTool"][0]["hooks"][0]["timeout"], 60000)
        self.assertEqual(gemini["AfterAgent"][0]["hooks"][0]["timeout"], 60000)
        codex = json.loads((ROOT / ".codex/hooks.json").read_text())["hooks"]
        self.assertEqual(codex["PreToolUse"][0]["hooks"][0]["timeout"], 15)
        self.assertIn("codex_pre_tool.py", codex["PreToolUse"][0]["hooks"][0]["command"])
        self.assertIn("checkpoint.py", claude["Stop"][0]["hooks"][0]["command"])
        self.assertIn("checkpoint.py", gemini["AfterAgent"][0]["hooks"][0]["command"])

    def test_registered_guards_and_checkpoint_lifecycle(self):
        for settings, key, tool in ((".claude/settings.json", "PreToolUse", "Bash"),
                                    (".gemini/settings.json", "BeforeTool", "run_shell_command"),
                                    (".codex/hooks.json", "PreToolUse", "Bash")):
            allowed = {"hook_event_name": key, "tool_name": tool,
                       "tool_input": {"command": "git status"}}
            denied = {**allowed, "tool_input": {"command": "git commit --no-verify"}}
            self.assertEqual(self.run_registered(settings, key, allowed).returncode, 0)
            self.assertEqual(self.run_registered(settings, key, denied).returncode, 2)

        (self.root / "README.md").write_text("dirty\n")
        for settings, key, event in ((".claude/settings.json", "PostToolUse", "PostToolUse"),
                                     (".gemini/settings.json", "AfterTool", "AfterTool")):
            result = self.run_registered(settings, key, {"hook_event_name": event})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("additionalContext", json.loads(result.stdout)["hookSpecificOutput"])
        for settings, key in ((".claude/settings.json", "Stop"),
                              (".gemini/settings.json", "AfterAgent")):
            stop = self.run_registered(settings, key, {"hook_event_name": key})
            self.assertEqual(stop.returncode, 0, stop.stderr)
            self.assertEqual(json.loads(stop.stdout)["decision"], "block")
        (self.root / "README.md").write_text("fixture\n")
        (self.root / ".workingdir").mkdir(exist_ok=True)
        (self.root / ".workingdir/private.txt").write_text("private\n")
        for settings, key, event in ((".claude/settings.json", "Stop", "Stop"),
                                     (".gemini/settings.json", "AfterAgent", "AfterAgent")):
            result = self.run_registered(settings, key, {"hook_event_name": event})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(result.stdout), {})

    def test_native_adapter_rejects_malformed_event_names_without_typeerror(self):
        for payload in ({"hook_event_name": None}, {"hook_event_name": 7},
                        {"hook_event_name": []}, {"hook_event_name": {}},
                        {"hook_event_name": "Unknown"}, {"hook_event_name": "Stop",
                         "stop_hook_active": "yes"}):
            with self.assertRaises(ValueError):
                ADAPTER.respond(payload)

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
