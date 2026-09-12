#!/usr/bin/env python3
"""Behavioral gates in disposable Git repositories; never disable real hooks."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

from common import HookError, run
from checks import (go_packages, source_checks, governance_commands, context_changed,
                    audit_scope, local_package_patterns, checkpoint_checks)
from hooks import push_updates, new_branch_base, pre_push, push_check_mode
import sandbox

ROOT = Path(__file__).resolve().parents[3]
RUNNER = Path(".config/lefthook/scripts/hooks.py")
GUARD = ROOT / ".config/agent/hooks/block_evasion.py"


def command(repo, *args, data=None, ok=True):
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
    for key in ("GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"):
        env.pop(key, None)
    result = subprocess.run(args, cwd=repo, env=env, input=data, capture_output=True,
                            timeout=120, check=False)
    if ok and result.returncode:
        raise AssertionError(result.stdout.decode() + result.stderr.decode())
    return result


class GitHooks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-hook-test-")
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.repo.mkdir()
        command(self.repo, "git", "init", "-q", "-b", "main")
        command(self.repo, "git", "config", "user.name", "Hook Test")
        command(self.repo, "git", "config", "user.email", "hook@example.test")
        for path in (".config/lefthook", ".config/agent/hooks"):
            shutil.copytree(ROOT / path, self.repo / path)
        shutil.copy(ROOT / "lefthook.yml", self.repo / "lefthook.yml")
        # Fixture policy self-tests are data, not another recursive full suite.
        (self.repo / ".config/lefthook/scripts/test_hooks.py").write_text(
            'print("fixture hook self-tests passed")\n')
        (self.repo / "README.md").write_text("# Fixture\n")
        command(self.repo, "git", "add", ".")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "chore: initialize fixture")
        command(self.repo, "lefthook", "install")

    def write(self, name, data, stage=True):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(data)
        if stage:
            command(self.repo, "git", "add", "--", name)

    def hook(self, name="pre-commit", *args, data=None):
        return command(self.repo, "lefthook", "run", name, *args, data=data, ok=False)

    def test_docs_commit_preserves_unstaged_and_untracked_files(self):
        self.write("README.md", "# Intended\n")
        self.write("README.md", "# Unstaged\n", stage=False)
        self.write("private scratch.txt", "keep me\n", stage=False)
        before = command(self.repo, "git", "diff", "--cached", "--binary").stdout
        checked = self.hook()
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertEqual(before, command(self.repo, "git", "diff", "--cached", "--binary").stdout)
        result = command(self.repo, "git", "commit", "-s", "-m", "docs: add staged text")
        self.assertIn(b"message-policy", result.stdout + result.stderr)
        self.assertIn(b"Dedupe review due", result.stdout + result.stderr)
        self.assertEqual(command(self.repo, "git", "show", "HEAD:README.md").stdout, b"# Intended\n")
        self.assertEqual((self.repo / "README.md").read_text(), "# Unstaged\n")
        self.assertTrue((self.repo / "private scratch.txt").exists())
        self.assertFalse((self.repo / ".workingdir").exists())

    def test_staged_python_failure_cannot_be_hidden_by_worktree_fix(self):
        self.write("bad name 'quoted'.py", "def invalid(:\n")
        self.write("bad name 'quoted'.py", "value = 1\n", stage=False)
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"invalid syntax", result.stdout + result.stderr)

    def test_codex_adapter_routes_guard_and_normalizes_failures(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        allowed = {"hook_event_name": "PreToolUse", "tool_name": "Bash",
                   "tool_input": {"command": "printf 'policy-test'"}}
        denied = {"tool_input": {"command": "git commit --no-verify"}}
        cases = [(json.dumps(allowed).encode(), 0), (json.dumps(denied).encode(), 2),
                 (b"", 2), (b"not JSON", 2), (b"{}", 2),
                 (b'{"tool_input":{"command":null}}', 2), (b" " * ((1 << 20) + 1), 2)]
        for payload, expected in cases:
            with self.subTest(size=len(payload), prefix=payload[:48]):
                result = command(self.repo, "python3", str(adapter), data=payload, ok=False)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertNotIn(b"policy-test\n", result.stdout)

    def test_codex_adapter_missing_guard_blocks(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        (self.repo / ".config/agent/hooks/block_evasion.py").unlink()
        result = command(self.repo, "python3", str(adapter),
                         data=b'{"tool_input":{"command":"git status"}}', ok=False)
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)

    def test_codex_hook_configuration_runs_from_nested_directory(self):
        self.write(".codex/hooks.json", (ROOT / ".codex/hooks.json").read_text())
        settings = json.loads((self.repo / ".codex/hooks.json").read_text())
        registration = settings["hooks"]["PreToolUse"][0]
        self.assertRegex("Bash", registration["matcher"])
        self.assertNotRegex("Write", registration["matcher"])
        action = registration["hooks"][0]
        self.assertEqual(action["type"], "command")
        nested = self.repo / "nested directory"
        nested.mkdir()
        for command_text, expected in (("git status", 0), ("git commit --no-verify", 2)):
            payload = json.dumps({"hook_event_name": "PreToolUse", "tool_name": "Bash",
                                  "tool_input": {"command": command_text}}).encode()
            result = command(nested, "/bin/sh", "-c", action["command"],
                             data=payload, ok=False)
            self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

    def test_good_index_ignores_unstaged_python_syntax_error(self):
        self.write("strange ; $ name.py", "value = 1\n")
        self.write("strange ; $ name.py", "def invalid(:\n", stage=False)
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_invalid_encoded_source_bytes_are_rejected(self):
        for name in ("bad.py", "bad.json"):
            (self.repo / name).write_bytes(b'"\xff"\n')
            command(self.repo, "git", "add", "--", name)
            self.assertNotEqual(self.hook().returncode, 0)
            command(self.repo, "git", "rm", "--cached", "--", name)

    def test_delete_only_and_empty_index(self):
        self.assertEqual(self.hook().returncode, 0)
        command(self.repo, "git", "rm", "README.md")
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_staged_policy_validates_and_runs_its_behavioral_gate(self):
        self.write("lefthook.yml", (self.repo / "lefthook.yml").read_text() + "# policy edit\n")
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"fixture hook self-tests passed", result.stdout + result.stderr)
        self.write("lefthook.yml", "pre-commit: [malformed\n")
        self.assertNotEqual(self.hook().returncode, 0)

    def test_gofmt_failure_does_not_modify_or_stage_file(self):
        self.write("go.mod", "module example.test/hooks\n\ngo 1.27\n")
        self.write("main.go", "package main\nfunc main(){println(1)}\n")
        before = (self.repo / "main.go").read_bytes()
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"gofmt", result.stdout + result.stderr)
        self.assertEqual(before, (self.repo / "main.go").read_bytes())

    def test_vet_checks_staged_go_package(self):
        self.write("go.mod", "module example.test/hooks\n\ngo 1.27\n")
        self.write("main.go", 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Printf("%d", "bad") }\n')
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"wrong type", result.stdout + result.stderr)

    def test_message_good_negative_and_missing_dco(self):
        message = self.repo / "message.txt"
        message.write_text("fix(hooks): gate changes\n\nSigned-off-by: Hook Test <hook@example.test>\n")
        self.assertEqual(self.hook("commit-msg", str(message)).returncode, 0)
        message.write_text("fix: missing signoff\n")
        self.assertNotEqual(self.hook("commit-msg", str(message)).returncode, 0)
        message.write_text("bad subject\n\nSigned-off-by: Hook Test <hook@example.test>\n")
        self.assertNotEqual(self.hook("commit-msg", str(message)).returncode, 0)

    def test_prepare_does_not_invent_attestation(self):
        message = self.repo / "message.txt"
        message.write_text("")
        result = self.hook("prepare-commit-msg", str(message), "")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("type(scope)", message.read_text())
        self.assertNotIn("Signed-off-by:", message.read_text())

    def test_real_push_initial_docs_update_new_branch_and_deletion(self):
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        first = command(self.repo, "git", "push", "-u", "origin", "main")
        self.assertIn(b"pushed-checks", first.stdout + first.stderr)
        self.assertIn(b"Push:", first.stdout + first.stderr)
        self.write("README.md", "# Pushed docs\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: update fixture")
        self.assertEqual(command(self.repo, "git", "push").returncode, 0)
        command(self.repo, "git", "checkout", "-q", "-b", "topic")
        self.assertEqual(command(self.repo, "git", "push", "origin", "topic").returncode, 0)
        self.assertEqual(command(self.repo, "git", "push", "origin", "--delete", "topic").returncode, 0)

    def test_push_checks_requested_ref_and_ignores_dirty_worktree(self):
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("bad.json", "{", stage=False)
        protocol = f"refs/heads/main {head} refs/heads/main {'0' * 40}\n".encode()
        result = self.hook("pre-push", "origin", data=protocol)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.repo / "bad.json").exists())
        missing = protocol.replace(b"0" * 40, b"f" * 40)
        result = self.hook("pre-push", "origin", data=missing)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"checking the full tree", result.stdout + result.stderr)
        self.assertNotEqual(self.hook("pre-push", "origin", data=b"broken\n").returncode, 0)

    def test_push_passes_actual_base_to_snapshot_audit(self):
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("README.md", "# Range audit\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: test pushed range")
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        protocol = f"refs/heads/main {head} refs/heads/main {base}\n"
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with mock.patch("hooks.sys.stdin", io.StringIO(protocol)), \
                    mock.patch("hooks.source_checks", return_value=False) as check:
                pre_push("origin")
                self.assertEqual(check.call_args.kwargs, {"base": base})
                self.assertEqual(check.call_args.args[1], ["README.md"])
            with mock.patch("hooks.sys.stdin", io.StringIO(protocol.replace(base, "f" * 40))), \
                    mock.patch("hooks.source_checks", return_value=False) as check:
                pre_push("origin")
                self.assertIsNone(check.call_args.kwargs["base"])
                self.assertIn("lefthook.yml", check.call_args.args[1])
        finally:
            os.chdir(original)

    def test_real_checkpoint_push_builds_and_tests_without_release_receipt(self):
        self.write("go.mod", "module example.test/checkpoint\n\ngo 1.27\n")
        self.write("value.go", "package checkpoint\n\nfunc Value() int { return 1 }\n")
        self.write("value_test.go", 'package checkpoint\n\nimport "testing"\n\n'
                   'func TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatal("wrong value")\n\t}\n}\n')
        # Valid syntax, but no signing authority or release gate implementation.
        self.write(".standards.yaml", 'receipt:\n  public_key: ""\n')
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat: add checkpoint fixture")
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        result = command(self.repo, "git", "push", "origin", "HEAD:refs/heads/checkpoint/wip")
        output = result.stdout + result.stderr
        self.assertIn(b"Checkpoint Go scope: example.test/checkpoint", output)
        self.assertIn(b"ok  \texample.test/checkpoint", output)
        self.assertIn(b"WIP checkpoint:", output)
        self.assertIn(b"no release receipt issued", output)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/checkpoint/wip").stdout,
                         command(self.repo, "git", "rev-parse", "HEAD").stdout)
        self.assertFalse((self.repo / ".git/praetor-receipts").exists())
        self.assertFalse((self.repo / ".standards-receipt.json").exists())
        # Existing remote checkpoints cannot become trusted strict baselines.
        # Promotion of this same commit must still reach and fail release checks.
        for destination in ("refs/heads/review/wip", "refs/tags/checkpoint/wip"):
            rejected = command(self.repo, "git", "push", "origin", "HEAD:" + destination, ok=False)
            self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
            self.assertIn(b"cmd/standardsctl", rejected.stdout + rejected.stderr)
            self.assertNotIn(b"WIP checkpoint:", rejected.stdout + rejected.stderr)
            self.assertEqual(command(self.repo, "git", "ls-remote", "origin", destination).stdout, b"")
        mixed_refs = ("refs/heads/checkpoint/mixed", "refs/heads/review/mixed")
        rejected = command(self.repo, "git", "push", "origin",
                           *("HEAD:" + ref for ref in mixed_refs), ok=False)
        self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
        self.assertIn(b"cmd/standardsctl", rejected.stdout + rejected.stderr)
        self.assertEqual(command(self.repo, "git", "ls-remote", "origin", *mixed_refs).stdout, b"")

    def test_push_destination_controls_mode_and_mixed_refs_still_run_strict_checks(self):
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("README.md", "# Checkpoint routing\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: test destination policy")
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        checkpoint = f"refs/heads/main {head} refs/heads/checkpoint/wip {base}\n"
        strict = f"refs/heads/checkpoint/local {head} refs/heads/main {base}\n"
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            # Only the destination determines policy; the local branch is irrelevant.
            with mock.patch("hooks.sys.stdin", io.StringIO(strict)), \
                    mock.patch("hooks.checkpoint_checks") as wip, \
                    mock.patch("hooks.source_checks", return_value=False) as full:
                pre_push("origin")
                full.assert_called_once()
                wip.assert_not_called()
            # An already-checked WIP commit cannot suppress the strict check for
            # that same commit/base, including after a duplicate checkpoint ref.
            for protocol in (checkpoint + strict, checkpoint + checkpoint + strict, strict + checkpoint):
                with self.subTest(protocol=protocol), \
                        mock.patch("hooks.sys.stdin", io.StringIO(protocol)), \
                        mock.patch("hooks.source_checks", side_effect=HookError("strict release rejected")) as full:
                    with self.assertRaisesRegex(HookError, "strict release rejected"):
                        pre_push("origin")
                    full.assert_called_once()
            # File checks are mandatory before either mode's source checks.
            with mock.patch("hooks.sys.stdin", io.StringIO(checkpoint)), \
                    mock.patch("hooks.file_checks", side_effect=HookError("invalid snapshot syntax")), \
                    mock.patch("hooks.checkpoint_checks") as wip:
                with self.assertRaisesRegex(HookError, "invalid snapshot syntax"):
                    pre_push("origin")
                wip.assert_not_called()
        finally:
            os.chdir(original)

    def test_real_merge_and_rewrite_stages(self):
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "checkout", "-q", "-b", "topic")
        self.write("topic.md", "topic\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: add topic")
        tip = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "checkout", "-q", "main")
        result = command(self.repo, "git", "merge", "--no-ff", "--signoff", "topic", "-m", "chore: merge topic")
        self.assertIn(b"post-merge", result.stdout + result.stderr)
        rewritten = self.hook("post-rewrite", data=f"{base} {tip}\n".encode())
        self.assertEqual(rewritten.returncode, 0, rewritten.stdout + rewritten.stderr)
        self.assertNotEqual(self.hook("post-rewrite", data=b"bad\n").returncode, 0)


class ScopeAndGuard(unittest.TestCase):
    def init_governance_repo(self, root):
        command(root, "git", "init", "-q")
        command(root, "git", "config", "user.name", "Hook Test")
        command(root, "git", "config", "user.email", "hook@example.test")
        command(root, "git", "add", ".")
        command(root, "git", "commit", "-q", "-m", "chore: initialize audit fixture")

    def test_new_branch_ignores_unrelated_tracking_ref(self):
        with mock.patch("hooks.git", return_value=b"refs/remotes/origin/a unrelated\nrefs/remotes/origin/b related\n"), \
                mock.patch("hooks.run", side_effect=[b"", b"ancestor\n"]) as process:
            self.assertEqual(new_branch_base("head", "origin"), "ancestor")
            self.assertEqual(process.call_count, 2)

    def test_new_strict_branch_ignores_checkpoint_and_symbolic_head_baselines(self):
        refs = (b"refs/remotes/origin/HEAD checkpoint\n"
                b"refs/remotes/origin/checkpoint/wip checkpoint\n"
                b"refs/remotes/origin/main strict\n")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin"), "base")
            self.assertEqual(process.call_args.args[0][-1], "strict")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin", include_checkpoints=True), "base")
            self.assertEqual(process.call_args.args[0][-1], "checkpoint")

    def test_push_protocol_boundaries(self):
        self.assertEqual(push_updates(""), [])
        zero = "0" * 40
        self.assertEqual(push_updates(f"(delete) {zero} refs/heads/a {'a' * 40}"), [])
        self.assertEqual(len(push_updates(f"refs/heads/a {'b' * 40} refs/heads/a {zero}")), 1)
        with self.assertRaises(HookError):
            push_updates("bad")

    def test_checkpoint_destination_boundaries(self):
        for destination in ("refs/heads/checkpoint/wip", "refs/heads/checkpoint/a/b"):
            self.assertEqual(push_check_mode(destination), "checkpoint")
        for destination in ("refs/heads/main", "refs/heads/lts/1", "refs/heads/audit/wip",
                            "refs/tags/checkpoint/wip", "refs/checkpoint/wip", "checkpoint/wip",
                            "refs/heads/checkpoint", "refs/heads/checkpoint/", ""):
            with self.subTest(destination=destination):
                self.assertEqual(push_check_mode(destination), "strict")

    def test_guard_command_and_json_without_changing_live_environment(self):
        for cmd in ("git status", "git commit -s -m 'fix: valid'"):
            payload = {"hook_event_name": "PreToolUse", "tool_name": "Bash",
                       "tool_input": {"command": cmd, "workdir": str(ROOT)}}
            result = self.guard_input(json.dumps(payload).encode())
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"PRAETOR_COMMAND_POLICY_OK\n")
        # Denied commands are JSON data for the guard; they are never executed.
        for cmd in ("git commit --no-verify", "git commit -n", "LEFTHOOK=0 git commit"):
            result = self.guard_input(json.dumps({"tool_input": {"command": cmd}}).encode())
            self.assertEqual(result.returncode, 1)
            self.assertNotIn(b"PRAETOR_COMMAND_POLICY_OK", result.stdout)

    def guard_input(self, data):
        return subprocess.run(["python3", str(GUARD)], input=data, capture_output=True,
                              timeout=10, check=False)

    def test_guard_rejects_missing_or_wrong_json_command(self):
        payloads = [None, [], "git status", {}, {"tool_input": None},
                    {"tool_input": []}, {"tool_input": "git status"}, {"tool_input": {}}]
        payloads.extend({"tool_input": {"command": value}}
                        for value in (None, False, 1, [], {}, "", " \t\r\n"))
        for payload in payloads:
            with self.subTest(payload=payload):
                result = self.guard_input(json.dumps(payload).encode())
                self.assertEqual(result.returncode, 1)
                self.assertIn(b"Invalid hook input", result.stderr)

    def test_guard_rejects_malformed_and_oversized_json(self):
        maximum = 1 << 20
        valid = json.dumps({"tool_input": {"command": "git status"}}).encode()
        boundary = valid + b" " * (maximum - len(valid))
        result = self.guard_input(boundary)
        self.assertEqual(result.returncode, 0, result.stderr)
        for data in (b"", b"not json", b'{"tool_input":', b"\xff",
                     b"[" * 2000 + b"]" * 2000, boundary + b" "):
            with self.subTest(size=len(data), prefix=data[:20]):
                result = self.guard_input(data)
                self.assertEqual(result.returncode, 1)
                self.assertIn(b"Invalid hook input", result.stderr)
                self.assertNotIn(b"Traceback", result.stderr)

    def test_guard_preserves_argv_and_environment_entry_points(self):
        for args in (("git", "status"), ("--environment",)):
            result = subprocess.run(["python3", str(GUARD), *args], input=b"",
                                    capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"")

    def load_codex_adapter(self):
        path = GUARD.with_name("codex_pre_tool.py")
        spec = importlib.util.spec_from_file_location("tested_codex_pre_tool", path)
        module = importlib.util.module_from_spec(spec)
        with mock.patch.object(sys, "path", [str(GUARD.parent), *sys.path]):
            spec.loader.exec_module(module)
        return module

    def test_codex_adapter_fails_closed_when_guard_is_unavailable(self):
        adapter = self.load_codex_adapter()
        payload = b'{"tool_input":{"command":"git status"}}'
        for failure in (FileNotFoundError("missing fixture executable"),
                        subprocess.TimeoutExpired(["python3", "block_evasion.py"], 10)):
            with self.subTest(failure=type(failure).__name__), \
                    mock.patch.object(adapter.subprocess, "run", side_effect=failure), \
                    contextlib.redirect_stderr(io.StringIO()) as diagnostic:
                self.assertEqual(adapter.check(payload), 2)
                self.assertIn("unavailable", diagnostic.getvalue())

    def test_codex_adapter_rejects_success_without_guard_marker(self):
        adapter = self.load_codex_adapter()
        payload = b'{"tool_input":{"command":"git status"}}'
        def skipped_guard(*_args, **kwargs):
            kwargs["stdout"].write(b"guard process completed without checking policy\n")
            return subprocess.CompletedProcess(["python3", "block_evasion.py"], 0)
        with mock.patch.object(adapter.subprocess, "run", side_effect=skipped_guard), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(adapter.check(payload), 2)

    def test_reverse_dependencies_embed_testdata_module_and_docs_scope(self):
        with tempfile.TemporaryDirectory(prefix="praetor-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scope\n\ngo 1.27\n")
            (root / "a").mkdir()
            (root / "b").mkdir()
            (root / "c").mkdir()
            (root / "a/a.go").write_text('package a\nimport _ "embed"\n//go:embed data.txt guide.md\nvar Data string\n')
            (root / "a/data.txt").write_text("embedded")
            (root / "a/guide.md").write_text("embedded markdown")
            (root / "a/assets").mkdir()
            (root / "a/assets/remaining.json").write_text("{}")
            with (root / "a/a.go").open("a") as stream:
                stream.write('//go:embed assets/*\nvar Assets string\n')
            (root / "b/b.go").write_text('package b\nimport "example.test/scope/a"\nvar Value = a.Data\n')
            (root / "c/c.go").write_text("package c\n")
            expected = ["example.test/scope/a", "example.test/scope/b"]
            for names in (["a/a.go"], ["a/data.txt"], ["a/testdata/input.json"], ["a/guide.md"]):
                self.assertEqual(go_packages(root, names, reverse=True), expected)
            self.assertEqual(go_packages(root, ["README.md"], reverse=True), [])
            self.assertEqual(len(go_packages(root, ["go.mod"], reverse=True)), 3)
            self.assertEqual(go_packages(root, ["removed/old.go"], reverse=True), [])
            self.assertEqual(go_packages(root, ["a/assets/deleted.json"], reverse=True), expected)

    def test_governance_selection_without_go_changes(self):
        with tempfile.TemporaryDirectory(prefix="praetor-governance-") as temp:
            root = Path(temp)
            (root / ".standards.yaml").write_text("repository: {}\n")
            (root / ".workingdir").mkdir()
            self.init_governance_repo(root)
            for name in (".standards.yaml", ".standards.lock", ".standards-baseline.json", ".agents/persona.md"):
                commands = governance_commands(root, [name], False)
                self.assertEqual(commands[0][3:], ["audit", "--touched=.standards.yaml"])
                self.assertEqual(commands[1][3:], ["flavor", "audit", "."])
            self.assertEqual(governance_commands(root, ["README.md"], False), [])
            self.assertEqual(len(governance_commands(root, ["a.go"], True)), 2)
            self.assertEqual(governance_commands(root, [".workingdir/OPEN.md"], False)[0][3:], ["state", "audit", "."])
            for name in (".gemini/GEMINI.md", ".codex/rules.md", ".cursor/rules/hiss-invariants.mdc"):
                self.assertTrue(context_changed([name]))
            with mock.patch("checks.parallel", side_effect=HookError("governance rejected")) as gate:
                with self.assertRaisesRegex(HookError, "governance rejected"):
                    source_checks(root, [".standards.yaml"])
                self.assertEqual(len(gate.call_args.args[0]), 2)

    def test_governance_only_push_retains_gate_and_pinned_receipt_verification(self):
        with tempfile.TemporaryDirectory(prefix="praetor-receipt-") as temp:
            root = Path(temp)
            (root / ".standards.yaml").write_text("repository: {}\n")
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()
            calls = []
            def process(argv, **kwargs):
                if argv[0] == "git":
                    return run(argv, **kwargs)
                calls.append(argv)
                return b""
            with mock.patch("checks.parallel"), mock.patch("checks.run", side_effect=process):
                self.assertTrue(source_checks(root, [".standards.yaml"], base=base))
                self.assertEqual([cmd[4] for cmd in calls], ["run", "verify"])
            def reject_pin(argv, **kwargs):
                if argv[:5] == ["go", "run", "./cmd/standardsctl", "gate", "verify"]:
                    raise HookError("pin mismatch")
                return process(argv, **kwargs)
            with mock.patch("checks.parallel"), mock.patch("checks.run", side_effect=reject_pin):
                with self.assertRaisesRegex(HookError, "pin mismatch"):
                    source_checks(root, [".standards.yaml"], base=base)

    def test_audit_range_uses_exact_oid_and_touched_paths(self):
        with tempfile.TemporaryDirectory(prefix="praetor-audit-range-") as temp:
            root = Path(temp)
            (root / "legacy.go").write_text("package legacy\n")
            (root / ".standards-baseline.json").write_text('{"total_infractions": 0}\n')
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()
            supplied = ["legacy.go", ".standards-baseline.json"]
            self.assertEqual(audit_scope(root, supplied, base),
                             ["--base=" + base, "--touched=" + ",".join(supplied)])
            # Missing explicit refs cannot silently drop historical comparison.
            for invalid in ("f" * 40, "--help", "missing-branch"):
                with self.assertRaises(HookError):
                    audit_scope(root, supplied, invalid)
            # Absent remote history makes every tracked file touched, even when
            # callers request a narrower path list.
            self.assertEqual(audit_scope(root, ["README.md"], None),
                             ["--touched=.standards-baseline.json,legacy.go"])
            for unrepresentable in (["comma,name.go"], ["line\nname.go"], [" leading.go"], ["a.go"] * 10001):
                with self.assertRaisesRegex(HookError, "lossless CSV"):
                    audit_scope(root, unrepresentable, base)

    def test_real_scoped_race_test_propagates_failure(self):
        with tempfile.TemporaryDirectory(prefix="praetor-race-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/race\n\ngo 1.27\n")
            (root / "a.go").write_text("package a\n")
            test = root / "a_test.go"
            test.write_text('package a\nimport "testing"\nfunc TestA(t *testing.T) {}\n')
            source_checks(root, ["a_test.go"], "test")
            test.write_text('package a\nimport "testing"\nfunc TestA(t *testing.T) { t.Fatal("negative control") }\n')
            with self.assertRaisesRegex(HookError, "negative control"):
                source_checks(root, ["a_test.go"], "test")

    def test_real_scoped_lint_scans_reverse_dependencies(self):
        with tempfile.TemporaryDirectory(prefix="praetor-lint-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / ".golangci.yml").write_text(
                'version: "2"\nlinters:\n  default: none\n  enable: [errcheck]\n')
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/scopes/core"\n'
                                'func Value() int { return core.Value() }\n')
            # Use the actual from-source invocation: an installed binary may have been
            # built with an older Go version than the checked module requires.
            source_checks(root, ["core/core.go"], "lint")
            consumer.write_text('package consumer\nimport ("example.test/scopes/core"; "os")\n'
                                'func Value() int { os.Chdir("."); return core.Value() }\n')
            with self.assertRaisesRegex(HookError, "errcheck"):
                source_checks(root, ["core/core.go"], "lint")

    def test_checkpoint_test_only_packages_still_run_and_propagate_failure(self):
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-test-only-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/onlytests\n\ngo 1.27\n")
            test = root / "only_test.go"
            test.write_text('package onlytests\nimport "testing"\n'
                            'func TestOnly(t *testing.T) {}\n')
            checkpoint_checks(root, ["only_test.go"])
            test.write_text('package onlytests\nimport "testing"\n'
                            'func TestOnly(t *testing.T) { t.Fatal("test-only failure") }\n')
            with self.assertRaisesRegex(HookError, "test-only failure"):
                checkpoint_checks(root, ["only_test.go"])

    def test_checkpoint_real_build_rejects_broken_reverse_dependency(self):
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-build-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/checkpoint\n\ngo 1.27\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/checkpoint/core"\n'
                                'func Value() int { return core.Value() }\n')
            checkpoint_checks(root, ["core/core.go"])
            consumer.write_text('package consumer\nimport "example.test/checkpoint/core"\n'
                                'func Value() int { return core.Missing() }\n')
            with self.assertRaisesRegex(HookError, "go build.*|undefined: core.Missing") as failure:
                checkpoint_checks(root, ["core/core.go"])
            self.assertIn("undefined: core.Missing", str(failure.exception))

    def test_checkpoint_real_race_detector_rejects_consumer_test_race(self):
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-race-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/checkpoint\n\ngo 1.27\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            (root / "consumer.go").write_text("package consumer\n")
            test = root / "consumer_test.go"
            test.write_text('package consumer\nimport ("testing"; "example.test/checkpoint/core")\n'
                            'func TestConsumer(t *testing.T) { if core.Value() != 1 { t.Fatal("value") } }\n')
            checkpoint_checks(root, ["core/core.go"])
            test.write_text('package consumer\nimport ("testing"; "example.test/checkpoint/core")\n'
                            'func TestConsumer(t *testing.T) {\n'
                            'var value int; done := make(chan bool)\n'
                            'go func() { value = core.Value(); done <- true }()\n'
                            'value = 2; <-done; _ = value\n}\n')
            with self.assertRaisesRegex(HookError, "DATA RACE"):
                checkpoint_checks(root, ["core/core.go"])

    def test_real_scoped_gosec_cannot_succeed_without_scanning(self):
        with tempfile.TemporaryDirectory(prefix="praetor-gosec-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / ".gosec.json").write_text("{}\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/scopes/core"\n'
                                'func Value() int { return core.Value() }\n')
            source_checks(root, ["core/core.go"], "sec")
            consumer.write_text('package consumer\nimport ("example.test/scopes/core"; "crypto/md5")\n'
                                'func Sum() [md5.Size]byte { return md5.Sum([]byte{byte(core.Value())}) }\n')
            with self.assertRaisesRegex(HookError, "G401|G501"):
                source_checks(root, ["core/core.go"], "sec")

    def test_local_package_patterns_confine_actual_go_metadata(self):
        with tempfile.TemporaryDirectory(prefix="praetor-package-dirs-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / "a.go").write_text("package scopes\n")
            (root / "nested").mkdir()
            (root / "nested/a.go").write_text("package nested\n")
            selected = ["example.test/scopes", "example.test/scopes/nested"]
            self.assertEqual(local_package_patterns(root, selected), [".", "./nested"])
            with self.assertRaisesRegex(HookError, "outside the checked snapshot"):
                local_package_patterns(root, ["fmt"])
            with self.assertRaises(HookError):
                local_package_patterns(root, ["example.test/scopes/missing"])

    def test_process_failure_and_timeout_are_not_swallowed(self):
        with self.assertRaises(HookError):
            run(["python3", "-c", "raise SystemExit(7)"])
        with self.assertRaises(HookError):
            run(["python3", "-c", "import time; time.sleep(10)"], timeout=0.01)

    def test_timeout_kills_child_process_group(self):
        with tempfile.TemporaryDirectory(prefix="praetor-child-") as temp:
            marker = Path(temp) / "child-survived"
            child = f"import time; from pathlib import Path; time.sleep(0.2); Path({str(marker)!r}).touch()"
            parent = f"import subprocess,time; subprocess.Popen(['python3','-c',{child!r}]); time.sleep(10)"
            with self.assertRaises(HookError):
                run(["python3", "-c", parent], timeout=0.05)
            time.sleep(0.3)
            self.assertFalse(marker.exists())

    def test_sandbox_failure_removes_only_owned_container(self):
        calls = []
        def docker(argv, **kwargs):
            calls.append(argv)
            if argv[:2] == ["docker", "run"]:
                raise HookError("simulated container timeout")
            return b"owned-container\n" if argv[:2] == ["docker", "ps"] else b""
        with tempfile.TemporaryDirectory(prefix="praetor-docker-") as temp, \
                mock.patch("sandbox.git", return_value=b"a" * 40 + b"\n"), \
                mock.patch("sandbox.snapshot", return_value=contextlib.nullcontext(Path(temp))), \
                mock.patch("sandbox.run", side_effect=docker):
            with self.assertRaisesRegex(HookError, "simulated container timeout"):
                sandbox.main([])
        start = next(cmd for cmd in calls if cmd[:2] == ["docker", "run"])
        name = start[start.index("--name") + 1]
        self.assertTrue(name.startswith("praetor-gate-"))
        self.assertEqual(calls[-1], ["docker", "container", "rm", "--force", name])


if __name__ == "__main__":
    unittest.main(verbosity=2)
