#!/usr/bin/env python3
"""Native bridge to the configured Lefthook checkpoint batch-scope job."""

from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[3]
LIMIT = 1024 * 1024
sys.path.insert(0, str(ROOT / ".config/agent/hooks"))
from command_guard import check_job


def main():
    raw = sys.stdin.buffer.read(LIMIT + 1)
    if not raw or len(raw) > LIMIT:
        raise ValueError("missing or oversized native pre-tool input")
    return check_job(raw, "agent-checkpoint-pre-edit", b"PRAETOR_CHECKPOINT_SCOPE_OK")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError) as error:
        print("Praetor batch scope bridge: " + str(error)[:1000], file=sys.stderr)
        sys.exit(2)
