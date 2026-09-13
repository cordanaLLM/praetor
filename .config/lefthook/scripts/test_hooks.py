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

from common import HookError, run, snapshot
from checks import (go_packages, source_checks, governance_commands, context_changed,
                    audit_scope, local_package_patterns, checkpoint_checks)
from hooks import push_updates, new_branch_base, pre_push, push_check_mode
from privacy import check_private_history, check_private_index
import sandbox

ROOT = Path(__file__).resolve().parents[3]
RUNNER = Path(".config/lefthook/scripts/hooks.py")
GUARD = ROOT / ".config/agent/hooks/block_evasion.py"


def command(repo, *args, data=None, ok=True, maintain_state=True):
    # Most fixtures model an agent obeying the sync obligation. Negative state
    # tests opt out and execute the same real hooks against stale/missing state.
    if (maintain_state and args[:2] in (("git", "commit"), ("git", "push"))
            and (repo / "bin/praetorctl").is_file()):
        command(repo, "bin/praetorctl", "state", "sync", ".", ok=ok)
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
    for key in ("GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"):
        env.pop(key, None)
    result = subprocess.run(args, cwd=repo, env=env, input=data, capture_output=True,
                            timeout=120, check=False)
    if ok and result.returncode:
        raise AssertionError(result.stdout.decode() + result.stderr.decode())
    return result


