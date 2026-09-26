#!/usr/bin/env python3
"""Adapt the shared Lefthook checkpoint result to native agent lifecycle events."""

import json
from pathlib import Path
import subprocess
import sys
import threading

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from common import HookBudget, HookError, refuse_after, run_bounded, session_root

LIMIT = 1024 * 1024
MARKER = "PRAETOR_CHECKPOINT_RESULT="
STATE_MARKER = "PRAETOR_STATE_RESULT="
EVENTS = {"Stop", "PostToolUse", "AfterAgent", "AfterTool"}
STOPPING = {"Stop", "AfterAgent"}
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
        cwd=root, timeout=budget.timeout(STATE_TIMEOUT), max_output=LIMIT, grace=JOB_GRACE,
    )
    rows = [line[len(STATE_MARKER):] for line in raw.decode().splitlines()
            if line.startswith(STATE_MARKER)]
    if len(rows) != 1:
        raise ValueError("shared state job returned no unique execution result")
    report = json.loads(rows[0])
    if (not isinstance(report, dict) or type(report.get("schema_version")) is not int
            or report["schema_version"] != 1 or report.get("verified") is not True):
        raise ValueError("shared state job did not verify ledger freshness")


def checkpoint(event, root=ROOT, budget=None):
    """Require execution of the configured shared job, not just exit zero."""
    budget = HookBudget(HOOK_BUDGET) if budget is None else budget
    raw = run_bounded(
        ["lefthook", "run", "agent-checkpoint-" + event, "--no-tty", "--no-auto-install"],
        cwd=root, timeout=budget.timeout(CHECKPOINT_TIMEOUT), max_output=LIMIT, grace=JOB_GRACE,
    )
    rows = [line[len(MARKER):] for line in raw.decode().splitlines()
            if line.startswith(MARKER)]
    if len(rows) != 1:
        raise ValueError("shared checkpoint job returned no unique execution result")
    report = json.loads(rows[0])
    if (not isinstance(report, dict) or report.get("schema_version") != 1
            or type(report.get("enabled")) is not bool
            or type(report.get("due")) is not bool
            or not isinstance(report.get("actions"), list)
            or len(report["actions"]) > 16
            or not all(isinstance(item, str) and len(item) <= 1024
                       for item in report["actions"])):
        raise ValueError("shared checkpoint job returned an invalid result")
    if report["due"] and not report["actions"]:
        raise ValueError("due checkpoint has no actionable disposition")
    return report


def blocked(reason, active):
    """One continuation can reconcile the checkpoint; repeat failure stays explicit."""
    if active:
        return {"continue": False, "stopReason": "Checkpoint blocked: " + reason,
                "systemMessage": "Checkpoint remains incomplete: " + reason}
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


def respond(payload, budget=None):
    name, active = lifecycle_event(payload)
    budget = HookBudget(HOOK_BUDGET) if budget is None else budget
    stopping = name in STOPPING
    try:
        root = session_root(ROOT, payload.get("cwd"), budget)
    except HookError as error:
        return feedback(name, active, "Praetor checkpoint could not be verified: "
                        + str(error)[:1000])
    if stopping:
        try:
            verify_state(root, budget)
        except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
            reason = ("Praetor state could not be verified: " + str(error)[:1000]
                      + ". Inspect and repair the existing ledger, run praetorctl state "
                      "sync ., then retry; do not report completion while state is unverified.")
            return blocked(reason, active)
    try:
        report = checkpoint("stop" if stopping else "tool", root, budget)
        if not report["enabled"] or not report["due"]:
            return {}
        reason = ("Praetor checkpoint due: " + "; ".join(report["actions"])
                  + ". Review and stage only task-owned public changes; run required "
                  "verification and normal signed-off commits/pushes. Reuse an existing "
                  "PR or create a draft for the exact pushed branch. Preserve unrelated "
                  "and private files. Report a concrete blocker if this cannot complete.")
    except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
        reason = "Praetor checkpoint could not be verified: " + str(error)[:1000]
    return feedback(name, active, reason)


def answer(result):
    """Print the adapter's one response; False when the backstop already printed it."""
    if not ANSWERED.acquire(blocking=False):
        return False
    print(json.dumps(result), flush=True)
    return True


def overrun(payload):
    """The backstop's response: the event stays unverified, blocking a stop."""
    try:
        name, active = lifecycle_event(payload)
    except ValueError:
        return None  # respond() rejects this input itself, long before the limit.
    reason = (f"Praetor checkpoint adapter gave no answer within {HOOK_LIMIT} s; the "
              "checkpoint is unverified. Retry once the host responds again.")
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
            backstop.join(HOOK_STOP)  # The backstop is printing; let it finish and exit.
        return 0
    except (OSError, ValueError) as error:
        print("Praetor checkpoint adapter: " + str(error)[:1000], file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
