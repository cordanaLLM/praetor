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
import common


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

    def publication_fixture(self):
        git(self.root, "remote", "add", "origin", "https://github.com/acme/demo.git")
        self.write_config({**CONFIG, "publish": True, "require_pr": False})
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "local checkpoint")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        base = git(self.root, "rev-parse", "main").stdout.decode().strip()
        git(self.root, "update-ref", "refs/remotes/origin/checkpoint/test", head)
        return head, base

    def remote_commit(self, name, parent):
        git(self.root, "switch", "-q", "-c", name, parent)
        git(self.root, "commit", "-q", "--allow-empty", "-m", name)
        tip = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        git(self.root, "switch", "-q", "checkpoint/test")
        return tip

    def observe_remote(self, base, remote):
        original = checkpoint._run
        before = git(self.root, "show-ref").stdout

        def fake(argv, root, **kwargs):
            if argv[:2] == ["git", "ls-remote"]:
                branch_line = f"{remote}\trefs/heads/checkpoint/test\n" if remote else ""
                return f"{base}\trefs/heads/main\n{branch_line}".encode()
            if argv[:2] == ["gh", "pr"]:
                self.fail("PR lookup is disabled in this fixture")
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake):
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertEqual(git(self.root, "show-ref").stdout, before)
        self.assertFalse(result["commit_due"])
        return result

    def test_live_branch_ancestry_controls_publication_action(self):
        head, base = self.publication_fixture()
        ahead = self.remote_commit("remote-future", head)
        fork = self.remote_commit("remote-fork", base)
        cases = ((None, "due_push"), (base, "due_push"), (head, "pushed"),
                 (ahead, "remote_ahead"), (fork, "diverged"))
        for remote, status in cases:
            with self.subTest(status=status, remote=remote):
                result = self.observe_remote(base, remote)
                self.assertEqual(result["publication_status"], status, result)
                self.assertEqual(result["due"], status != "pushed")
                self.assertEqual(checkpoint.PUSH_ACTION in result["actions"], status == "due_push")
                if status in {"remote_ahead", "diverged"}:
                    self.assertIn("reconcile", " ".join(result["actions"]))

    def test_remote_movement_is_visible_even_without_commits_above_base(self):
        head, _ = self.publication_fixture()
        ahead = self.remote_commit("remote-future", head)
        result = self.observe_remote(head, ahead)
        self.assertEqual(result["publication_status"], "remote_ahead", result)
        self.assertTrue(result["due"])
        self.assertNotIn(checkpoint.PUSH_ACTION, result["actions"])
        self.assertEqual(self.observe_remote(head, None)["publication_status"], "not_ahead")
        self.assertEqual(self.observe_remote(head, head)["publication_status"], "not_ahead")

    def test_missing_remote_object_and_shallow_history_require_review(self):
        head, base = self.publication_fixture()
        result = self.observe_remote(base, "f" * 40)
        self.assertEqual(result["publication_status"], "error", result)
        self.assertIn("live branch object is unavailable", result["error"])
        self.assertNotIn(checkpoint.PUSH_ACTION, result["actions"])
        (self.root / ".git" / "shallow").write_text(head + "\n")
        result = self.observe_remote(base, base)
        self.assertEqual(result["publication_status"], "error", result)
        self.assertIn("shallow", result["error"])
        self.assertNotIn(checkpoint.PUSH_ACTION, result["actions"])

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

    def review_fixture(self):
        head, base = self.publication_fixture()
        self.write_config({**CONFIG, "publish": True, "require_checks": True,
                           "required_checks": ["gate"]})
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "require hosted checks")
        head = git(self.root, "rev-parse", "HEAD").stdout.decode().strip()
        return head, base

    def observe_review(self, head, base, checks, **overrides):
        original = checkpoint._run
        pr = {"number": 7, "url": "https://github.com/acme/demo/pull/7", "isDraft": True,
              "headRefOid": head, "headRefName": "checkpoint/test", "baseRefName": "main",
              "statusCheckRollup": checks, **overrides}

        def fake(argv, root, **kwargs):
            if argv[:2] == ["git", "ls-remote"]:
                return f"{head}\trefs/heads/checkpoint/test\n{base}\trefs/heads/main\n".encode()
            if argv[:3] == ["gh", "pr", "list"]:
                self.assertIn("statusCheckRollup", argv[-1])
                return json.dumps([pr]).encode()
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake):
            return checkpoint.inspect_checkpoint(self.root, "stop")

    def test_present_pr_requires_exact_head_checks_when_configured(self):
        head, base = self.review_fixture()
        check = {"__typename": "CheckRun", "name": "gate", "status": "COMPLETED"}
        cases = (("SUCCESS", "passed"), ("FAILURE", "failed"), ("CANCELLED", "failed"),
                 ("SKIPPED", "missing"), ("NEUTRAL", "missing"))
        for conclusion, status in cases:
            with self.subTest(conclusion=conclusion):
                result = self.observe_review(head, base, [{**check, "conclusion": conclusion}])
                self.assertEqual(result["publication_status"], "present", result)
                self.assertEqual(result["review_status"], status)
                self.assertEqual(result["review_head"], head)
                self.assertEqual(result["due"], status != "passed")
                self.assertFalse(result["commit_due"])
                self.assertNotIn(checkpoint.PUSH_ACTION, result["actions"])
        result = self.observe_review(head, base, [{**check, "conclusion": "SUCCESS"}], headRefOid=base)
        self.assertEqual(result["publication_status"], "error")
        self.assertNotEqual(result["review_status"], "passed")

    def test_review_empty_pending_and_missing_required_are_not_complete(self):
        head, base = self.review_fixture()
        pending = {"__typename": "CheckRun", "name": "gate", "status": "QUEUED", "conclusion": ""}
        success = {"__typename": "StatusContext", "context": "unrelated", "state": "SUCCESS"}
        for checks, status in (([], "missing"), ([pending], "pending"), ([success], "missing")):
            with self.subTest(status=status):
                result = self.observe_review(head, base, checks)
                self.assertEqual(result["review_status"], status, result)
                self.assertTrue(result["due"])
                self.assertEqual(result["required_checks_unpassed"], ["gate"])

    def test_review_mixed_duplicate_names_do_not_hide_failure(self):
        head, base = self.review_fixture()
        passed = {"__typename": "StatusContext", "context": "gate", "state": "SUCCESS"}
        failed = {**passed, "state": "ERROR"}
        result = self.observe_review(head, base, [passed, failed])
        self.assertEqual(result["review_status"], "failed", result)
        self.assertTrue(result["due"])
        self.assertEqual(result["check_counts"]["failed"], 1)

    def test_review_bounds_and_malformed_responses_fail_closed(self):
        head, base = self.review_fixture()
        passed = {"__typename": "StatusContext", "context": "gate", "state": "SUCCESS"}
        valid = self.observe_review(head, base, [passed] * (checkpoint.MAX_CHECKS - 1))
        self.assertEqual(valid["review_status"], "passed", valid)
        invalid = (None, {}, [passed] * checkpoint.MAX_CHECKS, [None],
                   [{**passed, "state": []}], [{**passed, "state": "NEW_UNKNOWN"}],
                   [{"__typename": "CheckRun", "name": "gate", "status": []}],
                   [{"__typename": "CheckRun", "name": "gate", "status": "IN_PROGRESS",
                     "conclusion": "SUCCESS"}])
        for checks in invalid:
            with self.subTest(checks=checks):
                result = self.observe_review(head, base, checks)
                self.assertEqual(result["publication_status"], "error", result)
                self.assertIn("error", result)

    def test_review_policy_is_strict_and_optional(self):
        invalid = ({"require_checks": "true"}, {"require_checks": True},
                   {"publish": True, "require_checks": True, "required_checks": []},
                   {"required_checks": ["gate"]}, {"required_checks": None},
                   {"publish": True, "require_checks": True, "required_checks": ["gate", "gate"]},
                   {"publish": True, "require_checks": True, "required_checks": ["gate\n"]},
                   {"publish": True, "require_checks": True, "required_checks": [str(i) for i in range(65)]})
        for extra in invalid:
            with self.subTest(extra=extra):
                self.write_config({**CONFIG, **extra})
                self.assertIn("error", checkpoint.inspect_checkpoint(self.root, "stop"))
        self.write_config(CONFIG)
        result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertEqual(result["review_status"], "not_requested")

    def test_clean_protected_base_observes_live_remote_without_push_advice(self):
        head, base = self.publication_fixture()
        future = self.remote_commit("remote-future", head)
        fork = self.remote_commit("remote-fork", base)
        git(self.root, "switch", "-q", "main")
        git(self.root, "merge", "--ff-only", "checkpoint/test")
        original = checkpoint._run
        cases = ((head, "base_current"), (base, "base_local_ahead"),
                 (future, "base_remote_ahead"), (fork, "base_diverged"))
        for remote, status in cases:
            def fake(argv, root, **kwargs):
                if argv[:2] == ["git", "ls-remote"]:
                    return f"{remote}\trefs/heads/main\n".encode()
                if argv[:2] == ["gh", "pr"]:
                    self.fail("base reconciliation must not query or create a PR")
                return original(argv, root, **kwargs)
            with self.subTest(status=status), mock.patch.object(checkpoint, "_run", side_effect=fake):
                result = checkpoint.inspect_checkpoint(self.root, "stop")
                self.assertEqual(result["publication_status"], status, result)
                self.assertEqual(result["due"], status != "base_current")
                self.assertNotIn(checkpoint.PUSH_ACTION, result["actions"])