class GitHooks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cli_temp = tempfile.TemporaryDirectory(prefix="praetor-hook-cli-")
        cls.addClassCleanup(cls.cli_temp.cleanup)
        cls.binary = Path(cls.cli_temp.name) / "praetorctl"
        command(ROOT, "go", "build", "-o", str(cls.binary), "./cmd/standardsctl")

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-hook-test-")
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.initialize_repo()

    def initialize_repo(self, initial_files=()):
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
        for name, content in initial_files:
            self.write(name, content, stage=False)
        command(self.repo, "git", "add", ".")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "chore: initialize fixture")
        self.initialize_state()
        command(self.repo, "lefthook", "install")

    def initialize_state(self):
        (self.repo / "bin").mkdir()
        os.link(self.binary, self.repo / "bin/praetorctl")
        (self.repo / ".git/info/exclude").write_text("/bin/\n/.workingdir/\n/Makefile\n")
        (self.repo / "Makefile").write_text("hook-cli:\n\t@test -x bin/praetorctl\n")
        command(self.repo, "bin/praetorctl", "state", "init", ".")
        command(self.repo, "bin/praetorctl", "state", "sync", ".")

    def write(self, name, data, stage=True):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(data)
        if stage:
            command(self.repo, "git", "add", "--", name)

    def hook(self, name="pre-commit", *args, data=None, maintain_state=True):
        if maintain_state and name in {"pre-commit", "pre-push", "commit-msg"}:
            command(self.repo, "bin/praetorctl", "state", "sync", ".")
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
        self.assertTrue((self.repo / ".workingdir/STATE.md").is_file())
        command(self.repo, "bin/praetorctl", "state", "sync", "--verify", ".")

    def test_staged_python_failure_cannot_be_hidden_by_worktree_fix(self):
        self.write("bad name 'quoted'.py", "def invalid(:\n")
        self.write("bad name 'quoted'.py", "value = 1\n", stage=False)
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"invalid syntax", result.stdout + result.stderr)

    def test_forced_private_state_staging_blocks_commit_without_reading_content(self):
        self.write(".gitignore", "/.workingdir/\n")
        self.write(".workingdir/docs/cluster.json", "not JSON: PRIVATE_FIXTURE\n", stage=False)
        command(self.repo, "git", "add", "-f", "--", ".workingdir/docs/cluster.json")
        before = command(self.repo, "git", "rev-parse", "HEAD").stdout
        result = command(self.repo, "git", "commit", "-s", "-m", "docs: accidental private guide", ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertNotIn(b"PRIVATE_FIXTURE", result.stdout + result.stderr)
        self.assertEqual(before, command(self.repo, "git", "rev-parse", "HEAD").stdout)
        self.assertEqual((self.repo / ".workingdir/docs/cluster.json").read_text(),
                         "not JSON: PRIVATE_FIXTURE\n")

    def test_untracking_legacy_private_state_keeps_local_file_and_allows_commit(self):
        self.repo = Path(self.temp.name) / "legacy"
        self.initialize_repo(((".workingdir/docs/cluster.md", "private fixture\n"),))
        self.write(".gitignore", "/.workingdir/\n")
        command(self.repo, "git", "rm", "--cached", "--", ".workingdir/docs/cluster.md")
        result = command(self.repo, "git", "commit", "-s", "-m", "chore: keep state private")
        self.assertIn(b"staged-checks", result.stdout + result.stderr)
        self.assertEqual(command(self.repo, "git", "ls-files", ".workingdir").stdout, b"")
        self.assertEqual((self.repo / ".workingdir/docs/cluster.md").read_text(), "private fixture\n")

    def test_private_gitlinks_block_commit_before_snapshot_export(self):
        for index, name in enumerate((".workingdir", ".workingdir/submodule")):
            with self.subTest(name=name):
                self.repo = Path(self.temp.name) / f"gitlink-{index}"
                self.initialize_repo()
                if name == ".workingdir":
                    shutil.rmtree(self.repo / ".workingdir")
                head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
                command(self.repo, "git", "clone", "-q", str(self.repo), str(self.repo / name))
                command(self.repo, "git", "update-index", "--add", "--cacheinfo", f"160000,{head},{name}")
                result = command(self.repo, "git", "commit", "-s", "-m", "chore: private gitlink", ok=False)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
                self.assertEqual(command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip(), head)

    def incoming_private_history(self, legacy=False, gitlink=False, old_topic=False):
        # Simulate commits received from a plain repository that never installed
        # Praetor hooks. The receiving fixture keeps all real hooks enabled.
        external = Path(self.temp.name) / "external"
        remote = Path(self.temp.name) / "remote.git"
        external.mkdir()
        command(external, "git", "init", "-q", "-b", "main")
        command(external, "git", "config", "user.name", "External Fixture")
        command(external, "git", "config", "user.email", "external@example.test")
        if old_topic:
            (external / "README.md").write_text("# Earlier public baseline\n")
            command(external, "git", "add", ".")
            command(external, "git", "commit", "-q", "-s", "-m", "docs: earlier baseline")
            command(external, "git", "branch", "feat/old-topic")
        (external / "README.md").write_text("# External fixture\n")
        if legacy:
            (external / ".workingdir").mkdir()
            (external / ".workingdir/private.txt").write_text("PRIVATE_HISTORY_SENTINEL\n")
        command(external, "git", "add", ".")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: published baseline")
        base = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(external, "git", "init", "--bare", "-q", str(remote))
        command(external, "git", "remote", "add", "origin", str(remote))
        command(external, "git", "push", "-q", "origin", "main")
        if old_topic:
            command(external, "git", "push", "-q", "origin", "feat/old-topic")
        if not legacy:
            if gitlink:
                command(external, "git", "update-index", "--add", "--cacheinfo", f"160000,{base},.workingdir")
            else:
                (external / ".workingdir").mkdir()
                (external / ".workingdir/private.txt").write_text("PRIVATE_HISTORY_SENTINEL\n")
                command(external, "git", "add", ".workingdir")
            command(external, "git", "commit", "-q", "-s", "-m", "docs: incoming private content")
        command(external, "git", "rm", "-r", "--cached", "--", ".workingdir")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: remove private tracking")
        head = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        command(self.repo, "git", "fetch", "-q", "origin")
        if old_topic:
            command(self.repo, "git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
        command(self.repo, "git", "fetch", "-q", str(external), "main")
        return remote, base, head

    def test_public_snapshot_initializes_private_state_before_flavor_and_gate(self):
        public = Path(self.temp.name) / "snapshot-source"
        public.mkdir()
        command(public, "git", "init", "-q", "-b", "main")
        command(public, "git", "config", "user.name", "Snapshot Fixture")
        command(public, "git", "config", "user.email", "snapshot@example.test")
        files = {".standards.yaml": "repository: {}\n", ".standards.lock": "{}\n",
                 ".golangci.yml": "version: '2'\n", ".github/workflows/ci.yml": "name: fixture\n",
                 "lefthook.yml": "{}\n", ".github/rulesets/main.json": "{}\n",
                 ".gitignore": "/.workingdir/\n",
                 "Makefile": 'state-audit: state-init\n\t"' + str(self.binary) + '" state audit .\n'
                             'state-init:\n\t"' + str(self.binary) + '" state init --if-absent .\n'}
        for name, value in files.items():
            path = public / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(value)
        command(public, "git", "add", ".")
        command(public, "git", "commit", "-q", "-s", "-m", "chore: public snapshot fixture")
        private = public / ".workingdir/STATE.md"
        private.parent.mkdir()
        private.write_text("PRIVATE_SNAPSHOT_SENTINEL\n")
        with contextlib.chdir(public), snapshot("HEAD") as directory:
            self.assertFalse((directory / ".workingdir").exists())
            def check_flavor(commands, root):
                for argv in commands:
                    if argv[3:5] == ["flavor", "audit"]:
                        run([str(self.binary), *argv[3:]], cwd=root)
            def check_gate(root):
                run([str(self.binary), "state", "audit", "."], cwd=root)
                return True
            with mock.patch("checks.parallel", side_effect=check_flavor), \
                    mock.patch("checks.run_full_gate", side_effect=check_gate) as gated:
                self.assertTrue(source_checks(directory, [".standards.yaml"], base="HEAD"))
                gated.assert_called_once_with(directory)
            self.assertTrue((directory / ".workingdir/OPEN.md").is_file())
            self.assertNotIn("PRIVATE_SNAPSHOT_SENTINEL", (directory / ".workingdir/STATE.md").read_text())
            self.assertEqual(command(directory, "git", "status", "--porcelain").stdout, b"")
            # Existing partial state must not be repaired or permitted into later gates.
            (directory / ".workingdir/OPEN.md").unlink()
            before = {p.name: p.read_bytes() for p in (directory / ".workingdir").iterdir() if p.is_file()}
            with mock.patch("checks.parallel") as scans, mock.patch("checks.run_full_gate") as gated:
                with self.assertRaisesRegex(HookError, "state audit failed"):
                    source_checks(directory, [".standards.yaml"], base="HEAD")
                scans.assert_not_called()
                gated.assert_not_called()
            self.assertEqual(before, {p.name: p.read_bytes() for p in (directory / ".workingdir").iterdir() if p.is_file()})
        self.assertEqual(private.read_text(), "PRIVATE_SNAPSHOT_SENTINEL\n")

    def test_new_branch_push_prefers_remote_default_over_older_topic_for_private_removal(self):
        remote, base, head = self.incoming_private_history(legacy=True, old_topic=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/removal")
        self.assertIn(b"pushed-checks", result.stdout + result.stderr)
        self.assertEqual((result.stdout + result.stderr).count(f"Push: {head[:12]} checked".encode()), 1)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/review/removal").stdout.decode().strip(), head)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)
        self.assertEqual(command(remote, "git", "ls-tree", "-r", "--name-only", head, "--", ".workingdir").stdout, b"")

    def test_new_branch_default_baseline_rejects_new_private_add_then_delete(self):
        remote, base, head = self.incoming_private_history(old_topic=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/private", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertNotIn(b"PRIVATE_HISTORY_SENTINEL", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/review/private").stdout, b"")
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)

    def test_missing_vendored_push_script_rejects_actual_push(self):
        remote, _, head = self.incoming_private_history(legacy=True, old_topic=True)
        (self.repo / ".config/lefthook/pre-push/pushed-checks.sh").unlink()
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/removal", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"script does not exist", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/review/removal").stdout, b"")

    def test_real_push_rejects_private_add_then_delete_in_requested_history(self):
        remote, base, head = self.incoming_private_history()
        self.write("README.md", "# Unstaged unrelated content\n", stage=False)
        for destination in ("refs/heads/main", "refs/heads/checkpoint/private"):
            result = command(self.repo, "git", "push", "origin", f"{head}:{destination}", ok=False)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
            self.assertNotIn(b"PRIVATE_HISTORY_SENTINEL", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/checkpoint/private").stdout, b"")
        self.assertEqual((self.repo / "README.md").read_text(), "# Unstaged unrelated content\n")

    def test_real_push_rejects_deleted_private_gitlink_history(self):
        remote, base, head = self.incoming_private_history(gitlink=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/main", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)

    def test_real_push_allows_removal_of_already_remote_private_state(self):
        remote, _, head = self.incoming_private_history(legacy=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/main")
        self.assertIn(b"pushed-checks", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), head)
        self.assertEqual(command(remote, "git", "ls-tree", "-r", "--name-only", head, "--", ".workingdir").stdout, b"")
        self.assertEqual((Path(self.temp.name) / "external/.workingdir/private.txt").read_text(),
                         "PRIVATE_HISTORY_SENTINEL\n")

    def test_merge_retaining_remote_private_baseline_is_not_new_private_content(self):
        external = Path(self.temp.name) / "merge-source"
        external.mkdir()
        command(external, "git", "init", "-q", "-b", "main")
        command(external, "git", "config", "user.name", "External Fixture")
        command(external, "git", "config", "user.email", "external@example.test")
        (external / "README.md").write_text("# Fixture\n")
        command(external, "git", "add", ".")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: initial source")
        command(external, "git", "branch", "side")
        (external / ".workingdir").mkdir()
        (external / ".workingdir/private.txt").write_text("old published fixture\n")
        command(external, "git", "add", ".workingdir")
        command(external, "git", "commit", "-q", "-s", "-m", "docs: old remote state")
        base = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(external, "git", "checkout", "-q", "side")
        (external / "other.md").write_text("public change\n")
        command(external, "git", "add", "other.md")
        command(external, "git", "commit", "-q", "-s", "-m", "docs: side change")
        command(external, "git", "checkout", "-q", "main")
        command(external, "git", "merge", "-q", "--no-ff", "-m", "Merge side fixture", "side")
        original = Path.cwd()
        try:
            os.chdir(external)
            check_private_history("HEAD", base)
        finally:
            os.chdir(original)

    def test_unknown_push_baseline_still_checks_private_history(self):
        _, _, head = self.incoming_private_history()
        protocol = f"refs/heads/incoming {head} refs/heads/main {'f' * 40}\n".encode()
        result = self.hook("pre-push", "origin", data=protocol)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"checking the full tree", result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)

    def test_private_symlink_and_ancestor_rejected_without_reading_target(self):
        target = Path(self.temp.name) / "outside-private"
        target.write_text("PRIVATE_SYMLINK_SENTINEL\n")
        for index, name in enumerate((".workingdir", ".workingdir/docs/link")):
            with self.subTest(name=name):
                self.repo = Path(self.temp.name) / f"symlink-{index}"
                self.initialize_repo()
                if name == ".workingdir":
                    shutil.rmtree(self.repo / ".workingdir")
                link = self.repo / name
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(target)
                command(self.repo, "git", "add", "-f", "--", name)
                result = command(self.repo, "git", "commit", "-s", "-m", "docs: private symlink", ok=False)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
                self.assertNotIn(b"PRIVATE_SYMLINK_SENTINEL", result.stdout + result.stderr)
                self.assertTrue(link.is_symlink())
                self.assertEqual(target.read_text(), "PRIVATE_SYMLINK_SENTINEL\n")

    def test_private_history_commit_bound_and_missing_objects_fail_closed(self):
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        heads = []
        for number in range(3):
            self.write("README.md", f"# Revision {number}\n")
            command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: bounded history")
            heads.append(command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip())
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with mock.patch("privacy.MAX_PRIVATE_COMMITS", 2):
                check_private_history(heads[1], base)
                with self.assertRaisesRegex(HookError, "exceeds 2 commits"):
                    check_private_history(heads[2], base)
            with self.assertRaises(HookError):
                check_private_history("f" * 40, base)
            with mock.patch("privacy.subprocess.run", return_value=subprocess.CompletedProcess([], 2)):
                with self.assertRaisesRegex(HookError, "inspection failed"):
                    check_private_index()
            with mock.patch("privacy.subprocess.run", side_effect=subprocess.TimeoutExpired("git", 60)):
                with self.assertRaisesRegex(HookError, "inspection failed"):
                    check_private_index()
        finally:
            os.chdir(original)

    def test_private_history_rejects_incomplete_shallow_ancestry(self):
        shallow = Path(self.temp.name) / "shallow"
        command(self.repo, "git", "clone", "-q", "--depth=1", self.repo.as_uri(), str(shallow))
        original = Path.cwd()
        try:
            os.chdir(shallow)
            with self.assertRaisesRegex(HookError, "shallow repository"):
                check_private_history("HEAD", None)
        finally:
            os.chdir(original)

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

    def test_codex_adapter_requires_executed_lefthook_job(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        empty_bin = self.repo / "empty-bin"
        empty_bin.mkdir()
        payload = b'{"tool_input":{"command":"git status"}}'
        with mock.patch.dict(os.environ, {"PATH": str(empty_bin)}):
            missing = command(self.repo, sys.executable, str(adapter), data=payload, ok=False)
        self.assertEqual(missing.returncode, 2, missing.stdout + missing.stderr)
        fake = empty_bin / "lefthook"
        fake.write_text("#!/bin/sh\nexit 0\n")
        fake.chmod(0o755)
        with mock.patch.dict(os.environ, {"PATH": str(empty_bin)}):
            skipped = command(self.repo, sys.executable, str(adapter), data=payload, ok=False)
        self.assertEqual(skipped.returncode, 2, skipped.stdout + skipped.stderr)

    def test_lefthook_agent_job_consumes_and_checks_command(self):
        for proposed, allowed in (("git status", True), ("git commit --no-verify", False)):
            result = self.hook("agent-pre-tool", data=json.dumps({
                "tool_input": {"command": proposed},
            }).encode())
            self.assertEqual(result.returncode == 0, allowed, result.stdout + result.stderr)
            if allowed:
                self.assertIn(b"PRAETOR_COMMAND_POLICY_OK", result.stdout)

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

    def test_client_settings_alone_run_hook_behavioral_gate(self):
        for name in (".claude/settings.json", ".gemini/settings.json"):
            with self.subTest(client=name):
                self.write(name, (ROOT / name).read_text())
                result = self.hook()
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"fixture hook self-tests passed", result.stdout + result.stderr)
                command(self.repo, "git", "reset", "-q", "HEAD", "--", name)

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

    def test_commit_requires_explicit_state_sync_after_staging(self):
        self.write("README.md", "# Staged after sync\n")
        before = command(self.repo, "git", "rev-parse", "HEAD").stdout
        rejected = command(self.repo, "git", "commit", "-s", "-m", "docs: stale state",
                           ok=False, maintain_state=False)
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn(b"state synchronization stale", rejected.stdout + rejected.stderr)
        self.assertEqual(command(self.repo, "git", "rev-parse", "HEAD").stdout, before)
        command(self.repo, "bin/praetorctl", "state", "sync", ".")
        command(self.repo, "git", "commit", "-s", "-m", "docs: maintained state", maintain_state=False)
        command(self.repo, "bin/praetorctl", "state", "sync", "--verify", ".")

    def test_precommit_missing_and_incomplete_ledger_are_not_repaired(self):
        self.write("README.md", "# Need live ledger\n")
        for name in ("BACKLOG.md", "STATE.md"):
            with self.subTest(name=name):
                path = self.repo / ".workingdir" / name
                before = path.read_bytes()
                path.unlink()
                rejected = self.hook(maintain_state=False)
                self.assertNotEqual(rejected.returncode, 0)
                self.assertFalse(path.exists())
                path.write_bytes(before)
        shutil.rmtree(self.repo / ".workingdir")
        self.assertNotEqual(self.hook(maintain_state=False).returncode, 0)
        self.assertFalse((self.repo / ".workingdir").exists())

    def test_postcommit_sync_failure_is_reported(self):
        (self.repo / ".workingdir/BACKLOG.md").unlink()
        rejected = self.hook("post-commit", maintain_state=False)
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn(b"state sync failed", rejected.stdout + rejected.stderr)

    def test_initial_commit_has_valid_unborn_state(self):
        command(self.repo, "git", "checkout", "--orphan", "new-history")
        command(self.repo, "git", "rm", "-r", "--cached", ".config", "lefthook.yml")
        command(self.repo, "bin/praetorctl", "state", "sync", ".")
        command(self.repo, "git", "commit", "-s", "-m", "chore: initial history", maintain_state=False)
        command(self.repo, "bin/praetorctl", "state", "sync", "--verify", ".")

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
        # Promotion must reach strict checks and reject the absent state-audit
        # bootstrap target before attempting the remaining release pipeline.
        for destination in ("refs/heads/review/wip", "refs/tags/checkpoint/wip"):
            rejected = command(self.repo, "git", "push", "origin", "HEAD:" + destination, ok=False)
            self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
            self.assertIn(b"No rule to make target 'state-audit'", rejected.stdout + rejected.stderr)
            self.assertNotIn(b"WIP checkpoint:", rejected.stdout + rejected.stderr)
            self.assertEqual(command(self.repo, "git", "ls-remote", "origin", destination).stdout, b"")
        mixed_refs = ("refs/heads/checkpoint/mixed", "refs/heads/review/mixed")
        rejected = command(self.repo, "git", "push", "origin",
                           *("HEAD:" + ref for ref in mixed_refs), ok=False)
        self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
        self.assertIn(b"No rule to make target 'state-audit'", rejected.stdout + rejected.stderr)
        self.assertEqual(command(self.repo, "git", "ls-remote", "origin", *mixed_refs).stdout, b"")
        self.assertFalse((self.repo / ".git/praetor-receipts").exists())
        self.assertFalse((self.repo / ".standards-receipt.json").exists())

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
        command(self.repo, "git", "merge", "--no-ff", "--no-commit", "topic")
        command(self.repo, "git", "commit", "-s", "-m", "chore: merge topic")
        merged = self.hook("post-merge")
        self.assertEqual(merged.returncode, 0, merged.stdout + merged.stderr)
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
        refs = (b"refs/remotes/origin/HEAD checkpoint refs/remotes/origin/checkpoint/wip\n"
                b"refs/remotes/origin/alias checkpoint refs/remotes/origin/checkpoint/wip\n"
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

    def test_new_branch_prefers_named_default_and_preserves_ancestry_failures(self):
        refs = (b"refs/remotes/origin/HEAD current refs/remotes/origin/trunk\n"
                b"refs/remotes/origin/main old\nrefs/remotes/origin/trunk current\n")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin"), "base")
            self.assertEqual(process.call_args.args[0][-1], "current")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", side_effect=HookError("missing ancestry")) as process:
            with self.assertRaisesRegex(HookError, "missing ancestry"):
                new_branch_base("head", "origin")
            self.assertEqual(process.call_count, 1)

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
            with mock.patch("checks.run", return_value=b""), \
                    mock.patch("checks.parallel", side_effect=HookError("governance rejected")) as gate:
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
                self.assertEqual(calls, [["make", "--no-print-directory", "state-audit"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "run", "--path=."],
                                        ["go", "run", "./cmd/standardsctl", "gate", "verify", "--path=."]])
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
