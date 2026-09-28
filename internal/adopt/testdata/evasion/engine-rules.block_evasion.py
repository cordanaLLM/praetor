#!/usr/bin/env python3
"""Agent PreToolUse evasion interceptor (HISS), written by praetorctl adopt.

Wire this script as a PreToolUse hook of the agent harness's shell tool. The harness passes
the pending tool call as one JSON object on stdin with the shell command at
tool_input.command; a command may instead be passed as arguments. Exit code 2 blocks the
call and returns the reason to the agent. Input of any other shape is refused, not allowed.

The rules are the engine's built-in command policy (internal/agenthook), rendered at
adoption; operator rules belong in hooks.command_policy.deny of .standards.yaml.

A git hook cannot observe --no-verify because git skips hooks entirely, so this script
is deliberately not part of lefthook.yml.
"""
import json
import os
import re
import sys

MAX_INPUT_BYTES = 1048576
MAX_SCAN_CHARS = 65536
MAX_SCAN_LINE_CHARS = 2048
BLOCK_EXIT = 2

RULES = [
    (r"--no-verify\b", "HISS"),
    (r"\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+commit\b[^\n]*\s-[aeiopqsvz]*n", "HISS"),
    (r"LEFTHOOK=[\x22\x27]?(0|false)\b", "HISS"),
    (r"SKIP=.*git", "HISS"),
    (r"(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])", "HISS"),
    (r"\b(rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee)\b[^\n]*\.git[/\\]hooks", "HISS"),
    (r"\b(sed|perl)\b[^\n]*\s(-[A-Za-z]*i|--in-place)[^\n]*\.git[/\\]hooks", "HISS"),
    (r"\bfind\b[^\n]*\.git[/\\]hooks[^\n]*\s-(delete|exec|execdir|ok)\b", "HISS"),
    (r">\s*[\x22\x27]?[^ \t\n\x22\x27]*\.git[/\\]hooks", "HISS"),
    (r"\blefthook\s+uninstall\b", "HISS"),
    (r"(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)", "DEV-01"),
]

LEFTHOOK_DISABLED = ("0", "false",)
LEFTHOOK_NARROWING = ("LEFTHOOK_EXCLUDE", "LEFTHOOK_SKIP",)


class Blocked(Exception):
    pass

def block(invariant, reason):
    raise Blocked("[BLOCKED BY " + invariant + "] " + reason + "\n")


def read_payload_command(stream):
    raw = stream.read(MAX_INPUT_BYTES + 1)
    if len(raw) > MAX_INPUT_BYTES:
        raise ValueError("hook input exceeds " + str(MAX_INPUT_BYTES) + " bytes")
    payload = json.loads(raw.decode("utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("hook input must be one JSON object")
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict):
        raise ValueError("tool_input must be an object")
    command = tool_input.get("command")
    if not isinstance(command, str) or not command.strip():
        raise ValueError("tool_input.command must be nonempty text")
    return command


def pending_command():
    if len(sys.argv) > 1:
        return " ".join(sys.argv[1:])
    if sys.stdin.isatty():
        block("HISS", "expected PreToolUse JSON on stdin or a command as arguments")
    try:
        return read_payload_command(sys.stdin.buffer)
    except (ValueError, OSError, RecursionError) as error:
        block("HISS", "invalid hook input: " + str(error))


def require_scannable(command):
    # re backtracks, so a longer command or line could stall this script past the harness's
    # hook timeout. Such a command is refused, never truncated.
    if len(command) <= MAX_SCAN_CHARS:
        if max(len(line) for line in command.split("\n")) <= MAX_SCAN_LINE_CHARS:
            return
    block("HISS", "command exceeds the scan bound: at most " + str(MAX_SCAN_CHARS) + " characters, "
          + str(MAX_SCAN_LINE_CHARS) + " per line; split it or write the long content to a file first.")


def main():
    try:
        value = os.environ.get("LEFTHOOK")
        if value in LEFTHOOK_DISABLED:
            block("HISS", "LEFTHOOK=" + value + " detected in environment. Evasion prohibited.")
        for name in LEFTHOOK_NARROWING:
            if os.environ.get(name):
                block("HISS", "Hook exclusions are prohibited.")
        command = pending_command()
        require_scannable(command)
        for pattern, invariant in RULES:
            if re.search(pattern, command):
                block(invariant, "Verification evasion prohibited: " + pattern)
        sys.exit(0)
    except Blocked as e:
        sys.stderr.write(str(e))
        sys.exit(BLOCK_EXIT)


if __name__ == "__main__":
    main()
