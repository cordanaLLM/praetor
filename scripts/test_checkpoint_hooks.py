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
SCOPE_SPEC = importlib.util.spec_from_file_location("checkpoint_scope", ROOT / ".config/lefthook/scripts/checkpoint_scope.py")
SCOPE = importlib.util.module_from_spec(SCOPE_SPEC)
SCOPE_SPEC.loader.exec_module(SCOPE)


class LifecycleOutput(unittest.TestCase):
    def setUp(self):
        self.state = mock.patch.object(ADAPTER, "verify_state")
        self.state_probe = self.state.start()
        self.addCleanup(self.state.stop)

    def test_stop_requires_state_verification_before_checkpoint_observation(self):
        for event in ("Stop", "AfterAgent"):
            with mock.patch.object(ADAPTER, "checkpoint", return_value={
                    "enabled": False, "due": False, "actions": []}) as observe:
                self.state_probe.side_effect = ValueError("state snapshot is stale")
                result = ADAPTER.respond({"hook_event_name": event})
                self.assertEqual(result["decision"], "block")
                self.assertIn("state snapshot is stale", result["reason"])
                self.assertIn("state sync", result["reason"])
                observe.assert_not_called()
        self.state_probe.assert_called()

    def test_tool_feedback_does_not_claim_state_verification(self):
        with mock.patch.object(ADAPTER, "checkpoint", return_value={
                "enabled": True, "due": False, "actions": []}):
            for event in ("PostToolUse", "AfterTool"):
                self.assertEqual(ADAPTER.respond({"hook_event_name": event}), {})
        self.state_probe.assert_not_called()

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


class StateResult(unittest.TestCase):
    def test_state_requires_one_positive_execution_result(self):
        marker = ADAPTER.STATE_MARKER
        valid = marker + '{"schema_version":1,"verified":true}\n'
        with mock.patch.object(ADAPTER, "run_bounded", return_value=valid.encode()) as run:
            self.assertIsNone(ADAPTER.verify_state())
        self.assertEqual(run.call_args.kwargs["timeout"], 20)
        for raw in (b"skipped job exited zero\n", (valid * 2).encode(),
                    (marker + "null\n").encode(), (marker + "{\n").encode(),
                    (marker + '{"schema_version":true,"verified":true}').encode(),
                    (marker + '{"schema_version":1,"verified":false}').encode(),
                    (marker + '{"schema_version":1,"verified":1}').encode()):
            with mock.patch.object(ADAPTER, "run_bounded", return_value=raw):
                with self.assertRaises(ValueError):
                    ADAPTER.verify_state()


