# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Offline review-gate fixtures. No API or credential-store access."""

import ast
import contextlib
import io
import os
import secrets
import shutil
import subprocess
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest.mock import patch

import ci_review_gate as gate


def workflow_step(name, workflow="portability.yml"):
    """Extract the shipped literal bash block for offline command replay."""
    path = Path(__file__).resolve().parents[1] / ".github/workflows" / workflow
    text = path.read_text(encoding="utf-8").split(
        "      - name: " + name + "\n", 1
    )[1]
    text = text.split("\n      - name:", 1)[0]
    if "        run: |\n" not in text:
        return text.split("        run: ", 1)[1].splitlines()[0]
    body = text.split("        run: |\n", 1)[1].split("        env:", 1)[0]
    return "\n".join(
        line[10:]
        for line in body.splitlines()
        if line.startswith("          ")
    )


class StubAPI:
    def __init__(self, unresolved=0, checks=None, statuses=None, error=None):
        """Initialize deterministic offline review and release fixtures."""
        self.unresolved = unresolved
        self.checks = checks or []
        self.statuses = statuses or []
        self.error = error

    def request(self, path, payload=None):
        """Return injected responses and reject requests outside the offline
        API surface.
        """
        if self.error:
            raise gate.GateError(self.error)
        if path == "/graphql":
            return {
                "data": {
                    "repository": {
                        "pullRequest": {
                            "headRefOid": "a" * 40,
                            "state": "OPEN",
                            "isDraft": False,
                            "reviewThreads": {
                                "nodes": [{"isResolved": False}]
                                * self.unresolved,
                                "pageInfo": {
                                    "hasNextPage": False,
                                    "endCursor": None,
                                },
                            },
                        }
                    }
                }
            }
        if "/check-runs?" in path:
            return {"check_runs": self.checks, "total_count": len(self.checks)}
        if "/statuses?" in path:
            return self.statuses
        raise AssertionError(f"unexpected stub request: {path}")


