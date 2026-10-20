#!/usr/bin/env python3
"""Adapt the shared Lefthook checkpoint result to native agent lifecycle events."""

import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import threading
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from common import (
    HookBudget,
    HookError,
    refuse_after,
    resolved_relative_to,
    run_bounded,
    session_root,
)

LIMIT = 1024 * 1024
MARKER = "PRAETOR_CHECKPOINT_RESULT="
STATE_MARKER = "PRAETOR_STATE_RESULT="
EVENTS = {"Stop", "PostToolUse", "AfterAgent", "AfterTool"}
STOPPING = {"Stop", "AfterAgent"}
SESSION_ENV = "PRAETOR_CHECKPOINT_SESSION"
MAX_WORKTREES = 512
MAX_RECORD_BYTES = 512 * 1024
SESSION_GIT_TIMEOUT = 2
SESSION_GIT_OUTPUT = 16 * 1024
# Every client registers this adapter with a 60 s timeout (.claude/settings.json,
# .gemini/settings.json, .codex/hooks.json) and lets the event through once it passes (see
# HookBudget). The checkout lookup, the state job and the checkpoint job share HOOK_BUDGET. A
# job that overruns gets JOB_GRACE to stop, which outlasts the praetor CLI's own 5 s wait for
# the Git commands it runs (see STOP_GRACE), and as long again to be reaped. HOOK_LIMIT answers
# for the adapter whatever it is still blocked on. What remains of the 60 s starts the
# interpreter. scripts/test_checkpoint_hooks.py checks this arithmetic against each registration.
HOOK_BUDGET = 42
STATE_TIMEOUT = 20
CHECKPOINT_TIMEOUT = 30
JOB_GRACE = 6
HOOK_STOP = 2 * JOB_GRACE
HOOK_LIMIT = 55
ANSWERED = threading.Lock()


def verify_state(root=ROOT, budget=None):
    """Require a fresh existing ledger; Stop must not silently perform its upkeep."""
    budget = HookBudget(HOOK_BUDGET) if budget is None else budget
    raw = run_bounded(
        ["lefthook", "run", "agent-state-stop", "--no-tty", "--no-auto-install"],
        cwd=root,
        timeout=budget.timeout(STATE_TIMEOUT),
        max_output=LIMIT,
        grace=JOB_GRACE,
    )
    rows = [
        line.removeprefix(STATE_MARKER)
        for line in raw.decode().splitlines()
        if line.startswith(STATE_MARKER)
    ]
    if len(rows) != 1:
        raise ValueError("shared state job returned no unique execution result")
    report = json.loads(rows[0])
    if (
        not isinstance(report, dict)
        or type(report.get("schema_version")) is not int
        or report["schema_version"] != 1
        or report.get("verified") is not True
    ):
        raise ValueError("shared state job did not verify ledger freshness")


def checkpoint(event, root=ROOT, budget=None, env=None):
    """Require execution of the configured shared job, not just exit zero."""
    budget = HookBudget(HOOK_BUDGET) if budget is None else budget
    cmd_env = dict(os.environ)
    if env:
        cmd_env.update(env)
    raw = run_bounded(
        [
            "lefthook",
            "run",
            "agent-checkpoint-" + event,
            "--no-tty",
            "--no-auto-install",
        ],
        cwd=root,
        timeout=budget.timeout(CHECKPOINT_TIMEOUT),
        max_output=LIMIT,
        grace=JOB_GRACE,
        env=cmd_env,
    )
    rows = [
        line.removeprefix(MARKER)
        for line in raw.decode().splitlines()
        if line.startswith(MARKER)
    ]
    if len(rows) != 1:
        raise ValueError("shared checkpoint job returned no unique execution result")
    report = json.loads(rows[0])
    if (
        not isinstance(report, dict)
        or report.get("schema_version") != 1
        or type(report.get("enabled")) is not bool
        or type(report.get("due")) is not bool
        or not isinstance(report.get("actions"), list)
        or len(report["actions"]) > 16
        or not all(
            isinstance(item, str) and len(item) <= 1024 for item in report["actions"]
        )
        or (
            report.get("deferred") is True
            and (
                report["due"]
                or report["actions"]
                or not isinstance(report.get("deferral"), str)
                or not report["deferral"]
            )
        )
    ):
        raise ValueError("shared checkpoint job returned an invalid result")
    if report["due"] and not report["actions"]:
        raise ValueError("due checkpoint has no actionable disposition")
    return report


