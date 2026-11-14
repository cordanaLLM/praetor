#!/usr/bin/env python3
"""
PreToolUse & Local Git Hook Evasion Blocker
Enforces HISS by intercepting attempts to bypass Git hooks, linters, or verification gates.
"""

import sys
import os
import re
import json
from pathlib import Path
import shutil
import subprocess

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


def commit_command(command, short_pattern):
    """Reuse the short-flag rule's Git option prefilter without its flag tail."""
    head, _, _ = short_pattern.partition(GIT_OPTION_RUN_JOIN)
    domain = head + GIT_OPTION_RUN_JOIN + r"commit\b|am\b)"
    return search(domain, join_line_continuations(command))


def command_segments(command, short_pattern):
    """Scan executable message bodies iteratively, with no recursive parser calls."""
    queue, parts = [(command, False)], []
    for index in range(len(command) + 1):
        if index >= len(queue):
            break
        spans, nested = scan_shell_input(queue[index], short_pattern)
        parts.extend(spans)
        queue.extend(nested)
        if len(queue) > len(command) + 1:
            raise ValueError(
                f"invalid shell command: executable span bound exceeded: {len(command) + 1}"
            )
    return parts


def scan_shell_input(source, short_pattern):
    """Choose command parsing or executable extraction from document data."""
    command, document = source
    if not document:
        return scan_command_spans(command, short_pattern)
    spans = CommandSpans(command, short_pattern)
    for _ in range(len(command)):
        if spans.position >= len(command):
            break
        spans.document_step()
    if spans.stack:
        raise ValueError("invalid shell command: unbalanced shell stack")
    if spans.documents:
        raise ValueError("invalid shell command: unterminated here-document")
    spans.nested.extend((execution, False) for execution in spans.word_executions)
    return [], spans.nested


def scan_command_spans(command, short_pattern):
    """Mask data operands while collecting executable message substitutions."""
    if not commit_command(command, short_pattern):
        return [(command, bool(re.search(READ_ONLY_VETO, command)))], []
    spans = CommandSpans(command, short_pattern)
    for _ in range(len(command)):
        if spans.position >= len(command):
            break
        spans.step()
    spans.split(len(command), len(command))
    if spans.stack:
        raise ValueError("invalid shell command: unbalanced shell stack")
    if spans.documents:
        raise ValueError("invalid shell command: unterminated here-document")
    if spans.whole:
        return [("".join(spans.judged), True)], spans.nested
    return spans.parts, spans.nested