class GateTests(unittest.TestCase):
    def verdict(self, **kwargs):
        """Evaluate one offline PR head with injected review and bot state."""
        return gate.evaluate(StubAPI(**kwargs), "example/repo", 7, "a" * 40)

    def test_open_thread_is_held(self):
        """Verify open thread is held."""
        clear, reason = self.verdict(unresolved=1)
        self.assertFalse(clear)
        self.assertIn("1 unresolved review thread", reason)

    def test_zero_threads_and_no_bots_is_clear(self):
        """Verify zero threads and no bots is clear."""
        self.assertTrue(self.verdict()[0])

    def test_pending_coderabbit_is_held(self):
        """Verify pending coderabbit is held."""
        clear, reason = self.verdict(
            checks=[
                {
                    "name": "CodeRabbit",
                    "status": "in_progress",
                    "app": {"slug": "coderabbitai"},
                }
            ]
        )
        self.assertFalse(clear)
        self.assertIn("1 pending bot", reason)

    def test_api_error_is_held_and_named(self):
        """Verify api error is held and named."""
        clear, reason = self.verdict(error="HTTP 403")
        self.assertFalse(clear)
        self.assertEqual(reason, "gate unreadable: HTTP 403")

    def test_all_named_bots_and_pending_states(self):
        """Verify all named bots and pending states."""
        for name in ("coderabbit", "codacy", "codeql", "copilot"):
            for state in (
                "queued",
                "in_progress",
                "waiting",
                "pending",
                "requested",
            ):
                with self.subTest(name=name, state=state):
                    self.assertFalse(
                        self.verdict(
                            checks=[
                                {
                                    "name": name,
                                    "status": state,
                                    "app": {"slug": "github-actions"},
                                }
                            ]
                        )[0]
                    )

    def test_completed_bots_and_unrelated_pending_checks_are_clear(self):
        """Verify completed bots and unrelated pending checks are clear."""
        self.assertTrue(
            self.verdict(
                checks=[
                    {
                        "name": "CodeQL",
                        "status": "completed",
                        "app": {"slug": "github-actions"},
                    },
                    {
                        "name": "ordinary unit tests",
                        "status": "queued",
                        "app": {"slug": "ci"},
                    },
                ]
            )[0]
        )

    def test_latest_status_supersedes_old_pending(self):
        """Verify latest status supersedes old pending."""
        self.assertTrue(
            self.verdict(
                statuses=[
                    {"context": "Codacy", "state": "success"},
                    {"context": "Codacy", "state": "pending"},
                ]
            )[0]
        )
        self.assertFalse(
            self.verdict(
                statuses=[
                    {"context": "Copilot", "state": "pending"},
                ]
            )[0]
        )

    def test_app_slug_also_identifies_review_bot(self):
        """Verify app slug also identifies review bot."""
        self.assertFalse(
            self.verdict(
                checks=[
                    {
                        "name": "review",
                        "status": "queued",
                        "app": {"slug": "coderabbitai"},
                    }
                ]
            )[0]
        )

    def test_malformed_or_graphql_errors_fail_closed(self):
        """Verify malformed or graphql errors fail closed."""
        for response in ({}, {"errors": [{"message": "private content"}]}):
            api = StubAPI()
            with patch.object(api, "request", return_value=response):
                clear, reason = gate.evaluate(api, "example/repo", 7, "a" * 40)
            self.assertFalse(clear)
            self.assertTrue(reason.startswith("gate unreadable:"))
            self.assertNotIn("private content", reason)

    def test_changed_head_is_held(self):
        """Verify changed head is held."""
        clear, reason = gate.evaluate(StubAPI(), "example/repo", 7, "b" * 40)
        self.assertFalse(clear)
        self.assertIn("PR head changed", reason)

    def test_second_thread_page_is_read(self):
        """Verify second thread page is read."""
        api = StubAPI()
        first = api.request("/graphql")
        first["data"]["repository"]["pullRequest"]["reviewThreads"][
            "pageInfo"
        ] = {
            "hasNextPage": True,
            "endCursor": "next",
        }
        second = StubAPI(unresolved=1).request("/graphql")
        with patch.object(
            api, "request", side_effect=[first, second]
        ) as request:
            self.assertEqual(
                gate.review_threads(api, "example/repo", 7, "a" * 40), 1
            )
        self.assertEqual(
            request.call_args.args[1]["variables"]["after"], "next"
        )

    def test_thread_page_bound_is_held(self):
        """Verify thread page bound is held."""
        api = StubAPI()
        response = api.request("/graphql")
        info = response["data"]["repository"]["pullRequest"]["reviewThreads"][
            "pageInfo"
        ]
        info.update(hasNextPage=True, endCursor="next")
        with patch.object(api, "request", return_value=response):
            clear, reason = gate.evaluate(api, "example/repo", 7, "a" * 40)
        self.assertFalse(clear)
        self.assertIn("cursor", reason)

    def test_rest_pagination_and_overflow(self):
        """Verify rest pagination and overflow."""
        api = StubAPI()
        with patch.object(api, "request", side_effect=[[{}] * 100, ["last"]]):
            self.assertEqual(len(gate.page_items(api, "/fixture")), 101)
        with (
            patch.object(api, "request", return_value=[{}] * 100),
            self.assertRaisesRegex(gate.GateError, "pagination bound"),
        ):
            gate.page_items(api, "/fixture")

    def test_truncated_check_list_is_held(self):
        """Verify truncated check list is held."""
        api = StubAPI()
        with (
            patch.object(
                api,
                "request",
                return_value={"check_runs": [], "total_count": 1},
            ),
            self.assertRaisesRegex(gate.GateError, "truncated"),
        ):
            gate.page_items(api, "/fixture", "check_runs")

    def test_unknown_bot_state_is_held(self):
        """Verify unknown bot state is held."""
        self.assertFalse(
            self.verdict(
                checks=[
                    {
                        "name": "CodeRabbit",
                        "status": "new_unknown_state",
                        "app": {"slug": "review"},
                    }
                ]
            )[0]
        )

    def test_output_is_single_line_and_boolean(self):
        """Verify output is single line and boolean."""
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / "output"
            with contextlib.redirect_stdout(io.StringIO()):
                gate.emit(
                    (False, "gate unreadable: error\nclear=true"), output
                )
            self.assertEqual(
                output.read_text().splitlines(),
                [
                    "clear=false",
                    "reason=gate unreadable: error clear=true",
                ],
            )

    def test_transport_requires_token_and_bounds_body_and_time(self):
        """Verify transport requires token and bounds body and time."""
        with self.assertRaisesRegex(gate.GateError, "GITHUB_TOKEN missing"):
            gate.API("")
        api = gate.API(secrets.token_hex(16))
        with patch.object(api.opener, "open") as opened:
            opened.return_value.__enter__.return_value.read.return_value = (
                b"x" * (gate.MAX_BYTES + 1)
            )
            with self.assertRaisesRegex(gate.GateError, "byte bound"):
                api.request("/fixture")
            self.assertLessEqual(
                opened.call_args.kwargs["timeout"], gate.REQUEST_TIMEOUT
            )
        api.deadline = 0
        with self.assertRaisesRegex(gate.GateError, "time budget"):
            api.request("/fixture")

    def test_http_error_redacts_response_and_transport_details(self):
        """Verify http error redacts response and transport details."""
        api = gate.API(secrets.token_hex(16))
        error = urllib.error.HTTPError(
            "/fixture", 403, "private response", {}, None
        )
        with (
            patch.object(api.opener, "open", side_effect=error),
            self.assertRaisesRegex(gate.GateError, "^HTTP 403$") as raised,
        ):
            api.request("/fixture")
        self.assertNotIn("private response", str(raised.exception))


