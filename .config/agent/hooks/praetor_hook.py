#!/usr/bin/env python3
"""Hand one tracked hook row to a praetorctl that serves it, or skip with the reason.

This repository's client files reach the subagent rows of `praetorctl hook <client> <event>`
through this launcher instead of through PATH alone. An engine built before a row existed
answers that row with its usage and exit 2, and exit 2 is a block in Claude Code, Codex and
Gemini CLI: every subagent launch stops until someone reinstalls. The engine cannot fix
that for binaries that are already installed, so the check happens here, before any engine
sees the call.

`praetorctl hook` with no arguments lists every pair an engine serves, in every engine
version. The launcher asks each candidate in order and hands the call, stdin included, to
the first one that lists this pair:

1. `bin/praetorctl` of the checkout this file belongs to, built from that tree by
   `make hook-cli` and kept current by the Git hooks, so it serves the rows the tree
   registers;
2. `praetorctl` from PATH, the installed engine.

When no candidate serves the pair, or none exists, the call is a stated skip: exit 0 and the
reason on stderr, the same shape the engine gives an event newer than itself. AGY also needs
a decision object on stdout, so the launcher prints the one the engine's agy encoder prints
for a skip. The gate is then not enforced, and the reason says so. A version-skewed or
missing engine never blocks the client. docs/guides/agent-hooks.md describes the rollout.

The AGY plugin carries a byte-identical copy of this file at
.agents/plugins/praetor/praetor_hook.py: AGY runs a plugin's hooks from the plugin directory,
and an installed plugin directory may sit outside any checkout. Both copies sit three
directories below the checkout root. TestPluginLauncherIsTheTrackedLauncher pins them equal.
"""

import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import threading

HERE = Path(__file__).resolve()
# The checkout root, for both tracked copies (.config/agent/hooks/, .agents/plugins/praetor/).
ROOT = HERE.parents[3] if len(HERE.parents) > 3 else HERE.parent
# The engine's own argument grammar (internal/agenthook argumentShape).
ARGUMENT = re.compile(r"[a-z-]+")
SUFFIX = ".exe" if os.name == "nt" else ""
PROBE_TIMEOUT = 5
PROBE_LIMIT = 64 * 1024
# How long the launcher waits for the engine's verdict. It outwaits every engine budget of a
# launcher row (budgetFor in internal/agenthook/evaluate.go: 10 s for the dispatch and handback
# rows, 30 s for post-return), so a slow engine still gives its own verdict. Both probes plus
# this wait end before the longest launcher row (60 s) gives up; a shorter row's client timeout
# ends that row first. TestLauncherTimeoutsOutwaitEngineBudgets pins both bounds.
RUN_TIMEOUT = 45
# The engine reads at most 1 MiB + 1 byte of payload (agenthook.MaxInputBytes).
DRAIN_LIMIT = 1024 * 1024 + 1
DRAIN_CHUNK = 64 * 1024
DRAIN_TIMEOUT = 2
# What a client needs on stdout to go on when no engine gives a verdict. The native clients
# go on after exit 0 with nothing on stdout. AGY's contract always wants a JSON object: the
# one the engine prints for a skip (agyEncodePreTool, agyEncodeStop in
# internal/agenthook/dialect_agy.go). An agy event missing here has no known answer shape.
PROCEED = {
    ("agy", "pre-tool"): '{"decision":"allow"}\n',
    ("agy", "pre-dispatch"): '{"decision":"allow"}\n',
    ("agy", "stop"): "{}\n",
}


def candidates(root, which=shutil.which):
    """Existing engines in preference order, each path once."""
    found = []
    local = root / "bin" / ("praetorctl" + SUFFIX)
    # Only a checkout builds bin/praetorctl. Above an installed AGY plugin, root is an
    # unrelated directory whose bin/ is not this tree's engine.
    if (root / ".git").exists() and local.is_file():
        found.append(str(local))
    installed = which("praetorctl")
    if installed and not any(same_file(installed, engine) for engine in found):
        found.append(installed)
    return found


