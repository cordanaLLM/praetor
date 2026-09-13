#!/usr/bin/env python3
"""Focused real-Git tests for the read-only checkpoint planner."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).parent))
import checkpoint


CONFIG = {
    "version": 1, "enabled": True, "commit_after_minutes": 1440,
    "commit_after_files": 1, "on_stop": True, "publish": False, "remote": "origin",
    "base": "main", "repository": "acme/demo",
    "branch_prefixes": ["checkpoint/"], "require_pr": True,
}


def git(root, *args, check=True):
    env = dict(os.environ)
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"):
        env.pop(key, None)
    result = subprocess.run(["git", *args], cwd=root, env=env,
                            capture_output=True, timeout=20, check=False)
    if check and result.returncode:
        raise AssertionError(result.stderr.decode())
    return result


class CheckpointTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-checkpoint-")
        self.root = Path(self.temp.name)
        git(self.root, "init", "-q", "-b", "main")
        git(self.root, "config", "user.name", "Checkpoint Test")
        git(self.root, "config", "user.email", "checkpoint@example.test")
        self.write_config()
        (self.root / "README.md").write_text("initial\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "init")
        git(self.root, "switch", "-q", "-c", "checkpoint/test")

    def tearDown(self):
        self.temp.cleanup()

    def write_config(self, value=None):
        path = self.root / ".config" / "agent"
        path.mkdir(parents=True, exist_ok=True)
        (path / "checkpoint.json").write_text(json.dumps(value or CONFIG))

    def test_missing_config_is_explicitly_disabled(self):
        (self.root / ".config" / "agent" / "checkpoint.json").unlink()
        result = checkpoint.inspect_checkpoint(self.root, "tool")
        self.assertEqual(result["publication_status"], "disabled")
        self.assertFalse(result["enabled"])

    def test_malformed_config_is_error(self):
        self.write_config({**CONFIG, "unexpected": True})
        result = checkpoint.inspect_checkpoint(self.root, "tool")
        self.assertEqual(result["publication_status"], "error")
        self.assertIn("unknown or missing", result["error"])

    def test_private_only_changes_are_empty(self):
        (self.root / ".workingdir").mkdir()
        (self.root / ".workingdir" / "private.json").write_text("private\n")
        (self.root / ".standards-receipt.json").write_text("private\n")
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertEqual(result["changed_count"], 0)
        self.assertFalse(result["due"])
        self.assertEqual(result["publication_status"], "not_due")

    def test_threshold_and_stop_policy(self):
        (self.root / "public.txt").write_text("change\n")
        tool = checkpoint.inspect_checkpoint(self.root, "tool")
        self.assertTrue(tool["commit_due"])
        self.assertTrue(tool["due"])
        self.assertTrue(any("commit -s" in item for item in tool["actions"]))
        self.write_config({**CONFIG, "on_stop": False, "commit_after_files": 1000})
        stop = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertFalse(stop["due"])

    def test_protected_branch_is_error_when_due(self):
        git(self.root, "switch", "-q", "main")
        (self.root / "public.txt").write_text("change\n")
        result = checkpoint.inspect_checkpoint(self.root, "tool")
        self.assertEqual(result["publication_status"], "error")
        self.assertIn("protected", result["error"])

    def test_publish_false_does_not_query_network(self):
        (self.root / "public.txt").write_text("change\n")
        original = checkpoint._run
        calls = []

        def capture(argv, root, **kwargs):
            calls.append(argv)
            return original(argv, root, **kwargs)

        checkpoint._run = capture
        try:
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        finally:
            checkpoint._run = original
        self.assertEqual(result["publication_status"], "not_due")
        self.assertFalse(any(argv[:2] == ["gh", "pr"] for argv in calls))
        self.assertFalse(any("ls-remote" in argv for argv in calls))

    def test_detached_head_is_error_when_due(self):
        (self.root / "public.txt").write_text("change\n")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        git(self.root, "checkout", "-q", "--detach", head)
        result = checkpoint.inspect_checkpoint(self.root, "tool")
        self.assertEqual(result["publication_status"], "error")
        self.assertIn("detached", result["error"])

    def test_remote_parser_accepts_only_exact_github_forms(self):
        accepted = ("https://github.com/acme/demo", "https://github.com/acme/demo.git",
                    "git@github.com:acme/demo", "git@github.com:acme/demo.git",
                    "ssh://git@github.com/acme/demo", "ssh://git@github.com/acme/demo.git")
        rejected = ("https://evilgithub.com/acme/demo", "https://github.com/acme/demo/extra",
                    "https://user:pass@github.com/acme/demo", "ssh://git@github.com:22/acme/demo",
                    "https://github.com/acme/demo?token=x")
        for url in accepted:
            checkpoint._validate_remote(url, "acme/demo")
        for url in rejected:
            with self.assertRaises(checkpoint.CheckpointError):
                checkpoint._validate_remote(url, "acme/demo")

    def test_clean_ahead_checks_live_refs_and_requests_push(self):
        git(self.root, "remote", "add", "origin", "https://github.com/acme/demo.git")
        self.write_config({**CONFIG, "publish": True, "require_pr": False})
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "ahead")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        base = git(self.root, "rev-parse", "HEAD~1").stdout.decode().strip()
        original = checkpoint._run

        def fake(argv, root, **kwargs):
            if argv[:2] == ["git", "ls-remote"]:
                return f"{base}\trefs/heads/main\n".encode()
            return original(argv, root, **kwargs)

        checkpoint._run = fake
        try:
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        finally:
            checkpoint._run = original
        self.assertEqual(result["publication_status"], "due_push")
        self.assertFalse(result["commit_due"])
        self.assertTrue(result["due"])
        self.assertEqual(result["actions"], [checkpoint.PUSH_ACTION])

    def test_clean_pushed_exact_pr_is_present(self):
        git(self.root, "remote", "add", "origin", "git@github.com:acme/demo.git")
        self.write_config({**CONFIG, "publish": True})
        (self.root / "ahead.txt").write_text("ahead\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "ahead")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        base = git(self.root, "rev-parse", "HEAD~1").stdout.decode().strip()
        original = checkpoint._run

        def fake(argv, root, **kwargs):
            if argv[:2] == ["git", "ls-remote"]:
                return f"{head}\trefs/heads/checkpoint/test\n{base}\trefs/heads/main\n".encode()
            if argv[:2] == ["gh", "pr"]:
                return json.dumps([{"number": 7, "url": "https://github.com/acme/demo/pull/7",
                                     "isDraft": True, "headRefOid": head,
                                     "headRefName": "checkpoint/test", "baseRefName": "main"}]).encode()
            return original(argv, root, **kwargs)

        checkpoint._run = fake
        try:
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        finally:
            checkpoint._run = original
        self.assertEqual(result["publication_status"], "present")
        self.assertFalse(result["due"])
        self.assertEqual(result["changed_count"], 0)

    def test_unborn_branch_requests_initial_commit(self):
        git(self.root, "switch", "--orphan", "checkpoint/initial")
        self.write_config()
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertNotIn("error", result)
        self.assertEqual(result["head"], "")
        self.assertTrue(result["commit_due"])
        self.assertTrue(result["due"])

    def test_opposing_staged_and_unstaged_changes_still_count(self):
        (self.root / "README.md").write_text("staged change\n")
        git(self.root, "add", "README.md")
        (self.root / "README.md").write_text("initial\n")
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertEqual(result["changed_count"], 1)
        self.assertTrue(result["commit_due"])

    def test_staged_private_write_rejected_and_legacy_removal_allowed(self):
        private = self.root / ".workingdir/state.txt"
        private.parent.mkdir()
        private.write_text("PRIVATE_SENTINEL\n")
        git(self.root, "add", ".workingdir/state.txt")
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertIn("private .workingdir content is staged", result["error"])
        self.assertNotIn("PRIVATE_SENTINEL", json.dumps(result))
        git(self.root, "commit", "-q", "-m", "legacy private fixture")
        git(self.root, "rm", "--cached", ".workingdir/state.txt")
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertNotIn("error", result)

    def test_staged_receipt_does_not_replace_existing_privacy_gate(self):
        (self.root / ".standards-receipt.json").write_text("{}\n")
        git(self.root, "add", ".standards-receipt.json")
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertNotIn("error", result)
        self.assertFalse(result["due"])

    def test_policy_duplicate_keys_symlink_and_size_are_rejected(self):
        path = self.root / ".config/agent/checkpoint.json"
        path.write_text(json.dumps(CONFIG)[:-1] + ', "enabled": false}')
        self.assertIn("duplicate keys", checkpoint.inspect_checkpoint(self.root, "tool")["error"])
        path.unlink()
        path.symlink_to(self.root / "README.md")
        self.assertIn("error", checkpoint.inspect_checkpoint(self.root, "tool"))
        path.unlink()
        path.write_bytes(b" " * (checkpoint.MAX_OUTPUT + 1))
        self.assertIn("regular file", checkpoint.inspect_checkpoint(self.root, "tool")["error"])

    def test_policy_parent_symlink_is_rejected(self):
        directory = self.root / ".config/agent"
        target = self.root / "relocated"
        directory.rename(target)
        directory.symlink_to(target, target_is_directory=True)
        self.assertIn("error", checkpoint.inspect_checkpoint(self.root, "tool"))

    def test_pushed_missing_or_invalid_pr_never_reports_complete(self):
        git(self.root, "remote", "add", "origin", "https://github.com/acme/demo.git")
        self.write_config({**CONFIG, "publish": True})
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "publication fixture")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        base = git(self.root, "rev-parse", "HEAD~1").stdout.decode().strip()
        original = checkpoint._run
        valid = {"number": 7, "url": "https://github.com/acme/demo/pull/7", "isDraft": True,
                 "headRefOid": head, "headRefName": "checkpoint/test", "baseRefName": "main"}
        cases = [([], "due_draft_pr"), ([{**valid, "headRefOid": base}], "error"),
                 ([valid, valid], "error"), ({}, "error"),
                 ([{**valid, "url": "https://example.test/pull/7"}], "error"),
                 ([{**valid, "baseRefName": "wrong"}], "error")]
        for prs, status in cases:
            def fake(argv, root, **kwargs):
                if argv[:2] == ["git", "ls-remote"]:
                    return f"{head}\trefs/heads/checkpoint/test\n{base}\trefs/heads/main\n".encode()
                if argv[:2] == ["gh", "pr"]:
                    return json.dumps(prs).encode()
                return original(argv, root, **kwargs)
            with self.subTest(prs=prs), mock.patch.object(checkpoint, "_run", side_effect=fake):
                result = checkpoint.inspect_checkpoint(self.root, "stop")
                self.assertEqual(result["publication_status"], status)
                if status == "due_draft_pr":
                    self.assertTrue(result["due"])
                    self.assertEqual(result["actions"], [checkpoint.PR_ACTION])
        def denied(argv, root, **kwargs):
            if "ls-remote" in argv:
                raise checkpoint.CheckpointError("authentication failed")
            return original(argv, root, **kwargs)
        with mock.patch.object(checkpoint, "_run", side_effect=denied):
            self.assertIn("authentication failed", checkpoint.inspect_checkpoint(self.root, "stop")["error"])

    def test_remote_ref_and_object_validation(self):
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        for raw in (b"malformed\n", f"{head}\trefs/heads/main\n".encode() * 2):
            with mock.patch.object(checkpoint, "_git", return_value=raw):
                with self.assertRaises(checkpoint.CheckpointError):
                    checkpoint._remote_refs(self.root, CONFIG, "checkpoint/test")
        with mock.patch.object(checkpoint, "_remote_url", return_value="https://github.com/acme/demo"), \
                mock.patch.object(checkpoint, "_remote_refs", return_value=(
                    {"refs/heads/main": "f" * 40}, "refs/heads/checkpoint/test", "refs/heads/main")):
            with self.assertRaisesRegex(checkpoint.CheckpointError, "fetch the configured remote"):
                checkpoint._publication(self.root, CONFIG, "checkpoint/test", head)
        self.assertIsNotNone(checkpoint.OID.fullmatch("a" * 64))
        self.assertIsNone(checkpoint.OID.fullmatch("a" * 41))


if __name__ == "__main__":
    unittest.main()
