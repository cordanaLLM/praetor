#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Hold expensive PR work until review threads and bot checks are clear.

API contract: docs.github.com REST 2026-03-10, checked 2026-11-01.
Reads use GITHUB_TOKEN exclusively; selftest uses injected offline responses.
"""

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from urllib.parse import urlencode

BOT_NAMES = ("coderabbit", "codacy", "codeql", "copilot")
WORKFLOWS = ("portability.yml", "ci.yml")
HOLD_STEP = "Hold heavy tier for review"
MAX_BYTES = 2 * 1024 * 1024
MAX_PAGES = 10
PAGE_SIZE = 100
MAX_PRS = 100
MAX_RUNS = 100
REQUEST_TIMEOUT = 15
GATE_SECONDS = 120
RELEASE_SECONDS = 480
THREAD_QUERY = """query($owner:String!, $name:String!, $number:Int!, $after:String) {
  repository(owner:$owner, name:$name) {
    pullRequest(number:$number) {
      headRefOid state isDraft
      reviewThreads(first:100, after:$after) {
        nodes { isResolved }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}"""


class GateError(ValueError):
    """A safe diagnostic suitable for workflow outputs."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise GateError("API redirect refused")


class API:
    def __init__(self, token, seconds=GATE_SECONDS):
        if not token:
            raise GateError("GITHUB_TOKEN missing")
        self.token = token
        self.deadline = time.monotonic() + seconds
        self.opener = urllib.request.build_opener(NoRedirect)

    def request(self, path, payload=None):
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise GateError("API time budget exceeded")
        request = urllib.request.Request(
            "https://api.github.com" + path,
            data=json.dumps(payload).encode() if payload is not None else None,
            headers={
                "Authorization": "Bearer " + self.token,
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2026-03-10",
                "User-Agent": "ci-review-gate",
            },
        )
        try:
            with self.opener.open(
                request, timeout=min(REQUEST_TIMEOUT, remaining)
            ) as response:
                raw = response.read(MAX_BYTES + 1)
        except urllib.error.HTTPError as error:
            raise GateError(f"HTTP {error.code}") from None
        except (OSError, urllib.error.URLError):
            raise GateError("API connection failed or timed out") from None
        if len(raw) > MAX_BYTES:
            raise GateError("API response exceeds byte bound")
        try:
            return json.loads(raw)
        except (ValueError, UnicodeError):
            raise GateError("API response is not JSON") from None


def page_items(api, path, key=None, max_pages=MAX_PAGES):
    """Read bounded REST pages; never silently treat a partial read as complete."""
    result = []
    for page in range(1, max_pages + 1):
        separator = "&" if "?" in path else "?"
        data = api.request(
            f"{path}{separator}per_page={PAGE_SIZE}&page={page}"
        )
        items = data[key] if key else data
        if not isinstance(items, list) or len(items) > PAGE_SIZE:
            raise GateError("invalid API page")
        if key and (type(data["total_count"]) is not int or data["total_count"] < 0):
            raise GateError("invalid API total count")
        result.extend(items)
        if len(items) < PAGE_SIZE:
            if key and "total_count" in data and data["total_count"] != len(result):
                raise GateError("API total count changed or was truncated")
            return result
    raise GateError("API pagination bound exceeded")


def review_threads(api, repository, number, head):
    owner, name = repository.split("/")
    cursor, unresolved = None, 0
    for _ in range(MAX_PAGES):
        data = api.request("/graphql", {
            "query": THREAD_QUERY,
            "variables": {
                "owner": owner, "name": name, "number": number, "after": cursor,
            },
        })
        if data.get("errors"):
            raise GateError("GraphQL errors")
        pr = data["data"]["repository"]["pullRequest"]
        if pr["headRefOid"] != head:
            raise GateError("PR head changed")
        if type(pr["isDraft"]) is not bool:
            raise GateError("invalid PR draft state")
        if pr["state"] != "OPEN" or pr["isDraft"]:
            raise GateError("PR is closed or draft")
        threads = pr["reviewThreads"]
        nodes = threads["nodes"]
        if not isinstance(nodes, list) or len(nodes) > PAGE_SIZE:
            raise GateError("invalid review thread page")
        for node in nodes:
            if type(node["isResolved"]) is not bool:
                raise GateError("invalid review thread resolution")
            unresolved += not node["isResolved"]
        info = threads["pageInfo"]
        if type(info["hasNextPage"]) is not bool:
            raise GateError("invalid thread pagination")
        if not info["hasNextPage"]:
            return unresolved
        after = info["endCursor"]
        if not isinstance(after, str) or not after or after == cursor:
            raise GateError("invalid thread cursor")
        cursor = after
    raise GateError("review thread pagination bound exceeded")


def review_bot(text):
    normalized = re.sub(r"[^a-z0-9]", "", text.lower())
    return any(name in normalized for name in BOT_NAMES)


def pending_bots(api, repository, head):
    base = f"/repos/{repository}/commits/{head}"
    checks = page_items(api, base + "/check-runs?filter=latest", "check_runs")
    statuses = page_items(api, base + "/statuses")
    pending = 0
    for check in checks:
        name = check["name"] + " " + check["app"]["slug"]
        if review_bot(name):
            state = check["status"]
            if state not in {
                "queued", "in_progress", "completed", "waiting", "requested", "pending",
            }:
                raise GateError("unknown bot check state")
            pending += state != "completed"
    seen = set()
    # REST returns newest first: superseded pending statuses do not hold a PR.
    for status in statuses:
        context = status["context"]
        if context in seen:
            continue
        seen.add(context)
        if review_bot(context):
            state = status["state"]
            if state not in {"pending", "success", "failure", "error"}:
                raise GateError("unknown bot status state")
            pending += state == "pending"
    return pending


def evaluate(api, repository, number, head):
    """Return a fail-closed verdict; never emit API response bodies or tokens."""
    try:
        threads = review_threads(api, repository, number, head)
        bots = pending_bots(api, repository, head)
        if threads or bots:
            return False, f"{threads} unresolved review thread(s); {bots} pending bot check(s)"
        return True, "0 unresolved review threads; 0 pending bot checks"
    except ValueError as error:
        # Only GateError messages are safe to expose; JSON/schema errors may quote input.
        detail = str(error) if isinstance(error, GateError) else "invalid API response"
        return False, "gate unreadable: " + detail
    except (KeyError, TypeError, AttributeError):
        return False, "gate unreadable: incomplete API response"


def emit(verdict, output):
    clear, reason = verdict
    # Output is a single line, including when a stub or future diagnostic contains newlines.
    reason = " ".join(reason.split())[:500]
    text = f"clear={str(clear).lower()}\nreason={reason}\n"
    with Path(output).open("a", encoding="utf-8", newline="\n") as stream:
        stream.write(text)
    print(text, end="")


def latest_run(api, repository, workflow, pr):
    query = urlencode({"event": "pull_request", "head_sha": pr["head"]["sha"]})
    path = f"/repos/{repository}/actions/workflows/{workflow}/runs?{query}"
    # One bounded page, newest first; never select an older failed run behind a newer run.
    runs = api.request(path + f"&per_page={MAX_RUNS}&page=1")["workflow_runs"]
    if not isinstance(runs, list) or len(runs) > MAX_RUNS:
        raise GateError("invalid workflow run page")
    matches = [run for run in runs if run_matches(run, pr)]
    if not matches and len(runs) == MAX_RUNS:
        raise GateError("workflow run search bound exceeded")
    return max(matches, key=lambda run: run["id"], default=None)


def run_matches(run, pr):
    pulls = run["pull_requests"]
    if not isinstance(pulls, list) or len(pulls) > MAX_PRS:
        raise GateError("invalid workflow PR associations")
    return (run["head_sha"] == pr["head"]["sha"]
            and run["event"] == "pull_request"
            and any(pull["number"] == pr["number"] for pull in pulls))


def was_held(api, repository, run):
    if run["status"] != "completed" or run["conclusion"] != "failure":
        return False
    jobs = page_items(
        api, f"/repos/{repository}/actions/runs/{run['id']}/jobs?filter=latest", "jobs"
    )
    for job in jobs:
        steps = job["steps"]
        if not isinstance(steps, list) or len(steps) > 128:
            raise GateError("invalid job steps")
        if any(step["name"] == HOLD_STEP and step["conclusion"] == "failure"
               for step in steps):
            return True
    return False


def rerun(repository, run_id):
    """Only called by the trusted release workflow, never from PR-controlled code."""
    subprocess.run(
        ["gh", "run", "rerun", str(int(run_id)), "--repo", repository],
        env={**os.environ, "GH_TOKEN": os.environ["GITHUB_TOKEN"]},
        check=True, timeout=30, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )


def release_pr(api, repository, pr, restart=rerun):
    if pr["draft"]:
        return
    runs = [latest_run(api, repository, workflow, pr) for workflow in WORKFLOWS]
    held = [run for run in runs if run and was_held(api, repository, run)]
    if not held:
        return
    clear, reason = evaluate(api, repository, pr["number"], pr["head"]["sha"])
    print(f"PR {pr['number']}: {reason}")
    if not clear:
        return
    for run in held:
        # Refresh immediately before mutation: a new head, attempt, or run cancels this release.
        workflow = run["path"].rsplit("/", 1)[-1]
        latest = latest_run(api, repository, workflow, pr)
        if (latest and latest["id"] == run["id"]
                and latest["run_attempt"] == run["run_attempt"]
                and was_held(api, repository, latest)):
            if not evaluate(api, repository, pr["number"], pr["head"]["sha"])[0]:
                return
            restart(repository, run["id"])
            print(f"Re-ran whole run {run['id']}")


def release(api, repository, restart=rerun):
    prs = page_items(api, f"/repos/{repository}/pulls?state=open", max_pages=2)
    if len(prs) > MAX_PRS:
        raise GateError("open PR bound exceeded")
    failed = False
    for pr in prs:
        try:
            release_pr(api, repository, pr, restart)
        except (GateError, KeyError, TypeError, subprocess.SubprocessError):
            # Keep other PRs moving, but make a partial/unreadable sweep visibly red.
            print(f"PR {pr.get('number', '?')}: release unreadable or rerun failed")
            failed = True
    return int(failed)


def selftest():
    import unittest

    import ci_review_gate_test

    suite = unittest.defaultTestLoader.loadTestsFromModule(ci_review_gate_test)
    return int(not unittest.TextTestRunner(verbosity=2).run(suite).wasSuccessful())


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("gate", "release", "selftest"))
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY"))
    parser.add_argument("--pr", type=int)
    parser.add_argument("--head")
    args = parser.parse_args(argv)
    if args.command == "selftest":
        return selftest()
    try:
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository or ""):
            raise GateError("invalid repository")
        api = API(os.environ.get("GITHUB_TOKEN"), (
            RELEASE_SECONDS if args.command == "release" else GATE_SECONDS
        ))
        if args.command == "release":
            return release(api, args.repository)
        if not args.pr or args.pr < 1 or not re.fullmatch(r"[0-9a-f]{40}", args.head or ""):
            raise GateError("invalid PR number or head SHA")
        verdict = evaluate(api, args.repository, args.pr, args.head)
    except GateError as error:
        if args.command == "release":
            print(f"release unreadable: {error}")
            return 1
        verdict = False, f"gate unreadable: {error}"
    emit(verdict, os.environ["GITHUB_OUTPUT"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