class CheckedPolicyRead(unittest.TestCase):
    """The reader used where descriptor-relative opens are unavailable, exercised on every host.

    Windows selects it automatically; calling it directly keeps its refusals covered on the
    POSIX legs as well, where the descriptor-relative reader is the one in use.
    """

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-policy-")
        self.root = Path(self.temp.name)
        self.agent = self.root / ".config" / "agent"
        self.agent.mkdir(parents=True)
        self.policy = self.agent / "checkpoint.json"
        self.policy.write_bytes(json.dumps(CONFIG).encode())

    def tearDown(self):
        self.temp.cleanup()

    def test_positive_reads_the_policy_bytes(self):
        self.assertEqual(checkpoint._policy_bytes_checked(self.root), json.dumps(CONFIG).encode())

    def test_negative_links_and_misplaced_components_are_refused(self):
        outside = self.root / "outside.json"
        outside.write_bytes(json.dumps(CONFIG).encode())
        self.policy.unlink()
        self.policy.symlink_to(outside)
        with self.assertRaises(OSError):
            checkpoint._policy_bytes_checked(self.root)
        self.policy.unlink()
        relocated = self.root / "relocated"
        self.agent.rename(relocated)
        self.agent.symlink_to(relocated, target_is_directory=True)
        with self.assertRaises(OSError):
            checkpoint._policy_bytes_checked(self.root)
        self.agent.unlink()
        self.agent.write_text("not a directory")
        with self.assertRaises(NotADirectoryError):
            checkpoint._policy_bytes_checked(self.root)

    def test_boundary_absence_size_and_replacement(self):
        self.policy.write_bytes(b" " * checkpoint.MAX_OUTPUT)
        self.assertEqual(len(checkpoint._policy_bytes_checked(self.root)), checkpoint.MAX_OUTPUT)
        self.policy.write_bytes(b" " * (checkpoint.MAX_OUTPUT + 1))
        with self.assertRaisesRegex(checkpoint.CheckpointError, "regular file"):
            checkpoint._policy_bytes_checked(self.root)
        other = os.stat(self.root)
        with mock.patch.object(checkpoint.os, "fstat", return_value=other):
            with self.assertRaisesRegex(checkpoint.CheckpointError, "replaced"):
                checkpoint._policy_bytes_checked(self.root)
        self.policy.unlink()
        self.assertIsNone(checkpoint._policy_bytes_checked(self.root))
        self.assertIsNone(checkpoint._policy_bytes_checked(self.root / "absent"))