class CommandSpans:
    """Walk raw shell spans with bounded frames and here-document body lines."""

    nesting_limit = 16
    here_line_limit = 256
    state_start = READ_ONLY_VETO.index("(?i:")
    state_pattern = READ_ONLY_VETO[state_start:]
    here_pattern = r"^<<(-?)[ \t]*(\"[^\"]*\"|'[^']*'|[-A-Za-z0-9_]+)"
    redirect_pattern = r"^&?[<>](?:[<>|]|&[0-9-]*)?"
    data_option_pattern = re.compile(r"^(--(?:message|file|author|date)(=|$)|-[mFCc])")
    literal_pattern = re.compile(r'[^"\\$\x60\n]+')

    def __init__(self, command, short_pattern):
        """Initialize raw offsets, data masking and the shell-state veto."""
        self.command, self.judged = command, list(command)
        self.short_pattern, self.uncertain = short_pattern, False
        self.parts, self.stack, self.documents = [], [], []
        self.position = self.start = self.here_lines = 0
        self.whole = bool(re.search(self.state_pattern, command))
        self.whole |= bool(re.search(r"--%|%[;&|]", command))
        self.word_start = self.execution_start = -1
        self.execution_depth = 0
        self.next_data = self.redirect_data = self.word_expansion = False
        self.git_command = ""
        self.word_executions, self.nested = [], []

    def top(self):
        """Return the innermost quote or substitution terminator."""
        return self.stack[-1] if self.stack else ""

    def push(self, closer):
        """Bound simultaneously open lexical frames; fail closed at the limit."""
        if len(self.stack) == self.nesting_limit:
            raise ValueError("invalid shell command: nesting bound exceeded: 16")
        self.stack.append(closer)
        return True

    def quoted_step(self):
        """Handle escapes and constructs also active inside double quotes."""
        ch = self.command[self.position]
        if ch == "\\":
            self.uncertain = True
            self.position += 2
            if self.position > len(self.command):
                raise ValueError("invalid shell command: trailing escape")
            return True, True
        if ch in ('"', "\x60"):
            if self.top() == ch:
                self.close_frame()
            elif ch == "\x60":
                return True, self.open_execution("\x60", 1)
            else:
                self.push(ch)
            self.position += 1
            return True, True
        if ch == "$":
            self.word_expansion = True
            if self.command.startswith("$(", self.position):
                return True, self.open_execution(")", 2)
        return False, True

    def step(self):
        """Advance a word or construct, retaining double-quoted punctuation."""
        if self.word_step():
            return True
        handled, ok = self.quoted_step()
        if handled:
            return ok
        if self.top() == '"':
            literal = self.literal_pattern.match(self.command, self.position)
            if literal:
                self.position = literal.end()
                return True
            self.uncertain |= self.command[self.position] == "\n"
            self.position += 1
            return True
        return self.structural_step()

    def structural_step(self):
        """Interpret punctuation only outside literal double-quoted text."""
        ch = self.command[self.position]
        if ch == "'":
            return self.single_quote()
        if ch in "()":
            return self.parenthesis(ch)
        if ch == "#":
            return self.comment()
        return self.boundary_step(ch)

    def boundary_step(self, ch):
        """Handle boundaries, redirections and ordinary unquoted characters."""
        if ch in "<>":
            return self.redirection()
        if ch == "\n":
            return self.newline()
        if ch in ";&|":
            return self.separator()
        self.control_character(ch)
        self.position += 1
        return True

    def word_step(self):
        """Start outer words and complete them at ordinary blanks."""
        if self.stack:
            return False
        if self.command[self.position] in " \t":
            self.finish_word(self.position)
            self.position += 1
            return True
        if self.word_start < 0:
            self.word_start = self.position
        return False

    def finish_word(self, end):
        """Exclude only a data operand; other expansions retain the full scan."""
        if self.word_start < 0:
            return
        start = self.word_start
        word = self.command[start:end]
        self.enter_git_command(word, end)
        data = self.next_data or self.redirect_data
        offset = self.data_offset(word)
        if offset >= 0:
            self.mask(self.word_start + offset, end)
            self.nested.extend((execution, False) for execution in self.word_executions)
        else:
            self.uncertain |= self.word_expansion
        if data:
            self.next_data = self.next_data and self.redirect_data
        self.word_start = self.execution_start = -1
        self.word_expansion = self.redirect_data = False
        self.word_executions = []

    def enter_git_command(self, word, end):
        """Identify the Git subcommand before interpreting its operands."""
        if self.git_command or word not in ("commit", "am"):
            return
        start = self.start
        if commit_command(self.command[start:end], self.short_pattern):
            self.git_command = word

    def data_offset(self, word):
        """Identify a full data operand or an option's attached value."""
        if self.next_data or self.redirect_data:
            return 0
        if self.git_command != "commit":
            return -1
        option = self.data_option_pattern.match(word)
        if option is None:
            return -1
        self.next_data = option.end() == len(word) and not option[0].endswith("=")
        return option.end()

    def mask(self, start, end):
        """Remove data from short-option matching without changing offsets."""
        self.judged[start:end] = " " * (end - start)

    def open_execution(self, closer, width):
        """Record the outermost executable body within the current word."""
        self.word_expansion = True
        if self.execution_start < 0:
            self.execution_start = self.position + width
            self.execution_depth = len(self.stack) + 1
        self.position += width
        return self.push(closer)

    def close_frame(self):
        """Retain executable bodies for independent iterative command scans."""
        if len(self.stack) == self.execution_depth:
            start, end = self.execution_start, self.position
            self.word_executions.append(self.command[start:end])
            self.execution_start, self.execution_depth = -1, 0
        self.stack.pop()

    def parenthesis(self, ch):
        """Handle grouping independently from other structural characters."""
        if ch == "(":
            self.whole |= self.top() != ")"
            self.position += 1
            return self.push(")")
        if self.top() == ")":
            self.close_frame()
        else:
            self.whole = True
        self.position += 1
        return True

    def control_character(self, ch):
        """Keep unsupported syntax and positional globs conservative."""
        if self.stack:
            return
        self.word_expansion |= ch in "*?["
        if ch in "{}^!" or ord(ch) > 126 or ord(ch) < 32 and ch != "\t":
            self.whole = True

    def comment(self):
        """Ignore word-starting comments, including quotes and backslashes."""
        if self.position > 0 and self.command[self.position - 1] not in " \t\n;|&(":
            self.position += 1
            return True
        end = self.command.find("\n", self.position)
        if end < 0:
            end = len(self.command)
        self.mask(self.position, end)
        self.uncertain |= bool(self.stack)
        self.position = end
        return True

    def document_data(self, document, start):
        """Exclude body text while keeping executable expansions available."""
        position = self.position
        body = self.command[start:position]
        self.mask(start, self.position)
        if document[4] and any(ch in body for ch in "$\x60"):
            self.word_expansion = True
            self.nested.append((body, True))

    def document_step(self):
        """Treat document text as data; parse only executable substitutions."""
        if self.stack:
            return self.step()
        ch = self.command[self.position]
        if ch == "\\":
            self.position += min(2, len(self.command) - self.position)
        elif self.command.startswith("$(", self.position):
            return self.open_execution(")", 2)
        elif ch == "\x60":
            return self.open_execution("\x60", 1)
        else:
            self.position += 1
        return True

    def separator(self):
        """Distinguish combined-output redirection from command boundaries."""
        if self.command.startswith("&>", self.position):
            return self.redirection()
        if not self.stack:
            self.split(self.position, self.position + 1)
        self.position += 1
        return True

    def single_quote(self):
        """Consume literal text; refuse an unmatched quote."""
        end = self.command.find("'", self.position + 1)
        if end < 0:
            raise ValueError("invalid shell command: unbalanced quote")
        end += 1
        start = self.position
        literal = self.command[start:end]
        self.uncertain |= "\n" in literal or "\r" in literal
        self.position = end
        return True

    def split(self, end, next_position):
        """Record an outer boundary and reset its argument context."""
        self.finish_word(end)
        start = self.start
        part = "".join(self.judged[start:end])
        self.whole |= self.uncertain and commit_command(part, self.short_pattern)
        self.parts.append(
            (part, bool(re.search(READ_ONLY_VETO, self.command[start:end])))
        )
        self.start = next_position
        self.uncertain = False
        self.next_data = self.redirect_data = False
        self.git_command = ""

    def redirection(self):
        """Keep descriptor duplication and document openers in their span."""
        if not self.stack:
            self.finish_word(self.position)
        position = self.position
        rest = self.command[position:]
        if rest.startswith("<<"):
            match = re.match(self.here_pattern, rest)
            if match is None:
                raise ValueError(
                    "invalid shell command: unsupported here-document delimiter"
                )
            if len(self.documents) == self.nesting_limit:
                raise ValueError(
                    "invalid shell command: pending here-document bound exceeded: 16"
                )
            delimiter = match[2].strip("\"'")
            self.documents.append(
                (
                    delimiter,
                    match[1] == "-",
                    len(self.parts),
                    len(self.stack),
                    delimiter == match[2],
                )
            )
            self.position += match.end()
            return True
        redirect = re.match(self.redirect_pattern, rest)[0]
        self.position += len(redirect)
        self.redirect_data = not self.stack and not any(
            ch in redirect for ch in "0123456789-"
        )
        return True

    def newline(self):
        """Consume bounded document bodies before ending an outer command."""
        depth = len(self.stack)
        has_document = any(doc[3] == depth for doc in self.documents)
        self.uncertain |= depth > 0 and not has_document
        self.position += 1
        if depth == 0:
            self.split(self.position - 1, self.position)
        remaining = []
        for document in self.documents:
            if document[3] != depth:
                remaining.append(document)
                continue
            body_start = self.position
            self.document_body(document[0], document[1])
            self.document_data(document, body_start)
        self.documents = remaining
        if depth == 0:
            self.start = self.position
        return True

    def document_body(self, delimiter, tabs):
        """Consume bounded body lines; refuse unterminated or over-bound input."""
        while self.here_lines < self.here_line_limit:
            if self.position >= len(self.command):
                break
            self.here_lines += 1
            position = self.position
            line, found, _ = self.command[position:].partition("\n")
            self.position += len(line) + bool(found)
            if tabs:
                line = line.lstrip("\t")
            if line == delimiter:
                return True
        if self.here_lines == self.here_line_limit:
            raise ValueError(
                "invalid shell command: here-document body-line bound exceeded: 256"
            )
        raise ValueError("invalid shell command: unterminated here-document")


