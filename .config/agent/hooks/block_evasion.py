#!/usr/bin/env python3
"""
PreToolUse & Local Git Hook Evasion Blocker
Enforces HISS by intercepting attempts to bypass Git hooks, linters, or verification gates.
"""
import sys
import os
import re
import json

MAX_INPUT_BYTES = 1 << 20

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
                f"\n[BLOCKED BY HISS] Attempted verification evasion detected!\n"
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
        sys.stderr.write("[BLOCKED BY HISS] LEFTHOOK=0 detected in environment. Evasion prohibited.\n")
        return False
    if os.environ.get("LEFTHOOK_EXCLUDE") or os.environ.get("LEFTHOOK_SKIP"):
        sys.stderr.write("[BLOCKED BY HISS] Hook exclusions are prohibited.\n")
        return False
    return True


def read_json_command(stream) -> str:
    raw = stream.read(MAX_INPUT_BYTES + 1)
    if len(raw) > MAX_INPUT_BYTES:
        raise ValueError("hook input exceeds 1 MiB")
    payload = json.loads(raw.decode("utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("hook input must be an object")
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        raise ValueError("tool_input must be an object")
    command = tool_input.get("command")
    if not isinstance(command, str) or not command.strip():
        raise ValueError("tool_input.command must be nonempty text")
    return command


def main():
    if not audit_environment():
        sys.exit(1)

    if sys.argv[1:] == ["--environment"]:
        sys.exit(0)
    json_input = False
    if len(sys.argv) > 1:
        cmd = " ".join(sys.argv[1:])
    elif not sys.stdin.isatty():
        try:
            cmd = read_json_command(sys.stdin.buffer)
            json_input = True
        except (ValueError, OSError, RecursionError) as error:
            sys.stderr.write(f"[BLOCKED BY HISS] Invalid hook input: {error}\n")
            sys.exit(1)
    else:
        sys.stderr.write("Expected a command, PreToolUse JSON, or --environment.\n")
        sys.exit(1)
    if not audit_command(cmd):
        sys.exit(1)
    if json_input:
        # A protocol marker, written as exact bytes: print() translates the newline to CRLF on
        # Windows, so the same approval read differently depending on the host.
        sys.stdout.buffer.write(b"PRAETOR_COMMAND_POLICY_OK\n")
        sys.stdout.buffer.flush()

    sys.exit(0)

if __name__ == "__main__":
    main()
