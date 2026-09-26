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

1. `bin/praetorctl` of the tree this file belongs to, built from that tree by
   `make hook-cli` and kept current by the Git hooks, so it serves the rows the tree
   registers;
2. `praetorctl` from PATH, the installed engine.

When no candidate serves the pair, or none exists, the call is a stated skip: exit 0 and the
reason on stderr, the same shape the engine gives an event newer than itself. The gate is
then not enforced, and the reason says so. A version-skewed or missing engine never blocks
the client. docs/guides/agent-hooks.md describes the rollout.
"""

import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import threading

ROOT = Path(__file__).resolve().parents[3]
# The engine's own argument grammar (internal/agenthook argumentShape).
ARGUMENT = re.compile(r"[a-z-]+")
SUFFIX = ".exe" if os.name == "nt" else ""
PROBE_TIMEOUT = 5
PROBE_LIMIT = 64 * 1024
# The longest registration timeout of a launcher row. Every engine budget ends earlier, and
# the client's own timeout ends a shorter row first.
RUN_TIMEOUT = 60
# The engine reads at most 1 MiB + 1 byte of payload (agenthook.MaxInputBytes).
DRAIN_LIMIT = 1024 * 1024 + 1
DRAIN_TIMEOUT = 2


def candidates(root, which=shutil.which):
    """Existing engines in preference order, each path once."""
    found = []
    local = root / "bin" / ("praetorctl" + SUFFIX)
    if local.is_file():
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
    # Exit 1 is a fault every client reports without blocking: the engine gave no verdict.
    sys.stderr.write("praetor hook: " + reason[:1000] + "\n")
    return 1


def drain(stream, limit=DRAIN_LIMIT, timeout=DRAIN_TIMEOUT):
    """Consume the payload no engine will read, so the client's write never breaks a pipe."""
    reader = threading.Thread(target=stream.read, args=(limit,), daemon=True)
    reader.start()
    reader.join(timeout)


def skip(pair, engines):
    checked = "checked " + ", ".join(engines) if engines else "no praetorctl is built or installed"
    sys.stderr.write(
        f"praetor hook: no engine serves {' '.join(pair)} ({checked}); the gate is not enforced "
        "until bin/praetorctl is rebuilt (make hook-cli) or the engine is reinstalled "
        "(make dev-install), skipped\n")
    return 0


def main(argv, root=ROOT, which=shutil.which, run=subprocess.run, stdin=None):
    if len(argv) != 2 or not all(ARGUMENT.fullmatch(value) for value in argv):
        # A malformed registration is not skew; it fails closed, as the engine does.
        sys.stderr.write("praetor hook: usage: praetor_hook.py <client> <event>\n")
        return 2
    engines = candidates(root, which)
    for engine in engines:
        if serves(engine, argv, run):
            return serve(engine, argv, run)
    drain(stdin if stdin is not None else sys.stdin.buffer)
    return skip(argv, engines)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