def search_command(pattern, text, segments, short_pattern):
    """Search short commit/am flags after each span's read-only exemption."""
    if pattern != short_pattern:
        return search(pattern, text)
    for segment in segments:
        text, veto = segment
        if not veto:
            text = without_read_only_words(text)
        if "-" in text and search(
            pattern, join_line_continuations(text).replace("\n", " ")
        ):
            return True
    return False


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
    raw_command = command_str
    command_str = join_line_continuations(command_str)
    if not scannable(command_str):
        return False
    try:
        segments = command_segments(raw_command, READ_ONLY_EXEMPT_PATTERNS[0])
    except ValueError as error:
        sys.stderr.write("[BLOCKED BY HISS] Invalid hook input: " + str(error) + "\n")
        return False
    judged = without_read_only_words(command_str)
    for pattern in BLOCKED_PATTERNS:
        text = judged if pattern in READ_ONLY_EXEMPT_PATTERNS else command_str
        if search_command(pattern, text, segments, READ_ONLY_EXEMPT_PATTERNS[0]):
            reason = (
                f"[BLOCKED BY HISS] verification evasion prohibited; commits, pushes and tool "
                f"calls pass verification gates; pattern: {pattern}"
            )
            sys.stderr.write(
                reason.encode()[:MAX_REASON_BYTES].decode(errors="ignore") + "\n"
            )
            return False

    for pattern in TOPOLOGY_PATTERNS:
        if re.search(pattern, command_str):
            sys.stderr.write(
                f"[BLOCKED BY DEV-01] adoption or needs target: workstation dev root; repositories "
                f"live inside organization folders as leaf Git repositories; pattern: {pattern}\n"
            )
            return False

    return check_admission(command_str)


