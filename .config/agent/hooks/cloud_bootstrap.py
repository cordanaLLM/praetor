#!/usr/bin/env python3
"""Provision a cloud session with the two tools the engine's hooks need: lefthook and praetorctl.

Claude Code runs this at SessionStart. Outside a cloud session (CLAUDE_CODE_REMOTE is not "true") it does nothing.
In a cloud VM, which has neither tool, the before-tool guards and the stop checkpoint fail closed; this installs the
lefthook pin from tools/lefthook/go.mod, the module CI installs it from, and builds praetorctl from ./cmd/standardsctl
of this checkout, as the Makefile does, into a directory on the agent's PATH. Both builds run with -mod=readonly, so
every module is verified against a go.sum this checkout holds and none needs the checksum database, which a cloud VM
that clones from GitHub cannot reach (measured 2026-10-19: "sum.golang.org forbidden"). The bootstrap refuses to run
while checksum verification is switched off. It never disables a hook: a step that fails is reported and the guards
stay closed.
"""

import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
# The module that pins lefthook, and the package it builds; .github/workflows/ci.yml installs from the same module.
LEFTHOOK_MODULE = "tools/lefthook"
LEFTHOOK = "github.com/evilmartians/lefthook/v2"
# Claude Code registers this hook with a 600 s timeout (.claude/settings.json). The steps below share
# BOOTSTRAP_BUDGET, a step that overruns is killed within BOOTSTRAP_STOP, and BOOTSTRAP_LIMIT stays under the
# registration; scripts/test_checkpoint_hooks.py checks this arithmetic against the registration.
ENV_TIMEOUT = 10
INSTALL_TIMEOUT = 180
BUILD_TIMEOUT = 280
STATE_TIMEOUT = 30
BOOTSTRAP_BUDGET = 3 * ENV_TIMEOUT + INSTALL_TIMEOUT + BUILD_TIMEOUT + STATE_TIMEOUT
BOOTSTRAP_STOP = 10
BOOTSTRAP_LIMIT = 560
TARGETS = (Path("/usr/local/bin"), Path.home() / ".local" / "bin")


def target_dir(path_value):
    """The first writable install directory that is on the agent's PATH, or None."""
    on_path = {Path(entry) for entry in path_value.split(os.pathsep) if entry}
    for candidate in TARGETS:
        if candidate in on_path and candidate.is_dir() and os.access(candidate, os.W_OK):
            return candidate
    return None


def go_env(name):
    """One `go env` value, or "" when go cannot answer."""
    try:
        result = subprocess.run(["go", "env", name], capture_output=True, text=True, timeout=ENV_TIMEOUT,
                                check=False)
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return result.stdout.strip()


def verification_off():
    """Whether module checksum verification is switched off for any module."""
    return go_env("GOSUMDB") == "off" or bool(go_env("GONOSUMDB")) or bool(go_env("GOINSECURE"))


def run_step(name, command, timeout):
    """Run one named step in the checkout; "" on success, else one line naming the failure."""
    try:
        result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        return f"{name} failed: {error}"
    if result.returncode == 0:
        return ""
    return f"{name} exit {result.returncode}: {result.stderr.strip()[-300:]}"


def provision(bindir):
    """Build whichever tool is missing; the failure lines of the steps that ran."""
    failures = []
    if shutil.which("lefthook") is None:
        failures.append(run_step("lefthook build", ["go", "build", "-C", LEFTHOOK_MODULE, "-mod=readonly", "-o",
                                                    str(bindir / "lefthook"), LEFTHOOK], INSTALL_TIMEOUT))
    if shutil.which("praetorctl") is None:
        failures.append(run_step("praetorctl build", ["go", "build", "-mod=readonly", "-o",
                                                      str(bindir / "praetorctl"), "./cmd/standardsctl"],
                                 BUILD_TIMEOUT))
    return [line for line in failures if line]


def bootstrap(environ):
    """The one status line for the session, or None outside a cloud session."""
    if environ.get("CLAUDE_CODE_REMOTE") != "true":
        return None
    if shutil.which("go") is None:
        return "no go toolchain; the engine hooks stay closed"
    if verification_off():
        return "module checksum verification is off; refusing to install, the engine hooks stay closed"
    bindir = target_dir(environ.get("PATH", ""))
    if bindir is None:
        return "no writable install directory on PATH; the engine hooks stay closed"
    failures = provision(bindir)
    found = {name: shutil.which(name) for name in ("lefthook", "praetorctl")}
    missing = [name for name, path in found.items() if path is None]
    if not missing:
        # A fresh clone has no private state ledger, and the stop checkpoint refuses an unverified one (measured
        # 2026-10-19: "lstat .workingdir: no such file"); --if-absent never repairs a partial ledger.
        failures.append(run_step("state init", [found["praetorctl"], "state", "init", "--if-absent", "."],
                                 STATE_TIMEOUT))
    state = "ready" if not missing else "missing " + ", ".join(missing) + "; the engine hooks stay closed"
    return "; ".join([f"{state} in {bindir}", *[line for line in failures if line]])


def main(environ=os.environ):
    line = bootstrap(environ)
    if line is not None:
        sys.stdout.write(f"Praetor cloud session bootstrap: {line}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