class ThreadedBoundedOutput(unittest.TestCase):
    """The pipe reader Windows uses because select() there accepts only sockets."""

    def start(self, code):
        process = subprocess.Popen([sys.executable, "-c", code], stdin=subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.stop, process)
        return process

    @staticmethod
    def stop(process):
        if process.poll() is None:
            process.kill()
        process.wait(timeout=10)
        process.stdout.close()
        process.stderr.close()

    def test_positive_collects_stdout_and_bounds_both_streams(self):
        process = self.start("import os; os.write(1, b'pass'); os.write(2, b'note')")
        self.assertEqual(common._bounded_output_threaded(process, 10, 8), b"pass")

    def test_negative_limit_and_deadline_are_raised_while_running(self):
        process = self.start("import os, time; os.write(1, b'x' * 4096); time.sleep(30)")
        with self.assertRaisesRegex(common.HookError, "byte limit"):
            common._bounded_output_threaded(process, 10, 1024)
        process = self.start("import time; time.sleep(30)")
        with self.assertRaisesRegex(common.HookError, "timed out"):
            common._bounded_output_threaded(process, 0.2, 1024)

    def test_boundary_output_exactly_at_the_limit_is_accepted(self):
        process = self.start("import os; os.write(1, b'x' * 512); os.write(2, b'y' * 512)")
        self.assertEqual(common._bounded_output_threaded(process, 10, 1024), b"x" * 512)


if __name__ == "__main__":
    unittest.main()