# Host admission delegation to Go (`praetorctl admission-check`) when hooks.admission.enforce
# is enabled in operator settings (order engine-hook-build-scope-gate-20261027).
def install_manifest_path():
    if os.name == "nt":
        appdata = os.environ.get("APPDATA", "").strip()
        return Path(appdata) / "praetor" / "install.json" if appdata else None
    xdg = os.environ.get("XDG_CONFIG_HOME", "").strip()
    if xdg:
        return Path(xdg) / "praetor" / "install.json"
    home = os.environ.get("HOME", "").strip()
    return Path(home) / ".config" / "praetor" / "install.json" if home else None


def admission_enforce_in_yaml(text: str) -> bool:
    hooks_indent = None
    admission_indent = None
    for raw_line in text.splitlines()[:4096]:
        line = raw_line.split("#", 1)[0].rstrip()
        stripped = line.lstrip(" ")
        if not stripped:
            continue
        if re.search(
            r"\bhooks\s*:\s*\{[^{}]*\badmission\s*:\s*\{[^{}]*\benforce\s*:\s*true\b",
            stripped,
        ):
            return True
        indent = len(line) - len(stripped)
        if hooks_indent is not None and indent <= hooks_indent:
            hooks_indent = None
            admission_indent = None
        if admission_indent is not None and indent <= admission_indent:
            admission_indent = None
        if hooks_indent is None and re.match(r"^hooks\s*:\s*$", stripped):
            hooks_indent = indent
        elif hooks_indent is not None and admission_indent is None:
            if re.match(r"^admission\s*:\s*\{[^{}]*\benforce\s*:\s*true\b", stripped):
                return True
            if re.match(r"^admission\s*:\s*$", stripped):
                admission_indent = indent
        elif admission_indent is not None and re.match(
            r"^enforce\s*:\s*true\s*$", stripped
        ):
            return True
    return False


def admission_enforcement_state():
    """Return (enforce, isolate_manifest, error_reason) from selected operator settings."""
    fleet = os.environ.get("PRAETOR_FLEET_CONFIG", "").strip()
    workstation = os.environ.get("PRAETOR_WORKSTATION_CONFIG", "").strip()
    explicit = [Path(p) for p in (fleet, workstation) if p]
    manifest = install_manifest_path()
    manifest_docs = []
    manifest_missing = False
    if manifest is not None and manifest.is_file():
        try:
            data = json.loads(manifest.read_text(encoding="utf-8"))
            settings = data.get("settings") if isinstance(data, dict) else None
            if isinstance(settings, dict):
                for key in ("fleet", "workstation"):
                    doc = settings.get(key)
                    if (
                        isinstance(doc, dict)
                        and isinstance(doc.get("path"), str)
                        and doc["path"]
                    ):
                        path = Path(doc["path"])
                        if path.is_file():
                            manifest_docs.append(path)
                        else:
                            manifest_missing = True
        except (OSError, ValueError):
            if not explicit:
                return (
                    True,
                    False,
                    "[BLOCKED BY HISS] admission settings manifest unreadable",
                )
    paths = explicit if explicit else manifest_docs
    for path in paths:
        try:
            text = path.read_text(encoding="utf-8")
        except OSError as error:
            return (
                True,
                False,
                f"[BLOCKED BY HISS] admission settings unavailable: {error}",
            )
        if admission_enforce_in_yaml(text):
            return True, bool(explicit and manifest_missing), ""
    return False, False, ""


