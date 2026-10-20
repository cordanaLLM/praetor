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
MAX_REASON_BYTES = 4096
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
    r"\A(?:[^\x22\x27\\]|\\(?s:.)|\x27[^\x27]*\x27|\x22(?:[^\x22\\]|\\(?s:.))*\x22)*(?:\A|[\n;&|(\x60]|\x22(?:[^\x22\\]|\\(?s:.))*(?:\$\(|\x60))[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*=(?:(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+))?[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:builtin)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:chrt)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+)[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:command)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:env)(?:[ \t]+(?:(?:-u|--unset|-C|--chdir)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x63(?:\x68(?:\x64(?:\x69(?:\x72(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x68][^ \t\n;&|()\x60\x22\x27]*)?|\x75(?:\x6e(?:\x73(?:\x65(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63\x75][^ \t\n;&|()\x60\x22\x27]*)?|\x43(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x75(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x43\x75][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:exec)(?:[ \t]+(?:(?:-a)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x61(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:flock)(?:[ \t]+(?:(?:-w|--timeout|-E|--conflict-exit-code)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x63(?:\x6f(?:\x6e(?:\x66(?:\x6c(?:\x69(?:\x63(?:\x74(?:\x2d(?:\x65(?:\x78(?:\x69(?:\x74(?:\x2d(?:\x63(?:\x6f(?:\x64(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x78][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x66][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|\x74(?:\x69(?:\x6d(?:\x65(?:\x6f(?:\x75(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x75][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63\x74][^ \t\n;&|()\x60\x22\x27]*)?|\x45(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x77(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x45\x77][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+)[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:ionice)(?:[ \t]+(?:(?:-c|--class|-n|--classdata|-p|--pid|-P|--pgid|-u|--uid)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x63(?:\x6c(?:\x61(?:\x73(?:\x73(?:\x64(?:\x61(?:\x74(?:\x61(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|\x70(?:\x67(?:\x69(?:\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|\x69(?:\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x67\x69][^ \t\n;&|()\x60\x22\x27]*)?|\x75(?:\x69(?:\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63\x70\x75][^ \t\n;&|()\x60\x22\x27]*)?|\x50(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x63(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x6e(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x70(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x75(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x50\x63\x6e\x70\x75][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:nice)(?:[ \t]+(?:(?:-n|--adjustment)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x61(?:\x64(?:\x6a(?:\x75(?:\x73(?:\x74(?:\x6d(?:\x65(?:\x6e(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x75][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6a][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|\x6e(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x6e][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:nohup)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:setsid)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:stdbuf)(?:[ \t]+(?:(?:-i|-o|-e)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x69(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x6f(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65\x69\x6f][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:systemd-run)(?:[ \t]+(?:(?:-p|--property|-u|--unit|-E|--setenv|--slice|-M|--machine|-H|--host|--uid|--gid|--nice|--working-directory|--description)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x64(?:\x65(?:\x73(?:\x63(?:\x72(?:\x69(?:\x70(?:\x74(?:\x69(?:\x6f(?:\x6e(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x70][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|\x67(?:\x69(?:\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|\x68(?:\x6f(?:\x73(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|\x6d(?:\x61(?:\x63(?:\x68(?:\x69(?:\x6e(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x68][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|\x6e(?:\x69(?:\x63(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|\x70(?:\x72(?:\x6f(?:\x70(?:\x65(?:\x72(?:\x74(?:\x79(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x79][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x70][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|\x73(?:\x65(?:\x74(?:\x65(?:\x6e(?:\x76(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x76][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|\x6c(?:\x69(?:\x63(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65\x6c][^ \t\n;&|()\x60\x22\x27]*)?|\x75(?:\x69(?:\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|\x6e(?:\x69(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69\x6e][^ \t\n;&|()\x60\x22\x27]*)?|\x77(?:\x6f(?:\x72(?:\x6b(?:\x69(?:\x6e(?:\x67(?:\x2d(?:\x64(?:\x69(?:\x72(?:\x65(?:\x63(?:\x74(?:\x6f(?:\x72(?:\x79(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x79][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x67][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6b][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x64\x67\x68\x6d\x6e\x70\x73\x75\x77][^ \t\n;&|()\x60\x22\x27]*)?|\x45(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x48(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x4d(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x70(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x75(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x45\x48\x4d\x70\x75][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:taskset)(?:[ \t]+(?:-(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+)[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:time)(?:[ \t]+(?:(?:-f|--format|-o|--output)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x66(?:\x6f(?:\x72(?:\x6d(?:\x61(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|\x6f(?:\x75(?:\x74(?:\x70(?:\x75(?:\x74(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x75][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x70][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x75][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x66\x6f][^ \t\n;&|()\x60\x22\x27]*)?|\x66(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x6f(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x66\x6f][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:timeout)(?:[ \t]+(?:(?:-k|--kill-after|-s|--signal)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x6b(?:\x69(?:\x6c(?:\x6c(?:\x2d(?:\x61(?:\x66(?:\x74(?:\x65(?:\x72(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x66][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|\x73(?:\x69(?:\x67(?:\x6e(?:\x61(?:\x6c(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6e][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x67][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6b\x73][^ \t\n;&|()\x60\x22\x27]*)?|\x6b(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x73(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x6b\x73][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+)[ \t]+|(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:xargs)(?:[ \t]+(?:(?:-a|--arg-file|-d|--delimiter|-E|-e|-I|-i|-L|-l|-n|--max-args|-P|--max-procs|-s|--max-chars)[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)|-(?:\x2d(?:\x61(?:\x72(?:\x67(?:\x2d(?:\x66(?:\x69(?:\x6c(?:\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x66][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x67][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|\x64(?:\x65(?:\x6c(?:\x69(?:\x6d(?:\x69(?:\x74(?:\x65(?:\x72(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x74][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x69][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6c][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x65][^ \t\n;&|()\x60\x22\x27]*)?|\x6d(?:\x61(?:\x78(?:\x2d(?:\x61(?:\x72(?:\x67(?:\x73(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x67][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|\x63(?:\x68(?:\x61(?:\x72(?:\x73(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x68][^ \t\n;&|()\x60\x22\x27]*)?|\x70(?:\x72(?:\x6f(?:\x63(?:\x73(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x73][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x63][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x6f][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x72][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61\x63\x70][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x2d][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x78][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61][^ \t\n;&|()\x60\x22\x27]*)?|[^ \t\n;&|()\x60\x22\x27\x61\x64\x6d][^ \t\n;&|()\x60\x22\x27]*)?|\x45(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x49(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x4c(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x50(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x61(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x64(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x65(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x69(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x6c(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x6e(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|\x73(?:[^ \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*)|[^ \t\n;&|()\x60\x22\x27\x2d\x45\x49\x4c\x50\x61\x64\x65\x69\x6c\x6e\x73][^ \t\n;&|()\x60\x22\x27]*)))*[ \t]+)*(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:(?:(?:sudo|doas|pkexec|run0)(?:[ \t\r\n;&|()\x60]|$)|su(?:[ \t]+(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+))*[ \t]+(?:-c[^ \t\n;&|()\x60]*|--command(?:=[^ \t\n;&|()\x60]*)?)(?:[ \t\r\n;&|()\x60]|$)))",
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

