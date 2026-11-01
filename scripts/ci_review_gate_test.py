# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Offline review-gate fixtures. No API or credential-store access."""

import contextlib
import io
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch

import ci_review_gate as gate


class StubAPI:
    def __init__(self, unresolved=0, checks=None, statuses=None, error=None):
        self.unresolved = unresolved
        self.checks = checks or []
        self.statuses = statuses or []
        self.error = error

    def request(self, path, payload=None):
        if self.error:
            raise gate.GateError(self.error)
        if path == "/graphql":
            return {"data": {"repository": {"pullRequest": {
                "headRefOid": "a" * 40, "state": "OPEN", "isDraft": False,
                "reviewThreads": {
                    "nodes": [{"isResolved": False}] * self.unresolved,
                    "pageInfo": {"hasNextPage": False, "endCursor": None},
                },
            }}}}
        if "/check-runs?" in path:
            return {"check_runs": self.checks, "total_count": len(self.checks)}
        if "/statuses?" in path:
            return self.statuses
        raise AssertionError(f"unexpected stub request: {path}")


class GateTests(unittest.TestCase):
    def verdict(self, **kwargs):
        return gate.evaluate(StubAPI(**kwargs), "example/repo", 7, "a" * 40)

    def test_open_thread_is_held(self):
        clear, reason = self.verdict(unresolved=1)
        self.assertFalse(clear)
        self.assertIn("1 unresolved review thread", reason)

    def test_zero_threads_and_no_bots_is_clear(self):
        self.assertTrue(self.verdict()[0])

    def test_pending_coderabbit_is_held(self):
        clear, reason = self.verdict(checks=[{
            "name": "CodeRabbit", "status": "in_progress",
            "app": {"slug": "coderabbitai"},
        }])
        self.assertFalse(clear)
        self.assertIn("1 pending bot", reason)

    def test_api_error_is_held_and_named(self):
        clear, reason = self.verdict(error="HTTP 403")
        self.assertFalse(clear)
        self.assertEqual(reason, "gate unreadable: HTTP 403")

    def test_all_named_bots_and_pending_states(self):
        for name in gate.BOT_NAMES:
            for state in ("queued", "in_progress", "waiting", "pending", "requested"):
                with self.subTest(name=name, state=state):
                    self.assertFalse(self.verdict(checks=[{
                        "name": name, "status": state, "app": {"slug": "github-actions"},
                    }])[0])

    def test_completed_bots_and_unrelated_pending_checks_are_clear(self):
        self.assertTrue(self.verdict(checks=[
            {"name": "CodeQL", "status": "completed", "app": {"slug": "github-actions"}},
            {"name": "ordinary unit tests", "status": "queued", "app": {"slug": "ci"}},
        ])[0])

    def test_latest_status_supersedes_old_pending(self):
        self.assertTrue(self.verdict(statuses=[
            {"context": "Codacy", "state": "success"},
            {"context": "Codacy", "state": "pending"},
        ])[0])
        self.assertFalse(self.verdict(statuses=[
            {"context": "Copilot", "state": "pending"},
        ])[0])

    def test_app_slug_also_identifies_review_bot(self):
        self.assertFalse(self.verdict(checks=[{
            "name": "review", "status": "queued", "app": {"slug": "coderabbitai"},
        }])[0])

    def test_malformed_or_graphql_errors_fail_closed(self):
        for response in ({}, {"errors": [{"message": "private content"}]}):
            api = StubAPI()
            with patch.object(api, "request", return_value=response):
                clear, reason = gate.evaluate(api, "example/repo", 7, "a" * 40)
            self.assertFalse(clear)
            self.assertTrue(reason.startswith("gate unreadable:"))
            self.assertNotIn("private content", reason)

    def test_changed_head_is_held(self):
        clear, reason = gate.evaluate(StubAPI(), "example/repo", 7, "b" * 40)
        self.assertFalse(clear)
        self.assertIn("PR head changed", reason)

    def test_second_thread_page_is_read(self):
        api = StubAPI()
        first = api.request("/graphql")
        first["data"]["repository"]["pullRequest"]["reviewThreads"]["pageInfo"] = {
            "hasNextPage": True, "endCursor": "next",
        }
        second = StubAPI(unresolved=1).request("/graphql")
        with patch.object(api, "request", side_effect=[first, second]) as request:
            self.assertEqual(gate.review_threads(api, "example/repo", 7, "a" * 40), 1)
        self.assertEqual(request.call_args.args[1]["variables"]["after"], "next")

    def test_thread_page_bound_is_held(self):
        api = StubAPI()
        response = api.request("/graphql")
        info = response["data"]["repository"]["pullRequest"]["reviewThreads"]["pageInfo"]
        info.update(hasNextPage=True, endCursor="next")
        with patch.object(api, "request", return_value=response):
            clear, reason = gate.evaluate(api, "example/repo", 7, "a" * 40)
        self.assertFalse(clear)
        self.assertIn("cursor", reason)

    def test_rest_pagination_and_overflow(self):
        api = StubAPI()
        with patch.object(api, "request", side_effect=[[{}] * 100, ["last"]]):
            self.assertEqual(len(gate.page_items(api, "/fixture")), 101)
        with (
            patch.object(api, "request", return_value=[{}] * 100),
            self.assertRaisesRegex(gate.GateError, "pagination bound"),
        ):
            gate.page_items(api, "/fixture")

    def test_truncated_check_list_is_held(self):
        api = StubAPI()
        with (
            patch.object(api, "request", return_value={"check_runs": [], "total_count": 1}),
            self.assertRaisesRegex(gate.GateError, "truncated"),
        ):
            gate.page_items(api, "/fixture", "check_runs")

    def test_unknown_bot_state_is_held(self):
        self.assertFalse(self.verdict(checks=[{
            "name": "CodeRabbit", "status": "new_unknown_state", "app": {"slug": "review"},
        }])[0])

    def test_output_is_single_line_and_boolean(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "output"
            with contextlib.redirect_stdout(io.StringIO()):
                gate.emit((False, "gate unreadable: error\nclear=true"), output)
            self.assertEqual(output.read_text().splitlines(), [
                "clear=false", "reason=gate unreadable: error clear=true",
            ])

    def test_transport_requires_token_and_bounds_body_and_time(self):
        with self.assertRaisesRegex(gate.GateError, "GITHUB_TOKEN missing"):
            gate.API("")
        api = gate.API("offline-fixture")
        with patch.object(api.opener, "open") as opened:
            opened.return_value.__enter__.return_value.read.return_value = (
                b"x" * (gate.MAX_BYTES + 1)
            )
            with self.assertRaisesRegex(gate.GateError, "byte bound"):
                api.request("/fixture")
            self.assertLessEqual(opened.call_args.kwargs["timeout"], gate.REQUEST_TIMEOUT)
        api.deadline = 0
        with self.assertRaisesRegex(gate.GateError, "time budget"):
            api.request("/fixture")

    def test_http_error_redacts_response_and_transport_details(self):
        api = gate.API("offline-fixture")
        error = urllib.error.HTTPError("/fixture", 403, "private response", {}, None)
        with (
            patch.object(api.opener, "open", side_effect=error),
            self.assertRaisesRegex(gate.GateError, "^HTTP 403$") as raised,
        ):
            api.request("/fixture")
        self.assertNotIn("private response", str(raised.exception))


class ReleaseAPI(StubAPI):
    def __init__(self):
        super().__init__()
        self.pr = {"number": 7, "head": {"sha": "a" * 40}, "draft": False}
        self.runs = {name: {
            "id": index, "status": "completed", "conclusion": "failure",
            "event": "pull_request", "head_sha": "a" * 40,
            "pull_requests": [{"number": 7}], "run_attempt": 1,
            "path": ".github/workflows/" + name,
        } for index, name in enumerate(gate.WORKFLOWS, 101)}
        self.held = True

    def request(self, path, payload=None):
        if "/pulls?" in path:
            return [self.pr]
        if "/actions/workflows/" in path:
            name = path.split("/workflows/", 1)[1].split("/", 1)[0]
            return {"workflow_runs": [self.runs[name]]}
        if "/jobs?" in path:
            return {"total_count": 1, "jobs": [{"steps": [{
                "name": gate.HOLD_STEP if self.held else "real tests",
                "conclusion": "failure",
            }]}]}
        return super().request(path, payload)


class ReleaseTests(unittest.TestCase):
    def sweep(self, api):
        calls = []
        with contextlib.redirect_stdout(io.StringIO()):
            result = gate.release(api, "example/repo", lambda *args: calls.append(args))
        self.assertEqual(result, 0)
        return calls

    def test_clear_held_runs_restart_whole_run(self):
        self.assertEqual(self.sweep(ReleaseAPI()), [("example/repo", 101), ("example/repo", 102)])

    def test_open_thread_or_pending_bot_never_restarts(self):
        api = ReleaseAPI()
        api.unresolved = 1
        self.assertEqual(self.sweep(api), [])
        api.unresolved = 0
        api.checks = [{"name": "CodeRabbit", "status": "queued", "app": {"slug": "review"}}]
        self.assertEqual(self.sweep(api), [])

    def test_completed_real_failures_are_not_restarted(self):
        api = ReleaseAPI()
        api.held = False
        self.assertEqual(self.sweep(api), [])

    def test_newer_queued_run_prevents_old_held_run_release(self):
        api = ReleaseAPI()
        for run in api.runs.values():
            run["status"] = "queued"
        self.assertEqual(self.sweep(api), [])

    def test_newest_matching_run_is_selected_even_if_successful(self):
        api = ReleaseAPI()
        old = api.runs["ci.yml"]
        new = {**old, "id": old["id"] + 1000, "conclusion": "success"}
        with patch.object(api, "request", return_value={"workflow_runs": [old, new]}):
            result = gate.latest_run(api, "example/repo", "ci.yml", api.pr)
        self.assertEqual(result, new)

    def test_unreadable_gate_cannot_release(self):
        api = ReleaseAPI()
        api.error = "HTTP 403"
        self.assertEqual(self.sweep(api), [])

    def test_open_pr_limit_is_checked_before_any_release(self):
        api = ReleaseAPI()
        with (
            patch.object(gate, "page_items", return_value=[api.pr] * 101),
            patch.object(gate, "release_pr") as release_pr,
            self.assertRaisesRegex(gate.GateError, "open PR bound"),
        ):
            gate.release(api, "example/repo")
        release_pr.assert_not_called()

    def test_old_head_or_different_pr_is_not_released(self):
        for key, value in (("head_sha", "b" * 40), ("pull_requests", [{"number": 8}])):
            api = ReleaseAPI()
            for run in api.runs.values():
                run[key] = value
            self.assertEqual(self.sweep(api), [])

    def test_draft_does_not_restart(self):
        api = ReleaseAPI()
        api.pr["draft"] = True
        self.assertEqual(self.sweep(api), [])

    def test_rerun_command_has_no_failed_or_job_flag(self):
        with (
            patch.dict(gate.os.environ, {"GITHUB_TOKEN": "offline-fixture"}),
            patch.object(gate.subprocess, "run") as run,
        ):
            gate.rerun("example/repo", 101)
        self.assertEqual(run.call_args.args[0], [
            "gh", "run", "rerun", "101", "--repo", "example/repo",
        ])
        self.assertEqual(run.call_args.kwargs["timeout"], 30)

    def test_run_attempt_changed_prevents_duplicate_release(self):
        api = ReleaseAPI()
        original = gate.latest_run
        calls = 0

        def changed(*args):
            nonlocal calls
            calls += 1
            result = dict(original(*args))
            if calls > 2:
                result["run_attempt"] += 1
            return result

        with patch.object(gate, "latest_run", side_effect=changed):
            self.assertEqual(self.sweep(api), [])