class NativeLefthook(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="praetor-state-hook-cli-")
        cls.addClassCleanup(cls.build.cleanup)
        cls.binary = Path(cls.build.name) / "praetorctl"
        subprocess.run(["go", "build", "-o", str(cls.binary), "./cmd/standardsctl"],
                       cwd=ROOT, capture_output=True, timeout=180, check=True)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-checkpoint-hooks-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        SCOPE.ROOT = self.root
        self.git("init", "-q", "-b", "checkpoint/fixture")
        self.git("config", "user.name", "Checkpoint Fixture")
        self.git("config", "user.email", "fixture@example.test")
        for path in (".config/lefthook", ".config/agent/hooks"):
            shutil.copytree(ROOT / path, self.root / path)
        shutil.copy(ROOT / "lefthook.yml", self.root / "lefthook.yml")
        policy = json.loads((ROOT / ".config/agent/checkpoint.json").read_text())
        policy["publish"] = False
        policy["require_checks"] = False
        policy["required_checks"] = []
        policy["commit_after_files"] = 1
        policy["enforce_batch_scope"] = True
        (self.root / ".config/agent/checkpoint.json").write_text(json.dumps(policy))
        (self.root / ".gitignore").write_text("/.workingdir/\n/bin/\n")
        (self.root / "Makefile").write_text(".PHONY: hook-cli\nhook-cli:\n\ttest -x bin/praetorctl\n")
        (self.root / "bin").mkdir()
        os.link(self.binary, self.root / "bin/praetorctl")
        (self.root / "README.md").write_text("fixture\n")
        self.git("add", ".")
        self.git("commit", "-q", "-s", "-m", "chore: initialize fixture")
        self.state("init")
        self.state("sync")

    def state(self, action):
        result = subprocess.run([str(self.root / "bin/praetorctl"), "state", action, "."],
                                cwd=self.root, text=True, capture_output=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

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

    def run_registered(self, settings, key, payload, entry=0):
        spec = json.loads((ROOT / settings).read_text())
        hook = spec["hooks"][key][entry]
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
        self.state("sync")
        for settings, key, event in ((".claude/settings.json", "PostToolUse", "PostToolUse"),
                                     (".gemini/settings.json", "AfterTool", "AfterTool"),
                                     (".codex/hooks.json", "PostToolUse", "PostToolUse")):
            result = self.run_registered(settings, key, {"hook_event_name": event})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("additionalContext", json.loads(result.stdout)["hookSpecificOutput"])
        for settings, key in ((".claude/settings.json", "Stop"),
                              (".gemini/settings.json", "AfterAgent"),
                              (".codex/hooks.json", "Stop")):
            stop = self.run_registered(settings, key, {"hook_event_name": key})
            self.assertEqual(stop.returncode, 0, stop.stderr)
            self.assertEqual(json.loads(stop.stdout)["decision"], "block")
        (self.root / "README.md").write_text("fixture\n")
        (self.root / ".workingdir").mkdir(exist_ok=True)
        (self.root / ".workingdir/private.txt").write_text("private\n")
        self.state("sync")
        for settings, key, event in ((".claude/settings.json", "Stop", "Stop"),
                                     (".gemini/settings.json", "AfterAgent", "AfterAgent"),
                                     (".codex/hooks.json", "Stop", "Stop")):
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
        (self.root / ".workingdir").mkdir(exist_ok=True)
        (self.root / ".workingdir/secret.txt").write_text("PRIVATE_SENTINEL\n")
        self.state("sync")
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
        (self.root / ".workingdir").mkdir(exist_ok=True)
        (self.root / ".workingdir/state.txt").write_text("local\n")
        (self.root / ".standards-receipt.json").write_text("unrelated\n")
        self.state("sync")
        self.assertEqual(self.invoke({"hook_event_name": "Stop"}), {})

    def test_stop_rejects_stale_missing_and_malformed_state_before_cadence(self):
        state_path = self.root / ".workingdir/STATE.md"
        before = state_path.read_bytes()
        (self.root / "README.md").write_text("unrecorded change\n")
        result = self.invoke({"hook_event_name": "Stop"})
        self.assertEqual(result["decision"], "block")
        self.assertIn("Praetor state could not be verified", result["reason"])
        self.assertEqual(state_path.read_bytes(), before)
        self.state("sync")
        self.assertIn("Praetor checkpoint due", self.invoke({"hook_event_name": "Stop"})["reason"])
        (self.root / ".workingdir/OPEN.md").unlink()
        missing = self.invoke({"hook_event_name": "AfterAgent"})
        self.assertIn("Praetor state could not be verified", missing["reason"])
        self.assertFalse((self.root / ".workingdir/OPEN.md").exists())
        self.state("init")
        (self.root / ".workingdir/BUGS.md").write_text("malformed ledger\n")
        malformed = self.invoke({"hook_event_name": "AfterAgent", "stop_hook_active": True})
        self.assertIs(malformed["continue"], False)
        self.assertIn("Praetor state could not be verified", malformed["stopReason"])

    def test_native_batch_scope_allows_existing_and_private_but_blocks_new(self):
        (self.root / "README.md").write_text("dirty\n")
        existing = {"hook_event_name": "PreToolUse", "tool_name": "Edit",
                    "tool_input": {"file_path": str(self.root / "README.md")}, "cwd": str(self.root)}
        self.assertEqual(SCOPE.check(existing), 0)
        new = {**existing, "tool_input": {"file_path": str(self.root / "new.go")}}
        self.assertEqual(SCOPE.check(new), 2)
        private = {**existing, "tool_input": {"file_path": str(self.root / ".workingdir/state")}}
        self.assertEqual(SCOPE.check(private), 0)

    def test_native_batch_scope_handles_deleted_and_ambiguous_paths(self):
        (self.root / "README.md").unlink()
        deleted = {"hook_event_name": "BeforeTool", "tool_name": "replace",
                   "tool_input": {"file_path": str(self.root / "README.md")}, "cwd": str(self.root)}
        self.assertEqual(SCOPE.check(deleted), 0)
        malformed = {**deleted, "tool_input": {"file_path": ["README.md", "other"]}}
        with self.assertRaises(ValueError):
            SCOPE.check(malformed)
        oversized = json.dumps(deleted) + " " * (1 << 20)
        result = subprocess.run(["python3", "-B", str(self.root / ".config/agent/hooks/checkpoint_scope.py")],
                                cwd=self.root, input=oversized, text=True, capture_output=True, timeout=60)
        self.assertEqual(result.returncode, 2)

    def test_native_batch_scope_binds_repository_and_rejects_traversal(self):
        (self.root / "README.md").write_text("dirty\n")
        nested = self.root / "nested"
        nested.mkdir()
        relative_existing = {"hook_event_name": "PreToolUse", "tool_name": "Write",
                             "tool_input": {"file_path": str(self.root / "README.md")},
                             "cwd": str(nested)}
        self.assertEqual(SCOPE.check(relative_existing), 0)
        with self.assertRaises(ValueError):
            SCOPE.check({**relative_existing, "tool_input": {"file_path": "../README.md"}})
        outside = Path(self.temp.name).parent
        with self.assertRaises(ValueError):
            SCOPE.check({**relative_existing, "cwd": str(outside)})
        link = nested / "link"
        link.symlink_to(self.root)
        with self.assertRaises(ValueError):
            SCOPE.check({**relative_existing, "tool_input": {"file_path": "link/README.md"}})

    def scope_payload(self, path="new.go", **changes):
        return {"hook_event_name": "PreToolUse", "tool_name": "Write",
                "tool_input": {"file_path": path}, "cwd": str(self.root), **changes}

    def scope_bridge(self, raw):
        return subprocess.run(["python3", "-B", str(self.root / ".config/agent/hooks/checkpoint_scope.py")],
                              cwd=self.root, input=raw, capture_output=True, timeout=20)

    def test_registered_file_guards_block_new_paths_before_write(self):
        (self.root / "README.md").write_text("dirty\n")
        before = self.git("rev-parse", "HEAD")
        for settings, event, tools in ((".claude/settings.json", "PreToolUse", ("Edit", "Write")),
                                       (".gemini/settings.json", "BeforeTool", ("replace", "write_file"))):
            for tool in tools:
                payload = self.scope_payload(tool_name=tool, hook_event_name=event)
                denied = self.run_registered(settings, event, payload, entry=1)
                self.assertEqual(denied.returncode, 2, denied.stdout + denied.stderr)
                self.assertFalse((self.root / "new.go").exists())
                for allowed in ("README.md", ".workingdir/evidence.json"):
                    payload["tool_input"]["file_path"] = allowed
                    result = self.run_registered(settings, event, payload, entry=1)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(before, self.git("rev-parse", "HEAD"))

    def test_scope_policy_disabled_legacy_missing_and_not_due(self):
        clean = self.scope_bridge(json.dumps(self.scope_payload()).encode())
        self.assertEqual(clean.returncode, 0, clean.stderr)
        path = self.root / ".config/agent/checkpoint.json"
        original = json.loads(path.read_text())
        for field in ("enabled", "enforce_batch_scope", "legacy", "missing"):
            policy = dict(original)
            if field == "legacy":
                policy.pop("enforce_batch_scope")
            elif field == "missing":
                path.unlink()
            else:
                policy[field] = False
            if field != "missing":
                path.write_text(json.dumps(policy))
            result = self.scope_bridge(json.dumps(self.scope_payload()).encode())
            self.assertEqual(result.returncode, 0, (field, result.stderr))
        path.write_text(json.dumps({**original, "enforce_batch_scope": "true"}))
        self.assertEqual(self.scope_bridge(json.dumps(self.scope_payload()).encode()).returncode, 2)

    def test_scope_rejects_ambiguous_inputs_and_missing_job(self):
        valid = json.dumps(self.scope_payload()).encode()
        malformed = [b"", b"null", b"{", b"[" * 2000 + b"]" * 2000,
                     valid[:-1] + b',"tool_name":"Edit"}', b" " * ((1 << 20) + 1)]
        for change in ({"tool_name": []}, {"tool_name": "Bash"}, {"cwd": "."},
                       {"tool_input": {"file_path": ["a", "b"]}}):
            malformed.append(json.dumps(self.scope_payload(**change)).encode())
        for raw in malformed:
            self.assertEqual(self.scope_bridge(raw).returncode, 2)
        config = self.root / ".config/lefthook/praetor.yml"
        config.write_text(config.read_text().replace("agent-checkpoint-pre-edit:", "missing-scope-job:"))
        self.assertEqual(self.scope_bridge(valid).returncode, 2)

    def test_scope_uses_root_relative_names_and_rejects_nested_git(self):
        (self.root / "README.md").write_text("dirty\n")
        nested = self.root / "nested"
        nested.mkdir()
        payload = self.scope_payload("README.md", cwd=str(nested))
        self.assertEqual(self.scope_bridge(json.dumps(payload).encode()).returncode, 2)
        payload["tool_input"]["file_path"] = ".workingdir/state"
        self.assertEqual(self.scope_bridge(json.dumps(payload).encode()).returncode, 2)
        (nested / "owned.go").write_text("dirty\n")
        payload["tool_input"]["file_path"] = "owned.go"
        self.assertEqual(self.scope_bridge(json.dumps(payload).encode()).returncode, 0)
        self.git("init", "-q", str(nested))
        self.assertEqual(self.scope_bridge(json.dumps(payload).encode()).returncode, 2)

    def test_normal_checkpoint_output_omits_path_inventory(self):
        (self.root / "README.md").write_text("dirty\n")
        result = SCOPE.inspect_checkpoint(self.root, "tool")
        self.assertNotIn("public_paths", result)
        scoped = SCOPE.inspect_checkpoint(self.root, "tool", include_paths=True)
        self.assertEqual(scoped["public_paths"], ["README.md"])


if __name__ == "__main__":
    unittest.main()