# Lefthook skips every hook for exactly these LEFTHOOK values (lefthook v2.1.14,
# internal/command/run.go); agenthook's lefthookDisableValues holds the same pair.
LEFTHOOK_DISABLED = ("0", "false")

# The engine's generic dev-root rule (internal/agenthook/policy.go, builtinDevRoot), byte for
# byte; TestPythonGuardCarriesTheBuiltinDevRootRule fails when the two differ. It names no
# organisation folder: those are operator data, configured in hooks.command_policy.deny.
TOPOLOGY_PATTERNS = [
    r"(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*\bdev[/\\]?(\s|$)",
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


# Every match of the privilege rule contains one of these literal program names.
# BuiltinRule.RequiredAny supplies the same prefilter to Go and the emitted interceptor.
RULE_PREFILTERS = {BLOCKED_PATTERNS[-1]: ("sudo", "doas", "pkexec", "run0", "su")}


# The git rule with an option run (the short skip flag rule) is searched once per run (#1283).
# re retries a rule at every git it finds, and a git inside an option token (-git, --x=git,
# -C dir/git) starts at a boundary of the run an earlier git already walked: each retry walked
# the rest of that run again, quadratic in the line. A start at a walked boundary reaches the
# same end, and only a run's end can be followed by the rest of the rule (every option starts
# with a blank and a dash), so skipping it keeps re.search's answer. The adopter interceptor
# carries this code byte for byte (TestEmittedInterceptorSearchesLikeTheEngineGuard).
GIT_OPTION_RUN_HEAD = r"\bgit("
GIT_OPTION_RUN_JOIN = r")*\s+("


def search(pattern, text):
    """re.search(pattern, text) is not None, with the git option-run rule in linear time."""
    literals = RULE_PREFILTERS.get(pattern, ())
    if literals and not any(literal in text for literal in literals):
        return False
    head, join, tail = pattern.partition(GIT_OPTION_RUN_JOIN)
    if not join or not head.startswith(GIT_OPTION_RUN_HEAD):
        return re.search(pattern, text) is not None
    first = len(GIT_OPTION_RUN_HEAD)
    option = re.compile(head[first:])
    rest = re.compile(r"\s+(" + tail)
    walked = set()
    for found in re.finditer(r"\bgit", text):
        position = found.end()
        if position in walked:
            continue
        while position not in walked:
            walked.add(position)
            step = option.match(text, position)
            if step is None or step.end() == position:
                break
            position = step.end()
        if rest.match(text, position):
            return True
    return False


def audit_command(command_str: str) -> bool:
    command_str = join_line_continuations(command_str)
    if not scannable(command_str):
        return False
    judged = without_read_only_words(command_str)
    for pattern in BLOCKED_PATTERNS:
        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str
        if search(pattern, text):
            reason = (
                f"[BLOCKED BY HISS] verification evasion prohibited; commits, pushes and tool "
                f"calls pass verification gates; pattern: {pattern}"
            )
            sys.stderr.write(reason.encode()[:MAX_REASON_BYTES].decode(errors="ignore") + "\n")
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
