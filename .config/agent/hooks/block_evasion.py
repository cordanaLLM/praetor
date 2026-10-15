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
    r"--no-v(e(r(i(f(y)?)?)?)?)?\b",
    r"\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+(commit\b[^\n]*\s-[aeiopqsvz]*|am\b[^\n]*\s-[3cikmqsu]*)n",
    r"LEFTHOOK=[\x22\x27]?(0|false)\b",
    r"SKIP=.*git",
    r"(?i:core\.hookspath)(\s*=|\s+[\x22\x27]?[/~.$A-Za-z_\\])",
    r"\b(?i:rm|rmdir|unlink|mv|cp|ln|chmod|chown|chattr|truncate|shred|tee|del|erase|rd|ri|remove-item|move|move-item|ren|rename|rename-item|copy|copy-item|set-content|add-content|out-file|icacls|attrib)\b[^\n]*(?i:\.git[/\\]hooks)",
    r"(?m)^(?:(?:[^\Wsp]\w*|s(?:[^\We]\w*|e(?:[^\Wd]\w*|d\w+)?)?|p(?:[^\We]\w*|e(?:[^\Wr]\w*|r(?:[^\Wl]\w*|l\w+)?)?)?)?[^\w\n])*(sed|perl)\b[^\n]*\s(-[A-Za-z]*i|--in-place)[^\n]*(?i:\.git[/\\]hooks)",
    r"(?m)^(?:(?:[^\Wf]\w*|f(?:[^\Wi]\w*|i(?:[^\Wn]\w*|n(?:[^\Wd]\w*|d\w+)?)?)?)?[^\w\n])*(find)\b[^\n]*(?i:\.git[/\\]hooks)[^\n]*\s-(delete|exec|execdir|ok)\b",
    r">\s*[\x22\x27]?[^ \t\n\x22\x27]*(?i:\.git[/\\]hooks)",
    r"\blefthook\s+uninstall\b",
    r"(?m)^(?:(?:[^\Wg]\w*|g(?:[^\Wh]\w*|h\w+)?)?[^\w\n])*(gh)\b[^\n]*(?:\bpr\s+merge\b[^\n]*(?:\s|[\x22\x27])--admin(?:\s|$|[;&|\x22\x27]|=true\b)|(?:\s|[\x22\x27])--admin(?:\s|$|[;&|\x22\x27]|=true\b)[^\n]*\bpr\s+merge\b|\bpr\s+--admin(?:\s|$|[;&|\x22\x27]|=true\b)[^\n]*\bmerge\b)",
    r"(?m)^(?:(?:[^\Wg]\w*|g(?:[^\Wh]\w*|h\w+)?)?[^\w\n])*(gh)\b[^\n]*\bapi\b[^\n]*\bpulls/[0-9]+/merge/?(?:\?|\s|$|[;&|\x22\x27])",
    r"(?m)^(?:(?:[^\Wg]\w*|g(?:[^\Wh]\w*|h\w+)?)?[^\w\n])*(gh)\b[^\n]*\bapi\s+graphql\b[^\n]*\bmergePullRequest\b",
    r"(?m)^(?:(?:[^\Wcw]\w*|c(?:[^\Wu]\w*|u(?:[^\Wr]\w*|r(?:[^\Wl]\w*|l\w+)?)?)?|w(?:[^\Wg]\w*|g(?:[^\We]\w*|e(?:[^\Wt]\w*|t\w+)?)?)?)?[^\w\n])*(curl|wget)\b[^\n]*\bapi\.github\.com/[^\n]*\bpulls/[0-9]+/merge/?(?:\?|\s|$|[;&|\x22\x27])",
]

# The rules judged without the words of chained read-only commands (internal/agenthook/policy.go,
# readOnlyExemptRules), byte for byte; TestPythonGuardCarriesTheReadOnlyExemption fails when
# the lists differ. Every other rule judges the whole command.
READ_ONLY_EXEMPT_PATTERNS = [
    r"\bgit([ \t]+-[Cc][ \t]+(\x22[^\x22]*\x22|\x27[^\x27]*\x27|[^ \t\n\x22\x27][^ \t\n]*)|[ \t]+(--[A-Za-z][-A-Za-z]*|-[ABD-Zabd-z][-A-Za-z]*|-[Cc][A-Za-z]+)(=[^ \t\n]+)?)*\s+(commit\b[^\n]*\s-[aeiopqsvz]*|am\b[^\n]*\s-[3cikmqsu]*)n",
    r"SKIP=.*git",
]

# agenthook.ReadOnlyWords and agenthook.ReadOnlyVeto, byte for byte (same test).
READ_ONLY_WORDS = r"((?:;|&&|\|\|?)[ \t]*(?:git[ \t]+(?:--no-pager[ \t]+)?(?:log|show|status|diff|rev-parse)|head|tail|grep|wc))(?:[ \t]+[-A-Za-z0-9_./:=@,+~*?]+)*|((?:;|&&|\|\|?)[ \t]*sed)[ \t]+-n\b(?:[ \t]+[-A-Za-z0-9_./:=@,+~*?]+)*"
READ_ONLY_VETO = r"[^\t\x20-\x7e]|[\\\x60$(){}<>^!]|--%|%[;&|]|(?i:(?:\A|[^-A-Za-z0-9_])(?:export|declare|typeset|set|setenv|alias|function|sal|nal|set-alias|new-alias|doskey|hash)(?:[^-A-Za-z0-9_]|$))"