class ReleaseAPI(StubAPI):
    def __init__(self):
        """Initialize deterministic offline review and release fixtures."""
        super().__init__()
        self.pr = {
            "number": 7,
            "head": {
                "sha": "a" * 40,
                "repo": {"full_name": "fork/repo"},
            },
            "draft": False,
            "state": "open",
        }
        self.runs = {
            name: {
                "id": index,
                "status": "completed",
                "conclusion": "failure",
                "event": "pull_request",
                "head_sha": "a" * 40,
                "pull_requests": [{"number": 7}],
                "run_attempt": 1,
                "path": ".github/workflows/" + name,
            }
            for index, name in enumerate(gate.WORKFLOWS, 101)
        }
        self.held = True
        self.mutations = []

    def request(self, path, payload=None):
        """Return injected responses and reject requests outside the offline
        API surface.
        """
        if path.endswith("/rerun"):
            self.mutations.append((path, payload))
            return None
        if path == "/repos/example/repo/pulls/7":
            return self.pr
        if "/pulls?" in path:
            return [self.pr]
        if "/actions/workflows/" in path:
            name = path.split("/workflows/", 1)[1].split("/", 1)[0]
            return {"workflow_runs": [self.runs[name]]}
        if "/jobs?" in path:
            return {
                "total_count": 1,
                "jobs": [
                    {
                        "steps": [
                            {
                                "name": (
                                    gate.HOLD_STEP
                                    if self.held
                                    else "real tests"
                                ),
                                "conclusion": "failure",
                            }
                        ]
                    }
                ],
            }
        return super().request(path, payload)


