#!/usr/bin/env python3
"""Share Lefthook's command policy across compatible native before-tool hooks."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from common import HookBudget, HookError, SESSION_GIT_GRACE, refuse_after, session_root

MAX_INPUT = 1024 * 1024
MAX_DIAGNOSTIC = 4096
PASSED = b"PRAETOR_COMMAND_POLICY_OK"
# Every client registers this guard with a 15 s timeout (.claude/settings.json,
# .gemini/settings.json, .codex/hooks.json) and lets the call through once it passes (see
# HookBudget). The checkout lookup and the shared job share GUARD_BUDGET; a step that overruns
# is stopped within 2 * SESSION_GIT_GRACE (the job is killed outright), and GUARD_LIMIT refuses
# the call whatever the guard is still blocked on. What remains of the 15 s starts the
# interpreter. scripts/test_checkpoint_hooks.py checks this arithmetic against each registration.
GUARD_BUDGET = 10
GUARD_JOB_TIMEOUT = 10
GUARD_STOP = 2 * SESSION_GIT_GRACE
GUARD_LIMIT = 12


def payload_cwd(payload: bytes):
    """Return the session cwd a native payload names; the shared job judges everything else."""
    try:
        value = json.loads(payload)
    except (RecursionError, UnicodeDecodeError, ValueError):
        return None
    return value.get("cwd") if isinstance(value, dict) else None


def overrun() -> int:
    sys.stderr.write(f"Praetor: shared hook policy gave no answer within {GUARD_LIMIT} s; "
                     "refusing the call.\n")
    sys.stderr.flush()
    return 2


def check_job(payload: bytes, job: str, marker: bytes) -> int:
    """Return blocking exit code 2 for every failed or unavailable check."""
    if not payload or len(payload) > MAX_INPUT:
        sys.stderr.write("Praetor: missing or oversized before-tool input.\n")
        return 2
    backstop = refuse_after(GUARD_LIMIT, overrun)
    try:
        return run_job(payload, job, marker, HookBudget(GUARD_BUDGET))
    finally:
        backstop.cancel()


def run_job(payload: bytes, job: str, marker: bytes, budget: HookBudget) -> int:
    """Run the shared job in the session's checkout; exit 2 unless it printed ``marker``."""
    try:
        cwd = session_root(ROOT, payload_cwd(payload), budget)
        with tempfile.TemporaryFile() as output:
            result = subprocess.run(
                ["lefthook", "run", job, "--no-tty", "--no-auto-install"],
                cwd=cwd, input=payload, stdout=output, stderr=subprocess.STDOUT,
                timeout=budget.timeout(GUARD_JOB_TIMEOUT), check=False,
            )
            output.seek(0)
            diagnostic = output.read(MAX_DIAGNOSTIC)
            if result.returncode or marker not in diagnostic.splitlines():
                sys.stderr.write("Praetor: shared hook policy rejected this tool call.\n"
                                 + diagnostic.decode(errors="replace"))
                return 2
    except (HookError, OSError, subprocess.TimeoutExpired) as error:
        sys.stderr.write(f"Praetor: shared hook policy unavailable: {error}\n")
        return 2
    return 0


def check(payload: bytes) -> int:
    return check_job(payload, "agent-pre-tool", PASSED)


def main() -> int:
    return check(sys.stdin.buffer.read(MAX_INPUT + 1))


if __name__ == "__main__":
    sys.exit(main())