def extract_session_id(payload):
    """Read session ID from agent payload without guessing."""
    if not isinstance(payload, dict):
        return None
    session_id = payload.get("session_id")
    if isinstance(session_id, str) and session_id.strip():
        return session_id.strip()
    conv_id = payload.get("conversationId")
    if isinstance(conv_id, str) and conv_id.strip():
        return conv_id.strip()
    return None


def session_hash_for(session_id):
    """Return first 16 hex chars of SHA256 of session ID. Never store raw ID."""
    return hashlib.sha256(session_id.encode("utf-8")).hexdigest()[:16]


def _git_common_dir(root, budget):
    """Resolve repository common Git directory."""
    raw = run_bounded(
        ["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
        cwd=root,
        timeout=budget.timeout(SESSION_GIT_TIMEOUT),
        max_output=SESSION_GIT_OUTPUT,
        grace=JOB_GRACE,
    )
    return Path(raw.decode().strip()).resolve()


def _listed_worktrees(root, budget):
    """Read bounded NUL-delimited worktree listing."""
    raw = run_bounded(
        ["git", "worktree", "list", "--porcelain", "-z"],
        cwd=root,
        timeout=budget.timeout(SESSION_GIT_TIMEOUT),
        max_output=LIMIT,
        grace=JOB_GRACE,
    )
    entries, entry = [], {}
    for part in raw.split(b"\0"):
        field = os.fsdecode(part)
        if not field:
            if entry and "path" in entry:
                entries.append(entry)
                entry = {}
            if len(entries) >= MAX_WORKTREES:
                break
            continue
        key, sep, val = field.partition(" ")
        if key == "worktree" and sep:
            entry["path"] = val
    if entry and "path" in entry and len(entries) < MAX_WORKTREES:
        entries.append(entry)
    return entries


MAX_EXTRACT_NODES = 256


def _extract_strings(data):
    """Extract string values iteratively from tool input under a scalar bound."""
    result = []
    stack = [data]
    for _ in range(MAX_EXTRACT_NODES):
        if not stack:
            break
        item = stack.pop()
        if isinstance(item, str):
            result.append(item)
        elif isinstance(item, dict):
            stack.extend(item.values())
        elif isinstance(item, (list, tuple)):
            stack.extend(item)
    return result


def _extract_tool_info(payload):
    """Extract tool name and tool input from payload."""
    tool_name = payload.get("tool_name")
    tool_input = payload.get("tool_input")
    if not tool_name and isinstance(payload.get("toolCall"), dict):
        call = payload["toolCall"]
        tool_name = call.get("name")
        tool_input = call.get("args")
    return str(tool_name or ""), tool_input


def _path_matches(resolved_path_str, text):
    """Compare literal paths in tool text after resolving both filesystem spellings."""
    # Keep quoted paths with spaces intact; also find absolute operands embedded
    # in shell commands or scripts. Never execute or expand the command text.
    pattern = r"""(?<![\w./\\-])(?:"[^"]+"|'[^']+'|(?:[A-Za-z]:[/\\]|/|\\\\)[^\s'";&|<>()]+)"""
    candidates = [
        text,
        *(match.group().strip("\"'") for match in re.finditer(pattern, text)),
    ]
    for candidate in candidates:
        if not Path(candidate).is_absolute():
            continue
        try:
            resolved_relative_to(candidate, resolved_path_str)
            return True
        except (OSError, RuntimeError, ValueError):
            continue
    return False


def _is_worktree_touched(resolved, tool_name, tool_input, strings):
    """Whether worktree was touched by tool invocation."""
    if tool_name == "EnterWorktree" and isinstance(tool_input, dict):
        target = tool_input.get("name") or tool_input.get("path")
        if isinstance(target, str) and target.strip() in (resolved.name, str(resolved)):
            return True
    path_str = str(resolved)
    return any(_path_matches(path_str, s) for s in strings)


def _read_session_record(record_file):
    """Read existing worktree paths from session record file."""
    try:
        with record_file.open("rb") as stream:
            raw = stream.read(MAX_RECORD_BYTES + 1)
        if len(raw) > MAX_RECORD_BYTES:
            return set()
        data = json.loads(raw)
        if isinstance(data, list):
            return {str(Path(p).resolve()) for p in data if isinstance(p, str)}
    except (OSError, json.JSONDecodeError, UnicodeDecodeError):
        pass
    return set()


def _write_session_record(sessions_dir, record_file, paths):
    """Atomically write sorted bounded worktree paths to session record."""
    sessions_dir.mkdir(parents=True, exist_ok=True)
    sorted_paths = sorted(paths)[:MAX_WORKTREES]
    payload = json.dumps(sorted_paths).encode("utf-8")
    tmp = None
    try:
        with tempfile.NamedTemporaryFile(dir=sessions_dir, delete=False) as handle:
            tmp = Path(handle.name)
            handle.write(payload)
        os.replace(tmp, record_file)
        tmp = None
    finally:
        if tmp is not None:
            try:
                tmp.unlink(missing_ok=True)
            except OSError:
                pass


def ensure_session_record(root, session_hash, budget):
    """Ensure session record file exists before running stop checkpoint."""
    common_dir = _git_common_dir(root, budget)
    sessions_dir = common_dir / "praetor-checkpoint" / "sessions"
    record_file = sessions_dir / f"{session_hash}.json"
    if not record_file.is_file():
        _write_session_record(sessions_dir, record_file, set())


def record_tool_worktrees(payload, root, budget):
    """Record any worktree paths touched by tool event."""
    session_id = extract_session_id(payload)
    if not session_id:
        return
    session_hash = session_hash_for(session_id)
    tool_name, tool_input = _extract_tool_info(payload)
    if not tool_input:
        return
    strings = _extract_strings(tool_input)
    common_dir = _git_common_dir(root, budget)
    sessions_dir = common_dir / "praetor-checkpoint" / "sessions"
    record_file = sessions_dir / f"{session_hash}.json"

    worktrees = _listed_worktrees(root, budget)
    touched = set()
    for entry in worktrees:
        try:
            resolved = Path(entry["path"]).resolve(strict=True)
        except (OSError, RuntimeError):
            continue
        if resolved == root:
            continue
        if _is_worktree_touched(resolved, tool_name, tool_input, strings):
            touched.add(str(resolved))
    if not touched and record_file.is_file():
        return
    existing = _read_session_record(record_file) if record_file.is_file() else set()
    if touched - existing or not record_file.is_file():
        _write_session_record(sessions_dir, record_file, existing | touched)


def blocked(reason, active):
    """One continuation can reconcile the checkpoint; repeat failure stays explicit."""
    if active:
        return {
            "continue": False,
            "stopReason": "Checkpoint blocked: " + reason,
            "systemMessage": "Checkpoint remains incomplete: " + reason,
        }
    return {"decision": "block", "reason": reason}


def feedback(name, active, reason):
    """Block a stopping event; annotate a tool event, whose tool has already run."""
    if name in STOPPING:
        return blocked(reason, active)
    return {"hookSpecificOutput": {"hookEventName": name, "additionalContext": reason}}


def lifecycle_event(payload):
    """Return the validated event name and whether a stop hook already continued once."""
    if not isinstance(payload, dict):
        raise ValueError("hook input must be an object")
    name = payload.get("hook_event_name")
    if not isinstance(name, str) or name not in EVENTS:
        raise ValueError("expected Stop, PostToolUse, AfterAgent or AfterTool event")
    if type(payload.get("stop_hook_active", False)) is not bool:
        raise ValueError("stop_hook_active must be a boolean")
    return name, payload.get("stop_hook_active", False)


def _prepare_session(payload, root, budget, stopping):
    """Prepare session record and environment variable for checkpoint."""
    session_id = extract_session_id(payload)
    if not session_id:
        return None
    session_hash = session_hash_for(session_id)
    try:
        if stopping:
            ensure_session_record(root, session_hash, budget)
        else:
            record_tool_worktrees(payload, root, budget)
    except Exception as error:  # noqa: BLE001
        print("Praetor checkpoint adapter: " + str(error)[:1000], file=sys.stderr)
    return {SESSION_ENV: session_hash}


def respond(payload, budget=None):
    name, active = lifecycle_event(payload)
    budget = HookBudget(HOOK_BUDGET) if budget is None else budget
    stopping = name in STOPPING
    try:
        root = session_root(ROOT, payload.get("cwd"), budget)
    except HookError as error:
        return feedback(
            name,
            active,
            "Praetor checkpoint could not be verified: " + str(error)[:1000],
        )
    session_env = _prepare_session(payload, root, budget, stopping)
    if stopping:
        try:
            verify_state(root, budget)
        except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
            reason = (
                "Praetor state could not be verified: "
                + str(error)[:1000]
                + ". Inspect and repair the existing ledger, run praetorctl state "
                "sync ., then retry; do not report completion while state is unverified."
            )
            return blocked(reason, active)
    try:
        report = checkpoint(
            "stop" if stopping else "tool", root, budget, env=session_env
        )
        if (
            report.get("deferred") is True
        ):  # a detached job that cannot publish: printed, never a block
            return {
                "systemMessage": "Praetor checkpoint deferred: " + report["deferral"]
            }
        if not report["enabled"] or not report["due"]:
            return {}
        reason = (
            "Praetor checkpoint due: "
            + "; ".join(report["actions"])
            + ". Review and stage only task-owned public changes; run required "
            "verification and normal signed-off commits/pushes. Reuse an existing "
            "PR or create a draft for the exact pushed branch. Preserve unrelated "
            "and private files. Report a concrete blocker if this cannot complete."
        )
    except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
        reason = "Praetor checkpoint could not be verified: " + str(error)[:1000]
    return feedback(name, active, reason)


def answer(result):
    """Print the adapter's one response; False when the backstop already printed it."""
    if not ANSWERED.acquire(blocking=False):
        return False
    print(json.dumps(result), flush=True)  # caveman:not-applicable structured-protocol
    return True


def overrun(payload):
    """The backstop's response: the event stays unverified, blocking a stop."""
    try:
        name, active = lifecycle_event(payload)
    except ValueError:
        return None  # respond() rejects this input itself, long before the limit.
    reason = (
        f"Praetor checkpoint adapter gave no answer within {HOOK_LIMIT} s; the "
        "checkpoint is unverified. Retry once the host responds again."
    )
    return 0 if answer(feedback(name, active, reason)) else None


def main():
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if not raw or len(raw) > LIMIT:
            raise ValueError("missing or oversized lifecycle input")
        payload = json.loads(raw)
        backstop = refuse_after(HOOK_LIMIT, lambda: overrun(payload))
        try:
            result = respond(payload, HookBudget(HOOK_BUDGET))
        finally:
            backstop.cancel()
        if not answer(result):
            backstop.join(
                HOOK_STOP
            )  # The backstop is printing; let it finish and exit.
        return 0
    except (OSError, ValueError) as error:
        print("Praetor checkpoint adapter: " + str(error)[:1000], file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