class ReleaseTests(unittest.TestCase):
    def test_default_release_restarts_through_injected_api(self):
        """Exercise the production mutation path without a subprocess or
        live API.
        """
        api = ReleaseAPI()
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(gate.release(api, "example/repo"), 0)
        self.assertEqual(
            api.mutations,
            [
                ("/repos/example/repo/actions/runs/101/rerun", {}),
                ("/repos/example/repo/actions/runs/102/rerun", {}),
            ],
        )

    def sweep(self, api):
        """Capture release mutations while requiring a successful offline
        sweep.
        """
        calls = []
        with contextlib.redirect_stdout(io.StringIO()):
            result = gate.release(
                api, "example/repo", lambda api, *args: calls.append(args)
            )
        self.assertEqual(result, 0)
        return calls

    def test_clear_held_runs_restart_whole_run(self):
        """Verify clear held runs restart whole run."""
        self.assertEqual(
            self.sweep(ReleaseAPI()),
            [("example/repo", 101), ("example/repo", 102)],
        )

    def test_open_thread_or_pending_bot_never_restarts(self):
        """Verify open thread or pending bot never restarts."""
        api = ReleaseAPI()
        api.unresolved = 1
        self.assertEqual(self.sweep(api), [])
        api.unresolved = 0
        api.checks = [
            {
                "name": "CodeRabbit",
                "status": "queued",
                "app": {"slug": "review"},
            }
        ]
        self.assertEqual(self.sweep(api), [])

    def test_completed_real_failures_are_not_restarted(self):
        """Verify completed real failures are not restarted."""
        api = ReleaseAPI()
        api.held = False
        self.assertEqual(self.sweep(api), [])

    def test_newer_queued_run_prevents_old_held_run_release(self):
        """Verify newer queued run prevents old held run release."""
        api = ReleaseAPI()
        for run in api.runs.values():
            run["status"] = "queued"
        self.assertEqual(self.sweep(api), [])

    def test_newest_matching_run_is_selected_even_if_successful(self):
        """Verify newest matching run is selected even if successful."""
        api = ReleaseAPI()
        old = api.runs["ci.yml"]
        new = {**old, "id": old["id"] + 1000, "conclusion": "success"}
        with patch.object(
            api, "request", return_value={"workflow_runs": [old, new]}
        ):
            result = gate.latest_run(api, "example/repo", "ci.yml", api.pr)
        self.assertEqual(result, new)

    def test_unreadable_gate_cannot_release(self):
        """Verify unreadable gate cannot release."""
        api = ReleaseAPI()
        api.error = "HTTP 403"
        self.assertEqual(self.sweep(api), [])

    def test_open_pr_limit_processes_first_page_and_states_truncation(self):
        """Verify open pr limit processes first page and states truncation."""
        api = ReleaseAPI()
        with (
            patch.object(
                api, "request", return_value=[api.pr] * gate.MAX_PRS
            ) as request,
            patch.object(gate, "release_pr") as release_pr,
            contextlib.redirect_stdout(io.StringIO()) as output,
        ):
            self.assertEqual(gate.release(api, "example/repo"), 0)
        self.assertEqual(release_pr.call_count, gate.MAX_PRS)
        request.assert_called_once_with(
            "/repos/example/repo/pulls?state=open&per_page=100&page=1"
        )
        self.assertIn("truncated", output.getvalue())

    def test_old_head_or_different_pr_is_not_released(self):
        """Verify old head or different pr is not released."""
        for key, value in (
            ("head_sha", "b" * 40),
            ("pull_requests", [{"number": 8}]),
        ):
            api = ReleaseAPI()
            for run in api.runs.values():
                run[key] = value
            self.assertEqual(self.sweep(api), [])

    def test_draft_does_not_restart(self):
        """Verify draft does not restart."""
        api = ReleaseAPI()
        api.pr["draft"] = True
        self.assertEqual(self.sweep(api), [])

    def test_rerun_posts_whole_run_endpoint(self):
        """Verify rerun posts whole run endpoint."""
        api = StubAPI()
        with patch.object(api, "request", return_value=None) as request:
            gate.rerun(api, "example/repo", 101)
        request.assert_called_once_with(
            "/repos/example/repo/actions/runs/101/rerun", {}
        )

    def test_malformed_rerun_never_calls_api(self):
        """Verify malformed rerun never calls api."""
        api = StubAPI()
        for repository, run_id in (
            ("../repo", 1),
            ("owner/repo/extra", 1),
            ("owner/repo?x=y", 1),
            (None, 1),
            ("owner/repo", True),
            ("owner/repo", 0),
            ("owner/repo", -1),
            ("owner/repo", "1"),
            ("owner/repo", 1.5),
        ):
            with (
                self.subTest(repository=repository, run_id=run_id),
                patch.object(api, "request") as request,
            ):
                with self.assertRaisesRegex(gate.GateError, "invalid"):
                    gate.rerun(api, repository, run_id)
                request.assert_not_called()

    def test_run_attempt_changed_prevents_duplicate_release(self):
        """Verify run attempt changed prevents duplicate release."""
        api = ReleaseAPI()
        original = gate.latest_run
        calls = 0

        def changed(*args):
            """Return a newer run attempt after the initial discovery reads."""
            nonlocal calls
            calls += 1
            result = dict(original(*args))
            if calls > 2:
                result["run_attempt"] += 1
            return result

        with patch.object(gate, "latest_run", side_effect=changed):
            self.assertEqual(self.sweep(api), [])

    def test_workflow_path_ref_suffix_is_removed(self):
        """Verify workflow path ref suffix is removed."""
        api = ReleaseAPI()
        for run in api.runs.values():
            run["path"] += "@refs/heads/main"
        self.assertEqual(len(self.sweep(api)), 2)

    def test_fork_empty_associations_match_repository_and_sha(self):
        """Verify fork empty associations match repository and sha."""
        api = ReleaseAPI()
        for run in api.runs.values():
            run.update(
                pull_requests=[], head_repository={"full_name": "fork/repo"}
            )
        self.assertEqual(len(self.sweep(api)), 2)
        for run in api.runs.values():
            run["head_repository"]["full_name"] = "other/repo"
        self.assertEqual(self.sweep(api), [])

    def test_empty_associations_missing_identity_fail_closed(self):
        """Verify empty associations missing identity fail closed."""
        api = ReleaseAPI()
        run = {**api.runs["ci.yml"], "pull_requests": []}
        for data in (
            run,
            {**run, "head_repository": {}},
            {**run, "head_repository": {"full_name": ""}},
        ):
            with self.assertRaises((gate.GateError, KeyError)):
                gate.run_matches(data, api.pr)

    def test_refresh_pr_head_immediately_before_rerun(self):
        """Verify refresh pr head immediately before rerun."""
        api = ReleaseAPI()
        original = api.request

        def changed(path, payload=None):
            """Move the REST head after the review read, before any
            mutation.
            """
            if path == "/repos/example/repo/pulls/7":
                return {**api.pr, "head": {**api.pr["head"], "sha": "b" * 40}}
            return original(path, payload)

        with patch.object(api, "request", side_effect=changed) as request:
            self.assertEqual(self.sweep(api), [])
        self.assertIn(
            "/repos/example/repo/pulls/7",
            [c.args[0] for c in request.call_args_list],
        )

    def test_new_latest_run_immediately_before_rerun_is_skipped(self):
        """Verify new latest run immediately before rerun is skipped."""
        api = ReleaseAPI()
        original = gate.latest_run
        calls = 0

        def moved(*args):
            """Publish a new run after the initial discovery pass."""
            nonlocal calls
            calls += 1
            result = dict(original(*args))
            if calls > 2:
                result["id"] += 1000
            return result

        with patch.object(gate, "latest_run", side_effect=moved):
            self.assertEqual(self.sweep(api), [])

    def test_retry_budget_is_stated_and_never_posts(self):
        """Verify retry budget is stated and never posts."""
        for attempt in (3, 4):
            api = ReleaseAPI()
            for run in api.runs.values():
                run["run_attempt"] = attempt
            with contextlib.redirect_stdout(io.StringIO()) as output:
                gate.release(
                    api,
                    "example/repo",
                    lambda *args: self.fail("retry budget"),
                )
            self.assertIn("retry budget", output.getvalue())

    def test_final_identity_reads_are_adjacent_to_each_mutation(self):
        """Prevent expensive review or jobs pagination after the final
        identity checks.
        """
        api = ReleaseAPI()
        original = api.request
        calls = []

        def recorded(path, payload=None):
            """Record only offline request paths in their execution order."""
            calls.append(path)
            return original(path, payload)

        with (
            patch.object(api, "request", side_effect=recorded),
            contextlib.redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(gate.release(api, "example/repo"), 0)
        for index, path in enumerate(calls):
            if path.endswith("/rerun"):
                self.assertEqual(
                    calls[index - 2], "/repos/example/repo/pulls/7"
                )
                self.assertIn("/actions/workflows/", calls[index - 1])

    def test_retry_budget_two_still_releases(self):
        """Allow the last budgeted retry before attempt three."""
        api = ReleaseAPI()
        for run in api.runs.values():
            run["run_attempt"] = 2
        self.assertEqual(len(self.sweep(api)), 2)


class TransportTests(unittest.TestCase):
    def response(self, api, body, status=200):
        """Inject a response without using the network or reading
        credentials.
        """
        opened = patch.object(api.opener, "open")
        mock = opened.start()
        self.addCleanup(opened.stop)
        response = mock.return_value.__enter__.return_value
        response.read.return_value = body
        response.status = status
        return mock

    def test_post_declares_json_content_type(self):
        """Verify post declares json content type."""
        api = gate.API(secrets.token_hex(16))
        opened = self.response(api, b"{}")
        api.request("/graphql", {"query": "fixture"})
        request = opened.call_args.args[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(
            request.get_header("Content-type"), "application/json"
        )

    def test_empty_created_and_no_content_are_accepted(self):
        """Verify empty created and no content are accepted."""
        for status in (201, 204):
            with self.subTest(status=status):
                api = gate.API(secrets.token_hex(16))
                self.response(api, b"", status)
                self.assertIsNone(api.request("/fixture", {}))

    def test_empty_ok_and_nonempty_invalid_json_are_rejected(self):
        """Verify empty ok and nonempty invalid json are rejected."""
        for status, body in ((200, b""), (201, b"invalid"), (204, b"invalid")):
            api = gate.API(secrets.token_hex(16))
            self.response(api, body, status)
            with self.assertRaisesRegex(gate.GateError, "not JSON"):
                api.request("/fixture", {})

    def test_boolean_total_count_is_rejected(self):
        """Verify boolean total count is rejected."""
        api = StubAPI()
        for total in (True, False, -1, "1"):
            with (
                patch.object(
                    api,
                    "request",
                    return_value={"items": [], "total_count": total},
                ),
                self.assertRaisesRegex(
                    gate.GateError, "invalid API total count"
                ),
            ):
                gate.page_items(api, "/fixture", "items")

    def test_integer_subclass_total_count_is_accepted(self):
        """Verify integer subclass total count is accepted."""

        class Count(int):
            """An integer count remains valid without exact type equality."""

        api = StubAPI()
        with patch.object(
            api, "request", return_value={"items": [], "total_count": Count(0)}
        ):
            self.assertEqual(gate.page_items(api, "/fixture", "items"), [])


class SourceContractTests(unittest.TestCase):
    def test_fixture_functions_are_documented_and_bounded(self):
        """Keep changed fixture functions documented and at most 60 lines."""
        root = Path(gate.__file__).resolve().parents[1]
        for path in (
            Path(__file__),
            root / ".config/lefthook/scripts/test_ci_scope.py",
        ):
            tree = ast.parse(path.read_text(encoding="utf-8"))
            for node in ast.walk(tree):
                if isinstance(node, ast.FunctionDef):
                    with self.subTest(path=path.name, function=node.name):
                        self.assertIsNotNone(ast.get_docstring(node))
                        self.assertLessEqual(
                            node.end_lineno - node.lineno + 1, 60
                        )

    def test_release_and_heavy_holds_declare_api_read_permissions(self):
        """Require review API read permissions in every caller's workflow."""
        root = Path(gate.__file__).resolve().parents[1]
        for name in ("ci.yml", "portability.yml", "release-heavy-tier.yml"):
            text = (root / ".github/workflows" / name).read_text(
                encoding="utf-8"
            )
            permissions = text.split("\njobs:", 1)[0]
            for scope in ("contents", "pull-requests", "checks", "statuses"):
                self.assertIn(f"  {scope}: read", permissions)

    def test_control_jobs_state_linux_reason_and_share_platform_selftest(self):
        """Make platform omissions explicit and exercise the helper on every
        matrix leg.
        """
        root = Path(gate.__file__).resolve().parents[1]
        for name in ("ci.yml", "portability.yml", "release-heavy-tier.yml"):
            text = (root / ".github/workflows" / name).read_text(
                encoding="utf-8"
            )
            self.assertIn("Linux-only: GitHub API control job", text)
        self.assertIn(
            '"$PYTHON" -B scripts/ci_review_gate.py selftest || status=$?',
            workflow_step("Harness Self-Tests"),
        )

    def test_fixture_api_tokens_are_generated(self):
        """Prevent static token-shaped fixtures from triggering secret
        scanners.
        """
        tree = ast.parse(Path(__file__).read_text(encoding="utf-8"))
        for node in ast.walk(tree):
            if (
                isinstance(node, ast.Call)
                and isinstance(node.func, ast.Attribute)
                and node.func.attr == "API"
                and node.args
            ):
                argument = node.args[0]
                self.assertFalse(
                    isinstance(argument, ast.Constant) and bool(argument.value)
                )

    def test_review_threads_complexity_bound(self):
        """Keep the pagination coordinator below the review's McCabe bound."""
        source = ast.parse(Path(gate.__file__).read_text(encoding="utf-8"))
        function = next(
            n
            for n in source.body
            if isinstance(n, ast.FunctionDef) and n.name == "review_threads"
        )
        decisions = sum(
            isinstance(
                n,
                (
                    ast.If,
                    ast.For,
                    ast.ExceptHandler,
                    ast.IfExp,
                    ast.comprehension,
                ),
            )
            for n in ast.walk(function)
        )
        decisions += sum(
            len(n.values) - 1
            for n in ast.walk(function)
            if isinstance(n, ast.BoolOp)
        )
        self.assertLessEqual(1 + decisions, 10)

    def test_helper_functions_have_docstrings_and_bounded_length(self):
        """Require reviewable helper functions with documented branch
        behavior.
        """
        source = ast.parse(Path(gate.__file__).read_text(encoding="utf-8"))
        for node in ast.walk(source):
            if isinstance(node, ast.FunctionDef):
                with self.subTest(function=node.name):
                    self.assertIsNotNone(ast.get_docstring(node))
                    self.assertLessEqual(node.end_lineno - node.lineno + 1, 60)

    def test_documented_release_and_draft_contract(self):
        """Keep operational limits and bootstrap behavior visible to
        adopters.
        """
        root = Path(gate.__file__).resolve().parents[1]
        text = (root / "docs/guides/workflow-triggers.md").read_text(
            encoding="utf-8"
        )
        for phrase in (
            "no trusted helper on the default branch",
            "retry budget",
            "Gate not run on a draft",
            "fresh review",
        ):
            self.assertIn(phrase, text)


def write_trusted_helper(root, clear):
    """Write an offline trusted helper that validates PR command inputs."""
    helper = root / ".review-gate/scripts/ci_review_gate.py"
    helper.parent.mkdir(parents=True)
    helper.write_text(
        "import os,sys\nfrom pathlib import Path\n"
        "assert sys.argv[1:] == ['gate','--pr','7','--head','"
        + "a" * 40
        + "']\n"
        "Path('called').touch()\n"
        "with Path(os.environ['GITHUB_OUTPUT']).open('a') as out:"
        "\n"
        f"    out.write('clear={str(clear).lower()}\\n"
        "reason=fresh review fixture\\n')\n",
        encoding="utf-8",
    )


@unittest.skipUnless(shutil.which("bash"), "workflow replay requires bash")
class WorkflowTests(unittest.TestCase):
    def execute(
        self,
        name,
        *,
        trusted=True,
        clear=False,
        planned=True,
        workflow="portability.yml",
    ):
        """Replay shipped shell with a local helper stub and no GitHub API
        calls.
        """
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            if trusted:
                write_trusted_helper(root, clear)
            result = subprocess.run(
                [
                    shutil.which("bash"),
                    "-e",
                    "-c",
                    workflow_step(name, workflow),
                ],
                cwd=root,
                timeout=30,
                capture_output=True,
                text=True,
                check=False,
                env=dict(
                    os.environ,
                    PR_NUMBER="7",
                    PR_HEAD="a" * 40,
                    PLANNED_CLEAR=str(planned).lower(),
                    GITHUB_OUTPUT=str(root / "output"),
                    GITHUB_STEP_SUMMARY=str(root / "summary"),
                ),
            )
            output = (
                (root / "output").read_text()
                if (root / "output").exists()
                else ""
            )
            summary = (
                (root / "summary").read_text()
                if (root / "summary").exists()
                else ""
            )
            return result, output, summary, (root / "called").exists()

    def test_bootstrap_planner_and_hold_use_exact_reason(self):
        """Verify bootstrap planner and hold use exact reason."""
        for workflow in ("ci.yml", "portability.yml"):
            result, output, _, called = self.execute(
                "Evaluate review clearance",
                trusted=False,
                workflow=workflow,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("clear=false", output)
            self.assertIn(
                "reason=gate unreadable: "
                "no trusted helper on the default branch",
                output,
            )
            self.assertFalse(called)
            result, _, summary, called = self.execute(
                gate.HOLD_STEP,
                trusted=False,
                workflow=workflow,
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn(
                "gate unreadable: no trusted helper on the default branch",
                summary,
            )
            self.assertFalse(called)

    def test_fresh_pending_review_overrides_earlier_clearance(self):
        """Verify fresh pending review overrides earlier clearance."""
        for workflow in ("ci.yml", "portability.yml"):
            result, output, summary, called = self.execute(
                gate.HOLD_STEP, workflow=workflow
            )
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertNotIn("clear=true", output)
            self.assertIn("heavy tier held: fresh review fixture", summary)
            self.assertTrue(called)

    def test_fresh_clearance_allows_work_only_on_planned_platform(self):
        """Verify fresh clearance allows work only on planned platform."""
        result, output, _, called = self.execute(gate.HOLD_STEP, clear=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(output, "clear=true\n")
        self.assertTrue(called)
        result, _, summary, called = self.execute(
            gate.HOLD_STEP, clear=True, planned=False
        )
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertIn("rerun required for platform selection", summary)
        self.assertTrue(called)

    def test_draft_annotation_remains_canonical(self):
        """Verify draft annotation remains canonical."""
        for workflow in ("ci.yml", "portability.yml"):
            path = (
                Path(gate.__file__).resolve().parents[1]
                / ".github/workflows"
                / workflow
            )
            text = path.read_text(encoding="utf-8")
            hold = text.split("      - name: " + gate.HOLD_STEP + "\n", 1)[1]
            hold = hold.split("\n      # The hosted gate", 1)[0]
            self.assertIn(
                "if: github.event_name == 'pull_request' && "
                "github.event.pull_request.draft != true",
                hold,
            )
            self.assertIn("TITLE: Gate not run on a draft", text)
            self.assertIn(
                "The gate did not run because the pull request is a draft.",
                text,
            )
            self.assertIn(
                "It runs when the pull request is marked ready for review.",
                text,
            )


if __name__ == "__main__":
    unittest.main()
