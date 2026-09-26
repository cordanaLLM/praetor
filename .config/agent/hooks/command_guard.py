#!/usr/bin/env python3
"""Share Lefthook's command policy across compatible native before-tool hooks."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from common import session_root

MAX_INPUT = 1024 * 1024
MAX_DIAGNOSTIC = 4096
PASSED = b"PRAETOR_COMMAND_POLICY_OK"


def payload_cwd(payload: bytes):
    """Return the session cwd a native payload names; the shared job judges everything else."""
    try:
        value = json.loads(payload)
    except (RecursionError, UnicodeDecodeError, ValueError):
        return None
    return value.get("cwd") if isinstance(value, dict) else None


def check_job(payload: bytes, job: str, marker: bytes) -> int:
    """Return blocking exit code 2 for every failed or unavailable check."""
    if not payload or len(payload) > MAX_INPUT:
        sys.stderr.write("Praetor: missing or oversized before-tool input.\n")
        return 2
    try:
        with tempfile.TemporaryFile() as output:
            result = subprocess.run(
                ["lefthook", "run", job, "--no-tty", "--no-auto-install"],
                cwd=session_root(ROOT, payload_cwd(payload)), input=payload,
                stdout=output, stderr=subprocess.STDOUT,
                timeout=10, check=False,
            )
            output.seek(0)
            diagnostic = output.read(MAX_DIAGNOSTIC)
            if result.returncode or marker not in diagnostic.splitlines():
                sys.stderr.write("Praetor: shared hook policy rejected this tool call.\n"
                                 + diagnostic.decode(errors="replace"))
                return 2
    except (OSError, subprocess.TimeoutExpired) as error:
        sys.stderr.write(f"Praetor: shared hook policy unavailable: {error}\n")
        return 2
    return 0


def check(payload: bytes) -> int:
    return check_job(payload, "agent-pre-tool", PASSED)


def main() -> int:
    return check(sys.stdin.buffer.read(MAX_INPUT + 1))


if __name__ == "__main__":
    sys.exit(main())
