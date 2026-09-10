#!/usr/bin/env python3
"""
PreToolUse & Local Git Hook Evasion Blocker
Enforces HISS-16 by intercepting attempts to bypass Git hooks, linters, or verification gates.
"""
import sys
import os
import re

BLOCKED_PATTERNS = [
    r"--no-verify\b",
    r"-n\b(?=.*git\s+commit)",
    r"LEFTHOOK=0\b",
    r"SKIP=.*git",
    r"core\.hooksPath\s*=\s*/dev/null",
    r"rm\s+(-rf?\s+)?\.git/hooks",
]

def audit_command(command_str: str) -> bool:
    for pattern in BLOCKED_PATTERNS:
        if re.search(pattern, command_str):
            sys.stderr.write(
                f"\n[BLOCKED BY HISS-16] Attempted verification evasion detected!\n"
                f"Pattern '{pattern}' is strictly prohibited in cordanaLLM repositories.\n"
                f"All commits, pushes, and tool invocations must pass verification gates cleanly.\n\n"
            )
            return False
    return True

def audit_environment() -> bool:
    if os.environ.get("LEFTHOOK") == "0":
        sys.stderr.write("[BLOCKED BY HISS-16] LEFTHOOK=0 detected in environment. Evasion prohibited.\n")
        return False
    return True

def main():
    if not audit_environment():
        sys.exit(1)

    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
        if not audit_command(cmd):
            sys.exit(1)

    sys.exit(0)

if __name__ == "__main__":
    main()

