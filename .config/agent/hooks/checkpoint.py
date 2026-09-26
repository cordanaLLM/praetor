#!/usr/bin/env python3
"""Adapt the shared Lefthook checkpoint result to native agent lifecycle events."""

import json
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from common import HookError, run_bounded, session_root

LIMIT = 1024 * 1024
MARKER = "PRAETOR_CHECKPOINT_RESULT="
STATE_MARKER = "PRAETOR_STATE_RESULT="


def verify_state(root=ROOT):
    """Require a fresh existing ledger; Stop must not silently perform its upkeep."""
    raw = run_bounded(
        ["lefthook", "run", "agent-state-stop", "--no-tty", "--no-auto-install"],
        cwd=root, timeout=20, max_output=LIMIT,
    )
    rows = [line[len(STATE_MARKER):] for line in raw.decode().splitlines()
            if line.startswith(STATE_MARKER)]
    if len(rows) != 1:
        raise ValueError("shared state job returned no unique execution result")
    report = json.loads(rows[0])
    if (not isinstance(report, dict) or type(report.get("schema_version")) is not int
            or report["schema_version"] != 1 or report.get("verified") is not True):
        raise ValueError("shared state job did not verify ledger freshness")


def checkpoint(event, root=ROOT):
    """Require execution of the configured shared job, not just exit zero."""
    raw = run_bounded(
        ["lefthook", "run", "agent-checkpoint-" + event,
         "--no-tty", "--no-auto-install"], cwd=root, timeout=30, max_output=LIMIT,
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


def respond(payload):
    if not isinstance(payload, dict):
        raise ValueError("hook input must be an object")
    name = payload.get("hook_event_name")
    if not isinstance(name, str) or name not in {"Stop", "PostToolUse", "AfterAgent", "AfterTool"}:
        raise ValueError("expected Stop, PostToolUse, AfterAgent or AfterTool event")
    if type(payload.get("stop_hook_active", False)) is not bool:
        raise ValueError("stop_hook_active must be a boolean")
    active = payload.get("stop_hook_active", False)
    stopping = name in {"Stop", "AfterAgent"}
    root = session_root(ROOT, payload.get("cwd"))
    if stopping:
        try:
            verify_state(root)
        except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
            reason = ("Praetor state could not be verified: " + str(error)[:1000]
                      + ". Inspect and repair the existing ledger, run praetorctl state "
                      "sync ., then retry; do not report completion while state is unverified.")
            return blocked(reason, active)
    try:
        report = checkpoint("stop" if stopping else "tool", root)
        if not report["enabled"] or not report["due"]:
            return {}
        reason = ("Praetor checkpoint due: " + "; ".join(report["actions"])
                  + ". Review and stage only task-owned public changes; run required "
                  "verification and normal signed-off commits/pushes. Reuse an existing "
                  "PR or create a draft for the exact pushed branch. Preserve unrelated "
                  "and private files. Report a concrete blocker if this cannot complete.")
    except (HookError, OSError, ValueError, subprocess.TimeoutExpired) as error:
        reason = "Praetor checkpoint could not be verified: " + str(error)[:1000]
    if stopping:
        return blocked(reason, active)
    return {"hookSpecificOutput": {"hookEventName": name,
                                   "additionalContext": reason}}


def main():
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if not raw or len(raw) > LIMIT:
            raise ValueError("missing or oversized lifecycle input")
        result = respond(json.loads(raw))
        print(json.dumps(result))
        return 0
    except (OSError, ValueError) as error:
        print("Praetor checkpoint adapter: " + str(error)[:1000], file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
