#!/usr/bin/env python3
"""Share Lefthook's command policy using Codex PreToolUse blocking semantics."""

from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[3]
MAX_INPUT = 1024 * 1024
MAX_DIAGNOSTIC = 4096
PASSED = b"PRAETOR_COMMAND_POLICY_OK"


def check(payload: bytes) -> int:
    """Return Codex's blocking exit code for every failed or unavailable check."""
    if not payload or len(payload) > MAX_INPUT:
        sys.stderr.write("Praetor: missing or oversized PreToolUse input.\n")
        return 2
    try:
        with tempfile.TemporaryFile() as output:
            result = subprocess.run(
                ["lefthook", "run", "agent-pre-tool", "--no-tty", "--no-auto-install"],
                cwd=ROOT, input=payload, stdout=output, stderr=subprocess.STDOUT,
                timeout=10, check=False,
            )
            output.seek(0)
            diagnostic = output.read(MAX_DIAGNOSTIC)
            if result.returncode or PASSED not in diagnostic.splitlines():
                sys.stderr.write("Praetor: shared hook policy rejected this tool call.\n"
                                 + diagnostic.decode(errors="replace"))
                return 2
    except (OSError, subprocess.TimeoutExpired) as error:
        sys.stderr.write(f"Praetor: shared hook policy unavailable: {error}\n")
        return 2
    return 0


def main() -> int:
    return check(sys.stdin.buffer.read(MAX_INPUT + 1))


if __name__ == "__main__":
    sys.exit(main())