def same_file(first, second):
    try:
        return os.path.samefile(first, second)
    except OSError:
        return False


def serves(engine, pair, run=subprocess.run):
    """Whether engine lists `praetorctl hook <client> <event>` among the pairs it serves."""
    try:
        result = run([engine, "hook"], stdin=subprocess.DEVNULL, capture_output=True,
                     timeout=PROBE_TIMEOUT, check=False)
    except (OSError, subprocess.SubprocessError):
        return False
    listing = result.stdout[:PROBE_LIMIT] + b"\n" + result.stderr[:PROBE_LIMIT]
    wanted = "praetorctl hook " + " ".join(pair)
    return any(line.strip() == wanted for line in listing.decode("utf-8", "replace").splitlines())


def serve(engine, pair, run=subprocess.run):
    """Run the engine on the inherited streams and return its exit code unchanged."""
    try:
        return run([engine, "hook", *pair], timeout=RUN_TIMEOUT, check=False).returncode
    except subprocess.TimeoutExpired:
        reason = f"{engine} did not answer within {RUN_TIMEOUT} s"
    except OSError as error:
        reason = f"{engine} could not start: {error}"
    sys.stderr.write("praetor hook: " + reason[:1000] + "\n")
    # The engine gave no verdict. Exit 1 is a fault the native clients report without
    # blocking; AGY gets its allow answer instead, because no exit code is documented for it.
    return proceed(pair, 1)


def proceed(pair, code):
    """Let the client go on without a verdict: print the answer it needs, if it needs one."""
    answer = PROCEED.get(tuple(pair))
    if answer is None:
        return code
    sys.stdout.write(answer)  # caveman:not-applicable structured-protocol
    sys.stdout.flush()
    return 0


def read_stdin(limit):
    """Read the payload from descriptor 0, never through sys.stdin.buffer.

    A reader still blocked when the drain gives up would hold the buffer's lock while the
    interpreter shuts down, and CPython then aborts the process (exit 134) instead of
    exiting 0. os.read holds no such lock."""
    return os.read(0, limit)


def drain(read, limit=DRAIN_LIMIT, timeout=DRAIN_TIMEOUT):
    """Consume the payload no engine will read, so the client's write never breaks a pipe.

    read(n) returns at most n bytes, and b"" at the end of the input. The reader is a daemon
    thread, so a client that holds stdin open past the timeout cannot hold the launcher."""
    def consume():
        remaining = limit
        for _ in range(limit):  # every read returns at least one byte before the end
            try:
                chunk = read(min(remaining, DRAIN_CHUNK))
            except (OSError, ValueError):
                return
            remaining -= len(chunk)
            if not chunk or remaining <= 0:
                return

    reader = threading.Thread(target=consume, daemon=True)
    reader.start()
    reader.join(timeout)


def skip(pair, engines):
    checked = "checked " + ", ".join(engines) if engines else "no praetorctl is built or installed"
    sys.stderr.write(
        f"praetor hook: no engine serves {' '.join(pair)} ({checked}); gate unenforced "
        "until bin/praetorctl rebuilt (make hook-cli) or engine reinstalled "
        "(make dev-install), skipped\n")
    return proceed(pair, 0)


def well_formed(argv):
    """The engine's argument grammar; an agy event also needs a known answer shape."""
    if len(argv) != 2 or not all(ARGUMENT.fullmatch(value) for value in argv):
        return False
    return argv[0] != "agy" or tuple(argv) in PROCEED


def main(argv, root=ROOT, which=shutil.which, run=subprocess.run, stdin=None):
    if not well_formed(argv):
        # A malformed registration is not skew; it fails closed, as the engine does.
        sys.stderr.write("praetor hook: usage: praetor_hook.py <client> <event>\n")
        return 2
    engines = candidates(root, which)
    for engine in engines:
        if serves(engine, argv, run):
            return serve(engine, argv, run)
    drain(stdin.read if stdin is not None else read_stdin)
    return skip(argv, engines)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
