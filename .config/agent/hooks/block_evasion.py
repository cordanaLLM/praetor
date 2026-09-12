#!/usr/bin/env python3
"""
PreToolUse & Local Git Hook Evasion Blocker
Enforces HISS-16 by intercepting attempts to bypass Git hooks, linters, or verification gates.
"""
import sys
import os
import re
import json

BLOCKED_PATTERNS = [
    r"--no-verify\b",
    r"\bgit\s+commit\b[^\n]*\s-n\b",
    r"LEFTHOOK=0\b",
    r"SKIP=.*git",
    r"core\.hooksPath\s*=\s*/dev/null",
    r"rm\s+(-rf?\s+)?\.git/hooks",
]

TOPOLOGY_PATTERNS = [
    r"(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*(\bdev/?(\s|$)|/dev/(cordanaLLM|lusoris|vmafx|golusoris|upstream|local|stacks|worktrees|scratch)/?(\s|$))",
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

    for pattern in TOPOLOGY_PATTERNS:
        if re.search(pattern, command_str, re.IGNORECASE):
            sys.stderr.write(
                f"\n[BLOCKED BY DEV-01] Attempted adoption/needs target on organization container or dev root!\n"
                f"Pattern '{pattern}' targets an organization folder or dev root.\n"
                f"Repositories must live inside organization folders as leaf git repos.\n"
                f"Adopting an organization root folder or workstation dev root is strictly prohibited.\n\n"
            )
            return False

    return True

def audit_environment() -> bool:
    if os.environ.get("LEFTHOOK") == "0":
        sys.stderr.write("[BLOCKED BY HISS-16] LEFTHOOK=0 detected in environment. Evasion prohibited.\n")
        return False
    if os.environ.get("LEFTHOOK_EXCLUDE") or os.environ.get("LEFTHOOK_SKIP"):
        sys.stderr.write("[BLOCKED BY HISS-16] Hook exclusions are prohibited.\n")
        return False
    return True

def main():
    if not audit_environment():
        sys.exit(1)

    if sys.argv[1:] == ["--environment"]:
        sys.exit(0)
    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
    elif not sys.stdin.isatty():
        try:
            payload = json.load(sys.stdin)
            tool_input = payload.get("tool_input", {})
            cmd = tool_input.get("command", "")
            if not isinstance(cmd, str):
                raise ValueError("tool_input.command must be text")
        except (ValueError, AttributeError) as error:
            sys.stderr.write(f"[BLOCKED BY HISS-16] Invalid hook input: {error}\n")
            sys.exit(1)
    else:
        sys.stderr.write("Expected a command, PreToolUse JSON, or --environment.\n")
        sys.exit(1)
    if not audit_command(cmd):
        sys.exit(1)

    sys.exit(0)

if __name__ == "__main__":
    main()