# An older praetorctl answers `admission-check` with this error: such a binary is skipped, so a
# stale bin/praetorctl in a checkout never refuses every command
# (order engine-hook-build-scope-gate-20261027).
ADMISSION_UNSUPPORTED = b"unknown command: admission-check"


def praetorctl_candidates():
    """The checkout's bin/praetorctl, then praetorctl on PATH, each once."""
    suffix = ".exe" if os.name == "nt" else ""
    found = []
    local = Path("bin") / f"praetorctl{suffix}"
    if local.is_file() and os.access(local, os.X_OK):
        found.append(str(local.resolve()))
    on_path = shutil.which("praetorctl")
    if on_path and on_path not in found:
        found.append(on_path)
    return found


def admission_env(isolate_manifest):
    child_env = dict(os.environ)
    if isolate_manifest:
        for var in ("PRAETOR_FLEET_CONFIG", "PRAETOR_WORKSTATION_CONFIG"):
            val = child_env.get(var, "").strip()
            if val:
                child_env["XDG_CONFIG_HOME"] = str(Path(val).resolve().parent)
                break
    return child_env


def run_admission_check(binary, command_str, child_env):
    """(verdict, reason): True allows, False refuses, None means this binary predates the check."""
    try:
        result = subprocess.run(
            [binary, "admission-check"],
            input=command_str.encode("utf-8"),
            env=child_env,
            capture_output=True,
            timeout=10,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as error:
        return False, f"[BLOCKED BY HISS] heavy-step admission check failed: {error}"
    if result.returncode == 0:
        return True, ""
    if ADMISSION_UNSUPPORTED in result.stderr + result.stdout:
        return None, ""
    reason = result.stderr.decode("utf-8", errors="replace").strip()
    return (
        False,
        reason
        or f"[BLOCKED BY HISS] heavy-step admission check exited {result.returncode}",
    )


def check_admission(command_str: str) -> bool:
    enforce, isolate_manifest, err = admission_enforcement_state()
    if err:
        sys.stderr.write(err + "\n")
        return False
    if not enforce:
        return True
    candidates = praetorctl_candidates()
    child_env = admission_env(isolate_manifest)
    for binary in candidates[:2]:
        verdict, reason = run_admission_check(binary, command_str, child_env)
        if verdict is None:
            continue
        if not verdict:
            sys.stderr.write(reason + "\n")
        return verdict
    if candidates:
        sys.stderr.write(
            "[BLOCKED BY HISS] heavy-step admission check unavailable: praetorctl too old at "
            + ", ".join(candidates)
            + "; update PATH engine or remove bin/praetorctl\n"
        )
    else:
        sys.stderr.write(
            "[BLOCKED BY HISS] heavy-step admission check unavailable: praetorctl not found\n"
        )
    return False


def audit_environment() -> bool:
    value = os.environ.get("LEFTHOOK")
    if value in LEFTHOOK_DISABLED:
        sys.stderr.write(
            f"[BLOCKED BY HISS] LEFTHOOK={value} detected in environment. Evasion prohibited.\n"
        )
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
        sys.stderr.write(
            "input: command, PreToolUse JSON, or --environment required.\n"
        )
        sys.exit(1)
    if not audit_command(cmd):
        sys.exit(1)
    if json_input:
        # A protocol marker, written as exact bytes: print() translates the newline to CRLF on
        # Windows, so the same approval read differently depending on the host.
        sys.stdout.buffer.write(
            b"PRAETOR_COMMAND_POLICY_OK\n"
        )  # caveman:not-applicable protocol-marker
        sys.stdout.buffer.flush()

    sys.exit(0)


if __name__ == "__main__":
    main()
