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
# agenthook.MaxScanChars and MaxScanLineChars (TestPythonGuardCarriesTheScanBounds): re
# backtracks, so a longer command or line could stall this guard past the client's hook
# timeout. Such a command is refused, never truncated.
MAX_SCAN_CHARS = 65536
MAX_SCAN_LINE_CHARS = 2048

# The engine's built-in evasion rules (internal/agenthook/policy.go, builtinEvasion), byte
# for byte; TestPythonGuardCarriesTheBuiltinEvasionList fails when the two lists differ.
BLOCKED_PATTERNS = [
    r"--no-verify\b",
    r"\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+commit\b[^\n]*\s-[aeiopqsvz]*n",
    r"LEFTHOOK=[\x22\x27]?(0|false)\b",
    r"SKIP=.*git",
    r"(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])",
    r"\b(rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee)\b[^\n]*\.git[/\\]hooks",
    r"\b(sed|perl)\b[^\n]*\s(-[A-Za-z]*i|--in-place)[^\n]*\.git[/\\]hooks",
    r"\bfind\b[^\n]*\.git[/\\]hooks[^\n]*\s-(delete|exec|execdir|ok)\b",
    r">\s*[\x22\x27]?[^ \t\n\x22\x27]*\.git[/\\]hooks",
    r"\blefthook\s+uninstall\b",
]

# Lefthook skips every hook for exactly these LEFTHOOK values (lefthook v2.1.14,
# internal/command/run.go); agenthook's lefthookDisableValues holds the same pair.
LEFTHOOK_DISABLED = ("0", "false")

# The engine's generic dev-root rule (internal/agenthook/policy.go, builtinDevRoot), byte for
# byte; TestPythonGuardCarriesTheBuiltinDevRootRule fails when the two differ. It names no
# organisation folder: those are operator data, configured in hooks.command_policy.deny.
TOPOLOGY_PATTERNS = [
    r"(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)",
]

def scannable(command_str: str) -> bool:
    if len(command_str) <= MAX_SCAN_CHARS:
        if max(len(line) for line in command_str.split("\n")) <= MAX_SCAN_LINE_CHARS:
            return True
    sys.stderr.write(
        f"\n[BLOCKED BY HISS] Command exceeds scan bound: at most {MAX_SCAN_CHARS} characters, "
        f"{MAX_SCAN_LINE_CHARS} per line.\n"
        f"Split command, or write long content to file first.\n\n"
    )
    return False


def audit_command(command_str: str) -> bool:
    if not scannable(command_str):
        return False
    for pattern in BLOCKED_PATTERNS:
        if re.search(pattern, command_str):
            sys.stderr.write(
                f"\n[BLOCKED BY HISS] verification evasion detected.\n"
                f"pattern: '{pattern}'; policy: prohibited in governed repositories.\n"
                f"required: commits, pushes, and tool invocations pass verification gates cleanly.\n\n"
            )
            return False

    for pattern in TOPOLOGY_PATTERNS:
        if re.search(pattern, command_str):
            sys.stderr.write(
                f"\n[BLOCKED BY DEV-01] adoption/needs target rejects workstation dev root.\n"
                f"pattern: '{pattern}'; target: dev root.\n"
                f"required: repositories live inside organization folders as leaf Git repositories.\n"
                f"prohibited: workstation dev root adoption.\n\n"
            )
            return False

    return True

def audit_environment() -> bool:
    value = os.environ.get("LEFTHOOK")
    if value in LEFTHOOK_DISABLED:
        sys.stderr.write(f"[BLOCKED BY HISS] LEFTHOOK={value} detected in environment. Evasion prohibited.\n")
        return False
    if os.environ.get("LEFTHOOK_EXCLUDE") or os.environ.get("LEFTHOOK_SKIP"):
        sys.stderr.write("[BLOCKED BY HISS] hook exclusions: prohibited.\n")
        return False
    return True


def read_json_command(stream) -> str:
    raw = stream.read(MAX_INPUT_BYTES + 1)
    if len(raw) > MAX_INPUT_BYTES:
        raise ValueError(f"hook input exceeds {MAX_INPUT_BYTES} bytes")
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
        sys.stderr.write("input: command, PreToolUse JSON, or --environment required.\n")
        sys.exit(1)
    if not audit_command(cmd):
        sys.exit(1)
    if json_input:
        # A protocol marker, written as exact bytes: print() translates the newline to CRLF on
        # Windows, so the same approval read differently depending on the host.
        sys.stdout.buffer.write(b"PRAETOR_COMMAND_POLICY_OK\n")  # caveman:not-applicable protocol-marker
        sys.stdout.buffer.flush()

    sys.exit(0)

if __name__ == "__main__":
    main()