# agenthook.HeredocOperator, HeredocReaders, HeredocQuoted, HeredocVeto and HeredocSubstitution,
# byte for byte (TestPythonGuardCarriesTheHeredocRule): the heredoc bodies a text reader
# consumes are no command (without_heredoc_bodies, #1091).
HEREDOC_OPERATOR = r"(?:^|[^<])<<(-?)[ \t]*(?:\x27([A-Za-z0-9_.-]+)\x27|\x22([A-Za-z0-9_.-]+)\x22|(\\?)([A-Za-z0-9_.-]+))"
HEREDOC_READERS = r"^[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=[^ \t]*[ \t]+)*(?:git|gh|glab|cat|tee)(?:[ \t]|$)"
HEREDOC_QUOTED = r"\x27[^\x27]*\x27|\x22[^\x22$\x60\\]*\x22"
HEREDOC_VETO = r"[|;&(){}$\x60]"
HEREDOC_SUBSTITUTION = r"\$\(|\x60"

# Lefthook skips every hook for exactly these LEFTHOOK values (lefthook v2.1.14,
# internal/command/run.go); agenthook's lefthookDisableValues holds the same pair.
LEFTHOOK_DISABLED = ("0", "false")

# The engine's generic dev-root rule (internal/agenthook/policy.go, builtinDevRoot), byte for
# byte; TestPythonGuardCarriesTheBuiltinDevRootRule fails when the two differ. It names no
# organisation folder: those are operator data, configured in hooks.command_policy.deny.
TOPOLOGY_PATTERNS = [
    r"(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev/?(\s|$)",
]

# Every refusal below is agenthook's wording (internal/agenthook/policy.go), character for
# character: TestParityRefusalTextWithThePythonGuard compares this guard's full stderr with
# the engine's for each case, so the two cannot drift apart.
def scannable(command_str: str) -> bool:
    if len(command_str) <= MAX_SCAN_CHARS:
        if max(len(line) for line in command_str.split("\n")) <= MAX_SCAN_LINE_CHARS:
            return True
    sys.stderr.write(
        f"[BLOCKED BY HISS] command exceeds scan bound: at most {MAX_SCAN_CHARS} characters, "
        f"{MAX_SCAN_LINE_CHARS} per line; split command or write long content to file first\n"
    )
    return False


def without_read_only_words(command_str: str) -> str:
    """Drop the words of read-only commands chained after ;, &&, || or |, names kept.

    agenthook's withoutReadOnlyWords: a command READ_ONLY_VETO matches stays whole.
    """
    if re.search(READ_ONLY_VETO, command_str):
        return command_str
    return re.sub(READ_ONLY_WORDS, r"\1\2", command_str)


def join_line_continuations(command_str: str) -> str:
    """Join backslash line continuations into a single space before any rule runs."""
    return re.sub(r"\\\r?\n", " ", command_str)


def without_heredoc_bodies(command_str: str) -> str:
    """Drop the bodies and delimiter lines of the heredocs a text reader consumes.

    agenthook's withoutHeredocBodies: only an operator line that is one simple command whose
    program HEREDOC_READERS names loses its bodies, and only bodies that end and, for an unquoted
    delimiter, hold no command substitution. Every other line is kept.
    """
    lines = command_str.split("\n")
    kept = []
    index = 0
    while index < len(lines):
        line = lines[index]
        kept.append(line)
        index += 1
        operators = re.findall(HEREDOC_OPERATOR, line)
        if (not operators or re.search(HEREDOC_VETO, re.sub(HEREDOC_QUOTED, "", line))
                or not re.search(HEREDOC_READERS, line)):
            continue
        end = heredoc_bodies_end(lines, index, operators)
        if end is not None:
            index = end
    return "\n".join(kept)


def heredoc_bodies_end(lines, start, operators):
    """Return the line after the bodies of operators starting at lines[start], or None."""
    index = start
    for dash, single, double, backslash, bare in operators:
        body = []
        while index < len(lines):
            candidate = lines[index].removesuffix("\r")
            if dash:
                candidate = candidate.lstrip("\t")
            if candidate == single + double + bare:
                break
            body.append(lines[index])
            index += 1
        if index == len(lines):
            return None
        quoted = single or double or backslash
        if not quoted and re.search(HEREDOC_SUBSTITUTION, "\n".join(body)):
            return None
        index += 1
    return index


def audit_command(command_str: str) -> bool:
    command_str = join_line_continuations(command_str)
    if not scannable(command_str):
        return False
    command_str = without_heredoc_bodies(command_str)
    judged = without_read_only_words(command_str)
    for pattern in BLOCKED_PATTERNS:
        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str
        if re.search(pattern, text):
            sys.stderr.write(
                f"[BLOCKED BY HISS] verification evasion prohibited; commits, pushes and tool "
                f"calls pass verification gates; pattern: {pattern}\n"
            )
            return False

    for pattern in TOPOLOGY_PATTERNS:
        if re.search(pattern, command_str):
            sys.stderr.write(
                f"[BLOCKED BY DEV-01] adoption or needs target: workstation dev root; repositories "
                f"live inside organization folders as leaf Git repositories; pattern: {pattern}\n"
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
