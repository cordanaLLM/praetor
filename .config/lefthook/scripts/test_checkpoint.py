#!/usr/bin/env python3
"""Focused real-Git tests for the read-only checkpoint planner."""

import errno
import importlib.util
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
    for key in tuple(env):
        if key.startswith("GIT_CONFIG_"):
            env.pop(key)
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"):
        env.pop(key, None)
    result = subprocess.run(["git", *args], cwd=root, env=env,
                            capture_output=True, timeout=20, check=False)
    if check and result.returncode:
        raise AssertionError(result.stderr.decode())
    return result


ROOT = Path(__file__).resolve().parents[3]


ADAPTER_SPEC = importlib.util.spec_from_file_location(
    "checkpoint_adapter", ROOT / ".config/agent/hooks/checkpoint.py"
)


ADAPTER = importlib.util.module_from_spec(ADAPTER_SPEC)
ADAPTER_SPEC.loader.exec_module(ADAPTER)


DETACHED_ENV_KEYS = {
    "GH_CONFIG_DIR",
    "GH_TOKEN",
    "GITHUB_TOKEN",
    "GIT_SSH_COMMAND",
    "GIT_TERMINAL_PROMPT",
    "PRAETOR_DETACHED_JOB",
}



def setUpModule():
    """Run every test without the detached-job environment of the process running them: a cheap-lane
    job sets PRAETOR_DETACHED_JOB and disables push transport, and the checkpoint then defers, which
    every test that expects a due checkpoint must not see. The detached-job tests set their own."""
    for key in DETACHED_ENV_KEYS:
        os.environ.pop(key, None)


class CheckpointAdapterTests(unittest.TestCase):
    def test_deferred_report_is_feedback_without_a_block(self):
        report = {
            "enabled": True,
            "due": False,
            "actions": [],
            "deferred": True,
            "deferral": "detached job lane-1: no push transport, no forge token; "
            "the launching session commits",
        }
        for payload in (
            {"hook_event_name": "Stop"},
            {"hook_event_name": "AfterAgent", "stop_hook_active": True},
        ):
            with self.subTest(payload=payload), mock.patch.object(
                ADAPTER, "verify_state"
            ), mock.patch.object(ADAPTER, "checkpoint", return_value=report):
                result = ADAPTER.respond(payload)
                self.assertEqual(
                    result,
                    {"systemMessage": "Praetor checkpoint deferred: " + report["deferral"]},
                )
                self.assertNotIn("decision", result)
                self.assertNotIn("continue", result)

    def test_due_report_still_blocks(self):
        report = {"enabled": True, "due": True, "actions": ["commit"]}
        for payload in (
            {"hook_event_name": "Stop"},
            {"hook_event_name": "AfterAgent", "stop_hook_active": True},
        ):
            with self.subTest(payload=payload), mock.patch.object(
                ADAPTER, "verify_state"
            ), mock.patch.object(ADAPTER, "checkpoint", return_value=report):
                result = ADAPTER.respond(payload)
                if payload.get("stop_hook_active"):
                    self.assertIs(result["continue"], False)
                else:
                    self.assertEqual(result["decision"], "block")


class CheckpointTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-checkpoint-")
        self.root = Path(self.temp.name)
        self.external_paths = []
        git(self.root, "init", "-q", "-b", "main")
        git(self.root, "config", "user.name", "Checkpoint Test")
        git(self.root, "config", "user.email", "checkpoint@example.test")
        self.write_config()
        (self.root / "README.md").write_text("initial\n")
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", "init")
        git(self.root, "switch", "-q", "-c", "checkpoint/test")

    def tearDown(self):
        for path in self.external_paths:
            shutil.rmtree(path, ignore_errors=True)
        self.temp.cleanup()

    def write_config(self, value=None):
        path = self.root / ".config" / "agent"
        path.mkdir(parents=True, exist_ok=True)
        (path / "checkpoint.json").write_text(json.dumps(value or CONFIG))

    def add_linked_worktree(self, branch="checkpoint/orphan"):
        suffix = "-linked-worktree" if branch == "checkpoint/orphan" else f"-linked-worktree-{branch.replace('/', '-')}"
        path = self.root.parent / (self.root.name + suffix)
        self.external_paths.append(path)
        git(self.root, "worktree", "add", "-q", "-b", branch, str(path), "HEAD")
        return path

    def observe_worktrees(self, *, open_prs=None, fail_gh=False):
        original = checkpoint._run
        calls = []

        def fake(argv, root, **kwargs):
            calls.append(argv)
            if argv[:3] == ["gh", "pr", "list"]:
                if fail_gh:
                    raise checkpoint.CheckpointError("fake gh unavailable")
                return json.dumps(open_prs or []).encode()
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake):
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        return result, calls

    def test_stop_linked_public_change_blocks_and_names_worktree(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        # git reports the worktree by its resolved path: /private/var/... for macOS's /var/...
        expected = str(path.resolve())
        self.assertTrue(any(expected in action for action in result["actions"]))
        self.assertEqual(result["unfinished_worktrees"][0]["path"], expected)

    def test_stop_linked_public_change_is_detected_when_head_is_remote_clean(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        git(path, "fetch", "-q", "origin")
        (path / "public.txt").write_text("unfinished\n")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertEqual(
            result["unfinished_worktrees"][0]["reasons"],
            ["1 public change(s)", checkpoint.OWNERSHIP_UNKNOWN_REASON],
        )

    def test_stop_unowned_worktree_does_not_block(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        session_hash = "0123456789abcdef"
        sessions_dir = (self.root / ".git" / "praetor-checkpoint" / "sessions").resolve()
        sessions_dir.mkdir(parents=True, exist_ok=True)
        (sessions_dir / f"{session_hash}.json").write_text("[]\n")
        with mock.patch.dict(os.environ, {checkpoint.SESSION_ENV: session_hash}):
            result, _ = self.observe_worktrees()
        self.assertFalse(result["due"])
        self.assertEqual(result.get("unfinished_worktrees", []), [])

    def test_stop_owned_worktree_blocks_as_today(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        session_hash = "0123456789abcdef"
        sessions_dir = (self.root / ".git" / "praetor-checkpoint" / "sessions").resolve()
        sessions_dir.mkdir(parents=True, exist_ok=True)
        (sessions_dir / f"{session_hash}.json").write_text(json.dumps([str(path.resolve())]))
        with mock.patch.dict(os.environ, {checkpoint.SESSION_ENV: session_hash}):
            result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertEqual(result["unfinished_worktrees"][0]["path"], str(path.resolve()))
        self.assertNotIn(checkpoint.OWNERSHIP_UNKNOWN_REASON, result["unfinished_worktrees"][0]["reasons"])

    def test_gate_pid_reused_by_other_process_is_not_skipped(self):
        self.assertFalse(checkpoint._live_gate_pid(os.getpid()))
        path = self.root / ".standards/worktrees" / f"gate-{os.getpid()}-1"
        self.assertEqual(checkpoint._gate_scratch_pid(path, {self.root}), os.getpid())
        self.assertFalse(checkpoint._is_gate_scratch(path, {self.root}))
        self.assertEqual(checkpoint._gate_scratch_pid(path / "nested", {self.root}), os.getpid())
        self.assertIsNone(checkpoint._gate_scratch_pid(path, {self.root / "foreign"}))
        self.assertIsNone(checkpoint._gate_scratch_pid(path.parent / "gate-x", {self.root}))

    def test_stop_dead_gate_scratch_worktree_blocks_even_when_unowned(self):
        (self.root / ".gitignore").write_text("/.standards/worktrees/\n")
        git(self.root, "add", ".gitignore")
        git(self.root, "commit", "-q", "-m", "ignore scratch")
        scratch = self.root / ".standards" / "worktrees" / "gate-2147483647-1"
        self.external_paths.append(scratch)
        git(self.root, "worktree", "add", "-q", "-b", "gate-2147483647-1", str(scratch), "HEAD")
        (scratch / "public.txt").write_text("unfinished\n")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertIn("gate owner pid 2147483647 is not a live praetorctl gate run",
                      result["unfinished_worktrees"][0]["reasons"])
        session_hash = "0123456789abcdef"
        sessions_dir = (self.root / ".git" / "praetor-checkpoint" / "sessions").resolve()
        sessions_dir.mkdir(parents=True, exist_ok=True)
        (sessions_dir / f"{session_hash}.json").write_text(json.dumps([]))
        with mock.patch.dict(os.environ, {checkpoint.SESSION_ENV: session_hash}):
            result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertIn("gate owner pid 2147483647 is not a live praetorctl gate run",
                      result["unfinished_worktrees"][0]["reasons"])

    def test_stop_no_session_variable_names_ownership_unknown(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop(checkpoint.SESSION_ENV, None)
            result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertIn(checkpoint.OWNERSHIP_UNKNOWN_REASON, result["unfinished_worktrees"][0]["reasons"])

    def test_stop_open_line_names_branch(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        state = self.root / ".workingdir" / "OPEN.md"
        state.parent.mkdir(parents=True)
        state.write_text("ORPHAN: checkpoint/orphan\n")
        result, _ = self.observe_worktrees()
        self.assertFalse(result["due"])
        self.assertEqual(result.get("unfinished_worktrees", []), [])

    def test_stop_open_line_names_directory_or_whole_path(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        state = self.root / ".workingdir" / "OPEN.md"
        state.parent.mkdir(parents=True)
        for token in (path.name, str(path)):
            state.write_text(f"ORPHAN: {token}\n")
            result, _ = self.observe_worktrees()
            self.assertFalse(result["due"], token)

    def test_open_longer_token_does_not_name_branch(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        state = self.root / ".workingdir" / "OPEN.md"
        state.parent.mkdir(parents=True)
        state.write_text("ORPHAN: prefix-checkpoint/orphan-suffix\n")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])

    def test_stop_open_line_names_key_value_and_path_forms(self):
        # The inventory lines write "branch=<branch>" and a path under the dev root; a sentence
        # may end with the name. Each form names the worktree (engine-orphan-inventory-triage).
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        state = self.root / ".workingdir" / "OPEN.md"
        state.parent.mkdir(parents=True)
        for line in (
            "- [ ] ORPHAN-20261017 dev/other branch=checkpoint/orphan head=abc",
            f"- [ ] ORPHAN-20261017 dev/repo/worktrees/{path.name} branch=x",
            f"see worktrees/{path.name}.",
        ):
            state.write_text(line + "\n")
            result, _ = self.observe_worktrees()
            self.assertFalse(result["due"], line)

    def test_open_extended_or_dotted_name_does_not_name_worktree(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        state = self.root / ".workingdir" / "OPEN.md"
        state.parent.mkdir(parents=True)
        for line in (f"{path.name}.old", f"{path.name}x", f"old-{path.name}", "branch=checkpoint/orphan2"):
            state.write_text(line + "\n")
            result, _ = self.observe_worktrees()
            self.assertTrue(result["due"], line)

    def test_stop_branch_whose_change_landed_by_squash_is_not_due(self):
        # A train or squash merge lands the change under another commit: the branch keeps commits
        # on no remote and has no pull request of its own head, yet nothing of it is unfinished.
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("landed\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "unit")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"], "before the squash lands")
        git(self.root, "switch", "-q", "main")
        (self.root / "new.txt").write_text("landed\n")
        git(self.root, "add", "new.txt")
        git(self.root, "commit", "-q", "-m", "train (unit)")
        git(self.root, "push", "-q", "origin", "main")
        git(self.root, "switch", "-q", "checkpoint/test")
        git(path, "fetch", "-q", "origin")
        result, _ = self.observe_worktrees()
        self.assertEqual(result.get("unfinished_worktrees", []), [])
        (path / "new.txt").write_text("changed after landing\n")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"], "a public change after the landing is still unfinished")

    def test_stop_worktree_commit_without_remote_blocks(self):
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("commit\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "unpublished")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        self.assertIn("commit", " ".join(result["unfinished_worktrees"][0]["reasons"]))

    def test_stop_pushed_ahead_branch_without_pr_blocks_but_merged_pr_passes(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("commit\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "published")
        git(path, "push", "-q", "-u", "origin", "checkpoint/orphan")
        git(path, "fetch", "-q", "origin")
        result, _ = self.observe_worktrees()
        self.assertTrue(result["due"])
        for state in ("OPEN", "MERGED"):
            result, _ = self.observe_worktrees(
                open_prs=[{"headRefName": "checkpoint/orphan", "state": state}]
            )
            self.assertFalse(result["due"], state)

    def test_stop_worktree_gh_failure_is_unverified_unfinished(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("commit\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "published")
        git(path, "push", "-q", "-u", "origin", "checkpoint/orphan")
        git(path, "fetch", "-q", "origin")
        result, _ = self.observe_worktrees(fail_gh=True)
        self.assertTrue(result["due"])
        self.assertEqual(result.get("unfinished_worktrees", []), [])
        self.assertEqual(len(result["actions"]), 1)
        self.assertIn("fake gh unavailable", result["actions"][0])
        self.assertIn("1", result["actions"][0])
        self.assertEqual(result.get("pull_request_check_skipped"), "fake gh unavailable")

    def test_stop_worktree_pr_listing_retry_succeeds_without_unfinished_record(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("commit\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "published")
        git(path, "push", "-q", "-u", "origin", "checkpoint/orphan")
        git(path, "fetch", "-q", "origin")
        attempts = [0]
        original = checkpoint._run

        def fake_run(argv, root, **kwargs):
            if argv[:3] == ["gh", "pr", "list"] and "--head" not in argv:
                attempts[0] += 1
                if attempts[0] < 3:
                    raise checkpoint.CheckpointError("transient listing failure")
                return json.dumps([{"headRefName": "checkpoint/orphan", "state": "MERGED"}]).encode()
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake_run), mock.patch.object(
            checkpoint.time, "sleep"
        ) as sleep_mock:
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertEqual(attempts[0], 3)
        delays = [call.args[0] for call in sleep_mock.call_args_list if call.args and call.args[0] in (1, 2)]
        self.assertEqual(delays, [1, 2])
        self.assertEqual(result.get("unfinished_worktrees", []), [])
        self.assertEqual(result.get("actions", []), [])
        self.assertFalse(result["due"])

    def test_stop_worktree_pr_listing_failure_names_error_once_with_count(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        for i in range(4):
            path = self.add_linked_worktree(f"checkpoint/orphan-{i}")
            (path / f"new-{i}.txt").write_text(f"commit {i}\n")
            git(path, "add", f"new-{i}.txt")
            git(path, "commit", "-q", "-m", f"published {i}")
            git(path, "push", "-q", "-u", "origin", f"checkpoint/orphan-{i}")
            git(path, "fetch", "-q", "origin")
        original = checkpoint._run

        def fake_run(argv, root, **kwargs):
            if argv[:3] == ["gh", "pr", "list"] and "--head" not in argv:
                raise checkpoint.CheckpointError("listing failed permanently")
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake_run), mock.patch.object(
            checkpoint.time, "sleep"
        ):
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertTrue(result["due"])
        self.assertEqual(len(result["actions"]), 1)
        self.assertIn("listing failed permanently", result["actions"][0])
        self.assertIn("4", result["actions"][0])
        self.assertEqual(result.get("pull_request_check_skipped"), "listing failed permanently")
        for item in result.get("unfinished_worktrees", []):
            self.assertNotIn("pull request", " ".join(item.get("reasons", [])))
        self.assertEqual(result.get("unfinished_worktrees", []), [])

    def test_stop_worktree_pr_listing_spent_deadline_no_retry_sleep(self):
        cfg = {"enabled": True, "repository": "acme/demo"}
        with mock.patch.object(
            checkpoint,
            "_run",
            side_effect=checkpoint.CheckpointError("listing deadline error"),
        ), mock.patch.object(checkpoint.time, "sleep") as sleep_mock:
            states, error = checkpoint._list_pull_requests(
                self.root, cfg, deadline=time.monotonic() + 0.5
            )
        self.assertFalse(any(call.args and call.args[0] in (1, 2) for call in sleep_mock.call_args_list))
        self.assertIsNone(states)
        self.assertEqual(error, "listing deadline error")

    def test_stop_worktree_probe_failure_keeps_unverified_unfinished(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        (path / "new.txt").write_text("commit\n")
        git(path, "add", "new.txt")
        git(path, "commit", "-q", "-m", "published")
        git(path, "push", "-q", "-u", "origin", "checkpoint/orphan")
        git(path, "fetch", "-q", "origin")
        original = checkpoint._run

        def fake_run(argv, root, **kwargs):
            if argv[:3] == ["gh", "pr", "list"]:
                if "--head" in argv:
                    raise checkpoint.CheckpointError("probe failure")
                return b"[]"
            return original(argv, root, **kwargs)

        with mock.patch.object(checkpoint, "_run", side_effect=fake_run):
            result = checkpoint.inspect_checkpoint(self.root, "stop")
        self.assertTrue(result["due"])
        self.assertEqual(len(result["unfinished_worktrees"]), 1)
        self.assertIn("pull request state not verified", " ".join(result["unfinished_worktrees"][0]["reasons"]))

    def test_stop_old_worktree_admin_is_not_judged(self):
        path = self.add_linked_worktree()
        (path / "public.txt").write_text("unfinished\n")
        gitdir = Path(git(path, "rev-parse", "--absolute-git-dir").stdout.decode().strip())
        old = time.time() - checkpoint.WORKTREE_ACTIVITY_WINDOW - 10
        for item in (gitdir / "HEAD", gitdir / "index", gitdir / "logs" / "HEAD", gitdir):
            if item.exists():
                os.utime(item, (old, old))
        result, _ = self.observe_worktrees()
        self.assertFalse(result["due"])

    def test_stop_clean_remote_equal_worktree_is_not_due_and_root_is_omitted(self):
        bare = self.root.parent / (self.root.name + "-remote.git")
        self.external_paths.append(bare)
        git(self.root, "init", "--bare", "-q", str(bare))
        git(self.root, "remote", "add", "origin", str(bare))
        git(self.root, "push", "-q", "-u", "origin", "main")
        path = self.add_linked_worktree()
        git(path, "push", "-q", "-u", "origin", "checkpoint/orphan")
        git(path, "fetch", "-q", "origin")
        result, calls = self.observe_worktrees()
        self.assertFalse(result["due"])
        self.assertEqual(result.get("unfinished_worktrees", []), [])
        self.assertFalse(any(call[:2] == ["gh", "pr"] for call in calls))
        (self.root / "root-change.txt").write_text("root work\n")
        result, _ = self.observe_worktrees()
        reported = [str(Path(item["path"]).resolve()) for item in result.get("unfinished_worktrees", [])]
        self.assertNotIn(str(self.root.resolve()), reported)

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

    def observe_review(self, head, base, checks, annotations=None, **overrides):
        """Observe a review; annotations is the GraphQL check-run answer, by default none."""
        if annotations is None:
            annotations = self.check_runs()
        original = checkpoint._run
        pr = {"number": 7, "url": "https://github.com/acme/demo/pull/7", "isDraft": True,
              "headRefOid": head, "headRefName": "checkpoint/test", "baseRefName": "main",
              "statusCheckRollup": checks, **overrides}
        self.graphql_calls = []

        def fake(argv, root, **kwargs):
            if argv[:2] == ["git", "ls-remote"]:
                return f"{head}\trefs/heads/checkpoint/test\n{base}\trefs/heads/main\n".encode()
            if argv[:3] == ["gh", "pr", "list"]:
                self.assertIn("statusCheckRollup", argv[-1])
                return json.dumps([pr]).encode()
            if argv[:3] == ["gh", "api", "graphql"]:
                self.graphql_calls.append(argv)
                self.assertTrue(kwargs.get("network"))
                return annotations if isinstance(annotations, bytes) else json.dumps(annotations).encode()
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

    def rerun(self, conclusion, started, workflow="CI", status="COMPLETED"):
        return {"__typename": "CheckRun", "name": "gate", "workflowName": workflow,
                "status": status, "conclusion": conclusion, "startedAt": started}

    def test_review_rerun_supersedes_earlier_run_of_same_job(self):
        head, base = self.review_fixture()
        early, late = "2026-10-06T15:14:18Z", "2026-10-06T15:24:47Z"
        cases = (
            ("passing rerun after failure", [self.rerun("FAILURE", early), self.rerun("SUCCESS", late)], "passed"),
            ("failing rerun after pass", [self.rerun("SUCCESS", early), self.rerun("FAILURE", late)], "failed"),
            ("running rerun after failure",
             [self.rerun("FAILURE", early), self.rerun("", late, status="IN_PROGRESS")], "pending"),
            ("listed newest first", [self.rerun("SUCCESS", late), self.rerun("FAILURE", early)], "passed"),
        )
        for label, checks, status in cases:
            with self.subTest(label):
                result = self.observe_review(head, base, checks)
                self.assertEqual(result["review_status"], status, result)
                self.assertEqual(result["due"], status != "passed")

    def test_review_rerun_never_hides_another_workflow_or_untimed_run(self):
        head, base = self.review_fixture()
        early, late = "2026-10-06T15:14:18Z", "2026-10-06T15:24:47Z"
        cases = (
            ("other workflow", [self.rerun("FAILURE", early, workflow="Other"), self.rerun("SUCCESS", late)]),
            ("no workflow name", [self.rerun("FAILURE", early, workflow=""), self.rerun("SUCCESS", late)]),
            ("not started", [self.rerun("FAILURE", "0001-01-01T00:00:00Z"), self.rerun("SUCCESS", late)]),
            ("malformed start", [self.rerun("FAILURE", "yesterday"), self.rerun("SUCCESS", late)]),
            ("same start", [self.rerun("FAILURE", late), self.rerun("SUCCESS", late)]),
        )
        for label, checks in cases:
            with self.subTest(label):
                result = self.observe_review(head, base, checks)
                self.assertEqual(result["review_status"], "failed", result)
                self.assertTrue(result["due"])

    DRAFT_NOTE = {"title": checkpoint.DRAFT_GATE_TITLE, "message": checkpoint.DRAFT_GATE_MESSAGE}
    EXIT_NOTE = {"title": "", "message": "Process completed with exit code 1."}

    @staticmethod
    def check_runs(*runs):
        """A GraphQL commit answer holding one check suite with runs of (name, annotations)."""
        nodes = [{"name": name, "annotations": {"nodes": notes}} for name, notes in runs]
        return {"data": {"repository": {"object": {
            "checkSuites": {"nodes": [{"checkRuns": {"nodes": nodes}}]}}}}}

    def test_review_draft_refusal_is_draft_pending(self):
        head, base = self.review_fixture()
        failed = {"__typename": "CheckRun", "name": "gate", "status": "COMPLETED",
                  "conclusion": "FAILURE"}
        marked = self.check_runs(("gate", [self.DRAFT_NOTE, self.EXIT_NOTE]))
        result = self.observe_review(head, base, [failed], annotations=marked)
        self.assertEqual(result["review_status"], "draft_pending", result)
        self.assertEqual(result["check_counts"]["draft_pending"], 1)
        self.assertEqual(result["check_counts"]["failed"], 0)
        self.assertEqual(result["required_checks_unpassed"], ["gate"])
        self.assertTrue(result["due"])
        argv = self.graphql_calls[0]
        self.assertIn(f"oid={head}", argv)
        self.assertIn("owner=acme", argv)
        self.assertIn("name=demo", argv)

    def test_review_draft_real_failure_stays_failed(self):
        head, base = self.review_fixture()
        failed = {"__typename": "CheckRun", "name": "gate", "status": "COMPLETED",
                  "conclusion": "FAILURE"}
        other_title = {**self.DRAFT_NOTE, "title": "Gate not run"}
        other_message = {**self.DRAFT_NOTE, "message": checkpoint.DRAFT_GATE_MESSAGE + " "}
        cases = (
            ("exit annotation only", self.check_runs(("gate", [self.EXIT_NOTE]))),
            ("other title", self.check_runs(("gate", [other_title]))),
            ("other message", self.check_runs(("gate", [other_message]))),
            ("one unmarked run", self.check_runs(("gate", [self.DRAFT_NOTE]), ("gate", [self.EXIT_NOTE]))),
            ("marker on another check", self.check_runs(("other", [self.DRAFT_NOTE]))),
            ("no failed run listed", self.check_runs()),
        )
        for label, answer in cases:
            with self.subTest(label):
                result = self.observe_review(head, base, [failed], annotations=answer)
                self.assertEqual(result["review_status"], "failed", result)
                self.assertEqual(result["check_counts"]["draft_pending"], 0)
        marked = self.check_runs(("gate", [self.DRAFT_NOTE]))
        for conclusion in ("CANCELLED", "TIMED_OUT", "STARTUP_FAILURE"):
            with self.subTest(conclusion=conclusion):
                result = self.observe_review(head, base, [{**failed, "conclusion": conclusion}])
                self.assertEqual(result["review_status"], "failed", result)
                self.assertEqual(self.graphql_calls, [])
        status = {"__typename": "StatusContext", "context": "gate", "state": "FAILURE"}
        result = self.observe_review(head, base, [status, failed], annotations=marked)
        self.assertEqual(result["review_status"], "failed", result)

    def test_review_checkpoint_push_run_beside_draft_run(self):
        # ci.yml runs on a push to checkpoint/** as well as on the branch's draft pull request.
        # The push event carries no pull request, so its run does the work and reports the same
        # job name in the same workflow as the draft run that stopped in its draft step. The
        # newest run of the job decides; a failed push run is never read as draft pending.
        head, base = self.review_fixture()
        early, late = "2026-10-07T18:00:01Z", "2026-10-07T18:00:02Z"
        draft_marked = self.check_runs(("gate", [self.DRAFT_NOTE, self.EXIT_NOTE]))
        push_failed = self.check_runs(("gate", [self.DRAFT_NOTE]), ("gate", [self.EXIT_NOTE]))
        cases = (
            ("draft run newest, push run passed",
             [self.rerun("SUCCESS", early), self.rerun("FAILURE", late)], draft_marked, "draft_pending"),
            ("draft run newest, push run running",
             [self.rerun("", early, status="IN_PROGRESS"), self.rerun("FAILURE", late)], draft_marked,
             "draft_pending"),
            ("draft run newest, push run failed",
             [self.rerun("FAILURE", early), self.rerun("FAILURE", late)], push_failed, "failed"),
            ("push run newest and passed",
             [self.rerun("FAILURE", early), self.rerun("SUCCESS", late)], draft_marked, "passed"),
            ("push run newest and failed",
             [self.rerun("FAILURE", early), self.rerun("FAILURE", late)], push_failed, "failed"),
        )
        for label, checks, answer, status in cases:
            with self.subTest(label):
                result = self.observe_review(head, base, checks, annotations=answer)
                self.assertEqual(result["review_status"], status, result)
                self.assertEqual(result["due"], status != "passed")

    def test_review_ready_pull_request_with_draft_marker_is_failed(self):
        head, base = self.review_fixture()
        failed = {"__typename": "CheckRun", "name": "gate", "status": "COMPLETED",
                  "conclusion": "FAILURE"}
        result = self.observe_review(head, base, [failed], isDraft=False)
        self.assertEqual(result["review_status"], "failed", result)
        self.assertEqual(self.graphql_calls, [])
        passed = {**failed, "conclusion": "SUCCESS"}
        result = self.observe_review(head, base, [passed])
        self.assertEqual(result["review_status"], "passed", result)
        self.assertEqual(self.graphql_calls, [])

    def test_review_draft_annotation_bounds_and_malformed_answers_fail_closed(self):
        head, base = self.review_fixture()
        failed = {"__typename": "CheckRun", "name": "gate", "status": "COMPLETED",
                  "conclusion": "FAILURE"}
        at_bound = self.check_runs(("gate", [self.EXIT_NOTE] * (checkpoint.MAX_DRAFT_ANNOTATIONS - 1)
                                    + [self.DRAFT_NOTE]))
        result = self.observe_review(head, base, [failed], annotations=at_bound)
        self.assertEqual(result["review_status"], "draft_pending", result)
        over = self.check_runs(("gate", [self.DRAFT_NOTE] * (checkpoint.MAX_DRAFT_ANNOTATIONS + 1)))
        invalid = (b"not json", {}, {"data": {"repository": None}},
                   {"data": {"repository": {"object": {"checkSuites": {"nodes": [{}]}}}}},
                   self.check_runs((None, [self.DRAFT_NOTE])), over)
        for answer in invalid:
            with self.subTest(answer=answer):
                result = self.observe_review(head, base, [failed], annotations=answer)
                self.assertEqual(result["publication_status"], "error", result)
                self.assertIn("error", result)

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

    def inspect_with_env(self, **updates):
        env = {
            key: value
            for key, value in os.environ.items()
            if key not in DETACHED_ENV_KEYS and not key.startswith("GIT_CONFIG_")
        }
        env.update(updates)
        with mock.patch.dict(os.environ, env, clear=True):
            return checkpoint.inspect_checkpoint(self.root, "stop")

    def prepare_detached_job(self, remote):
        git(self.root, "remote", "add", "origin", remote)
        (self.root / "public.txt").write_text("change\n")

    def test_detached_job_without_ssh_transport_or_forge_token_defers(self):
        self.prepare_detached_job("git@github.com:acme/demo.git")
        result = self.inspect_with_env(
            PRAETOR_DETACHED_JOB="cheap-lane-7", GIT_SSH_COMMAND="/bin/false"
        )
        self.assertTrue(result["deferred"])
        self.assertFalse(result["due"])
        self.assertEqual(result["actions"], [])
        self.assertEqual(result["publication_status"], "deferred_detached_job")
        self.assertIn("detached job cheap-lane-7", result["deferral"])

    def test_detached_job_defers_on_a_branch_outside_the_checkpoint_prefixes(self):
        self.prepare_detached_job("git@github.com:acme/demo.git")
        git(self.root, "switch", "-q", "-c", "job-lane-probe")
        result = self.inspect_with_env(
            PRAETOR_DETACHED_JOB="cheap-lane-7", GIT_SSH_COMMAND="/bin/false"
        )
        self.assertNotIn("error", result)
        self.assertTrue(result["deferred"])
        self.assertFalse(result["due"])

    def test_same_branch_without_marker_still_refuses(self):
        self.prepare_detached_job("git@github.com:acme/demo.git")
        git(self.root, "switch", "-q", "-c", "job-lane-probe")
        result = self.inspect_with_env(GIT_SSH_COMMAND="/bin/false")
        self.assertFalse(result["deferred"])
        self.assertIn("nonallowed branch", result.get("error", ""))

    def test_detached_job_with_ssh_transport_keeps_commit_due(self):
        self.prepare_detached_job("git@github.com:acme/demo.git")
        result = self.inspect_with_env(PRAETOR_DETACHED_JOB="cheap-lane-7")
        self.assertFalse(result["deferred"])
        self.assertTrue(result["due"])
        self.assertNotIn("deferral", result)

    def test_detached_job_with_forge_token_keeps_commit_due(self):
        self.prepare_detached_job("ssh://git@github.com/acme/demo.git")
        gh_dir = self.root / ".gh"
        gh_dir.mkdir()
        (gh_dir / "hosts.yml").write_text("github.com:\n  oauth_token: secret\n")
        cases = (
            {"GH_TOKEN": "token-present"},
            {"GITHUB_TOKEN": "token-present"},
            {"GH_CONFIG_DIR": str(gh_dir)},
        )
        for env in cases:
            with self.subTest(env=env):
                result = self.inspect_with_env(
                    PRAETOR_DETACHED_JOB="cheap-lane-7",
                    GIT_SSH_COMMAND="/usr/bin/false -v",
                    **env,
                )
                self.assertFalse(result["deferred"])
                self.assertTrue(result["due"])
                self.assertNotIn("deferral", result)
        empty_dir = self.root / ".empty-gh"
        empty_dir.mkdir()
        deferred = self.inspect_with_env(
            PRAETOR_DETACHED_JOB="cheap-lane-7",
            GIT_SSH_COMMAND="/usr/bin/false -v",
            GH_CONFIG_DIR=str(empty_dir),
        )
        self.assertTrue(deferred["deferred"])
        self.assertFalse(deferred["due"])

    def test_no_job_marker_keeps_commit_due_without_publish_access(self):
        self.prepare_detached_job("git@github.com:acme/demo.git")
        result = self.inspect_with_env(GIT_SSH_COMMAND="/bin/false")
        self.assertFalse(result["deferred"])
        self.assertTrue(result["due"])

    def test_git_helper_ignores_environment_git_configuration(self):
        # #1370: GIT_CONFIG_COUNT/KEY/VALUE outrank repository configuration, and a cheap-lane
        # job carries them. Negative: a plain git call under that environment reads the
        # environment's value. Positive: the fixture helper reads the repository's own value.
        git(self.root, "config", "credential.helper", "store")
        injected = {"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "lane"}
        with mock.patch.dict(os.environ, injected):
            raw = subprocess.run(["git", "config", "--get", "credential.helper"], cwd=self.root,
                                 capture_output=True, check=True, timeout=30)
            self.assertEqual(raw.stdout.decode().strip(), "lane")
            helper = git(self.root, "config", "--get", "credential.helper")
        self.assertEqual(helper.stdout.decode().strip(), "store")

    def test_detached_job_https_transport_depends_on_credential_helper(self):
        self.prepare_detached_job("https://github.com/acme/demo.git")
        git(self.root, "config", "credential.helper", "")
        deferred = self.inspect_with_env(
            PRAETOR_DETACHED_JOB="cheap-lane-https", GIT_TERMINAL_PROMPT="0"
        )
        self.assertTrue(deferred["deferred"])
        git(self.root, "config", "--replace-all", "credential.helper", "store")
        self.assertEqual(
            git(self.root, "config", "--get", "credential.helper")
            .stdout.decode()
            .strip(),
            "store",
        )
        due = self.inspect_with_env(
            PRAETOR_DETACHED_JOB="cheap-lane-https", GIT_TERMINAL_PROMPT="0"
        )
        self.assertFalse(due["deferred"])
        self.assertTrue(due["due"])


class PolicyRoot(unittest.TestCase):
    """A repository root holding one checkpoint policy, for the tests of its two readers."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-policy-")
        self.root = Path(self.temp.name)
        self.agent = self.root / ".config" / "agent"
        self.agent.mkdir(parents=True)
        self.policy = self.agent / "checkpoint.json"
        self.policy.write_bytes(json.dumps(CONFIG).encode())

    def tearDown(self):
        self.temp.cleanup()


class PolicyDescriptors(PolicyRoot):
    """Both readers close every descriptor they open, on every path (#1219).

    The descriptors are asked of the operating system once the reader is done, so the check
    holds however the reader closes them. Nothing else opens a file in between, so a number
    that still answers is a descriptor the reader left open.
    """

    def read(self, reader, fail_at=None):
        """Run reader over the root; return what it returned or raised, and what it opened.

        With fail_at set, the open with that index (0 is the first) fails instead.
        """
        opened, real_open = [], os.open

        def counted_open(path, flags, *args, **kwargs):
            if len(opened) == fail_at:
                raise PermissionError(errno.EACCES, "planted failure", os.fspath(path))
            opened.append(real_open(path, flags, *args, **kwargs))
            return opened[-1]

        with mock.patch.object(checkpoint.os, "open", counted_open):
            try:
                result = reader(self.root)
            except (OSError, checkpoint.CheckpointError) as error:
                result = error
        left_open = [number for number in sorted(set(opened)) if self.answers(number)]
        self.assertEqual(left_open, [], f"descriptors left open after {opened}")
        return result, opened

    @staticmethod
    def answers(descriptor):
        """Whether descriptor is open; one that is gets closed, so the test leaks nothing."""
        try:
            os.fstat(descriptor)
        except OSError:
            return False
        os.close(descriptor)
        return True

    @unittest.skipUnless(
        checkpoint.DESCRIPTOR_RELATIVE,
        "this platform has no descriptor-relative opens; its reader is the checked one",
    )
    def test_positive_relative_reader_closes_each_component_and_the_policy(self):
        result, opened = self.read(checkpoint._policy_bytes_relative)
        self.assertEqual(result, json.dumps(CONFIG).encode())
        self.assertEqual(len(opened), len(checkpoint.POLICY_PARTS) + 2)

    @unittest.skipUnless(
        checkpoint.DESCRIPTOR_RELATIVE,
        "this platform has no descriptor-relative opens; its reader is the checked one",
    )
    def test_negative_a_failing_later_open_closes_every_earlier_descriptor(self):
        for fail_at in range(1, len(checkpoint.POLICY_PARTS) + 2):
            with self.subTest(fail_at=fail_at):
                result, opened = self.read(checkpoint._policy_bytes_relative, fail_at=fail_at)
                self.assertIsInstance(result, PermissionError)
                self.assertEqual(len(opened), fail_at)

    def test_boundary_a_policy_that_cannot_be_wrapped_or_found_leaves_nothing_open(self):
        readers = [checkpoint._policy_bytes_checked]
        if checkpoint.DESCRIPTOR_RELATIVE:
            readers.append(checkpoint._policy_bytes_relative)
        self.policy.unlink()
        for reader in readers:
            with self.subTest(reader=reader.__name__, policy="absent"):
                self.assertIsNone(self.read(reader)[0])
        # A directory opens read-only on POSIX and cannot be wrapped in a stream; Windows
        # refuses to open it. Either way the reader raises and holds no descriptor.
        self.policy.mkdir()
        for reader in readers:
            with self.subTest(reader=reader.__name__, policy="directory"):
                self.assertIsInstance(self.read(reader)[0], OSError)


class CheckedPolicyRead(PolicyRoot):
    """The reader used where descriptor-relative opens are unavailable, exercised on every host.

    Windows selects it automatically; calling it directly keeps its refusals covered on the
    POSIX legs as well, where the descriptor-relative reader is the one in use.
    """

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


# The top-level modules the minimal Python of the devcontainer (python3-minimal 3.14.4, image of
# 2026-10-17) imports, measured inside that image. The checkpoint bundle runs there on adoption,
# so a stdlib module it lacks (concurrent.futures and queue are two) fails every adopter's hook.
MINIMAL_PYTHON_MODULES = frozenset(
    "argparse ast contextlib errno hashlib json math os pathlib re selectors shutil signal stat"
    " subprocess sys tempfile threading time typing urllib".split()
)


class MinimalPythonBundle(unittest.TestCase):
    """The checkpoint bundle imports only modules python3-minimal provides, and runs its checks
    in parallel without concurrent.futures."""

    def bundle_imports(self, name):
        import ast

        tree = ast.parse((Path(__file__).parent / name).read_text())
        found = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                found |= {alias.name.split(".")[0] for alias in node.names}
            elif isinstance(node, ast.ImportFrom) and node.level == 0 and node.module:
                found.add(node.module.split(".")[0])
        return found - {"checkpoint", "common"}

    def test_bundle_imports_only_minimal_python_modules(self):
        for name in ("checkpoint.py", "common.py"):
            with self.subTest(name=name):
                extra = self.bundle_imports(name) - MINIMAL_PYTHON_MODULES
                self.assertFalse(extra, f"{name} imports modules python3-minimal lacks: {sorted(extra)}")

    def test_ordered_map_keeps_order_and_bounds_threads(self):
        seen = []
        lock = __import__("threading").Lock()

        def double(value):
            with lock:
                seen.append(__import__("threading").current_thread().name)
            return value * 2

        items = list(range(25))
        self.assertEqual(checkpoint._ordered_map(double, items), [value * 2 for value in items])
        self.assertLessEqual(len(set(seen)), checkpoint.WORKTREE_CHECK_WORKERS)
        self.assertEqual(checkpoint._ordered_map(double, []), [])

    def test_ordered_map_raises_the_first_failure(self):
        def fail_on_three(value):
            if value == 3:
                raise ValueError("three")
            return value

        with self.assertRaisesRegex(ValueError, "three"):
            checkpoint._ordered_map(fail_on_three, list(range(6)))


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
