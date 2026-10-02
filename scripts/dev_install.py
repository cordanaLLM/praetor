#!/usr/bin/env python3
"""Probe the MCP server, then install this checkout's binaries through the Go engine."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True

import dev_mcp
from dev_mcp_probe import probe
from portability_selftest import hook_toolchain

# The atomic install itself -- lock, backup, swap, manifest -- is
# internal/workstation (HISS-19: two installers never coexist). This script keeps only
# the MCP functional probe as a pre-flight gate and the subprocess call that reaches the
# Go engine.
INSTALL_TIMEOUT = 300
# setx writes one registry value and returns.
SETX_TIMEOUT = 30
# What a failed start printed is quoted in one line of the fault; the rest is cut.
MAX_FAULT_DETAIL = 240


def hook_interpreter_spelling():
    """Return (variable, default): the one spelling the Git hooks read their interpreter from.

    It is declared once, in the hook policy (toolchain.RESOLVED, applied by
    .config/lefthook/python.sh), and read from there instead of being repeated here.
    """
    return hook_toolchain().RESOLVED["python"]


def interpreter_fault(program, which=shutil.which):
    """Return why program cannot run the Git hooks, or None when it starts as Python 3.

    A name that resolves on PATH is not yet an interpreter, so the file PATH resolves it to
    is started (toolchain.interpreter_version, one bounded `-V`). Windows 10 and 11 put a
    python3.exe on the user's PATH that is the Microsoft Store alias: a lookup finds it, and
    a hook that starts it gets "Python was not found" and a nonzero status.
    """
    path = which(program)
    if path is None:
        # A value with a directory part is a path, which no PATH lookup resolves.
        missing = "is not an executable file" if os.path.dirname(program) else "is not on PATH"
        return f"{program} {missing}"
    toolchain = hook_toolchain()
    try:
        toolchain.interpreter_version(path)
    except toolchain.HookError as error:
        detail = " ".join(str(error).split())[:MAX_FAULT_DETAIL]
        return f"{program} does not start as Python 3 ({detail})"
    return None


def store_user_variable(variable, value, fault, platform=sys.platform, run=subprocess.run):
    """Store variable=value for the user's later processes, where the platform keeps such a store.

    Windows has per-user environment variables, written with setx and read by every process
    started afterwards, Git hooks included. No other platform has one store every shell
    reads, so there the install fails with fault and the line to add rather than guessing a
    profile.
    """
    if platform != "win32":
        raise RuntimeError(
            f"{fault}, so the Git hooks cannot start; this platform has no "
            f"per-user environment store, so export {variable}={value} in the shell profile "
            "that starts git and run the install again")
    result = run(["setx", variable, value], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                 timeout=SETX_TIMEOUT, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"setx {variable} failed: {result.stderr.decode(errors='replace').strip()}")


def settle_hook_interpreter(environ=None, fault=interpreter_fault, store=store_user_variable):
    """Make sure the Git hooks can start their interpreter on this host, and report how.

    Every hook starts through .config/lefthook/python.sh, which runs the interpreter the
    variable names, or the default name where it is unset or empty. Either one is started
    here, not only looked up (interpreter_fault). A set variable is the operator's choice:
    it is checked, never replaced. Where the default name does not start as Python 3 -- a
    Windows host has no python3, or only the Microsoft Store alias of that name -- the
    interpreter running this install is stored as the user's variable, which a shell opened
    afterwards reads, and the report carries the reason.
    """
    environ = os.environ if environ is None else environ
    variable, default = hook_interpreter_spelling()
    configured = environ.get(variable, "")
    if configured:
        reason = fault(configured)
        if reason is not None:
            raise RuntimeError(f"{variable} names an interpreter the Git hooks cannot run: "
                               f"{reason}; unset it or point it at a Python 3 interpreter")
        return {"variable": variable, "value": configured, "source": "environment"}
    reason = fault(default)
    if reason is None:
        return {"variable": variable, "value": default, "source": "default"}
    store(variable, sys.executable, reason)
    return {"variable": variable, "value": sys.executable, "source": "stored", "reason": reason}


def standardsctl_argv():
    """The engine command install delegates to. PRAETOR_STANDARDSCTL substitutes a stub
    binary for tests, so they assert on what the stub recorded rather than running a real
    `go build` (and never touch a real bin directory in the process)."""
    override = os.environ.get("PRAETOR_STANDARDSCTL")
    if override:
        return [override]
    return ["go", "run", "./cmd/standardsctl"]


def probe_mcp(staging):
    """Build praetor-mcp and run its functional probe before the engine installs anything.

    This is a sanity gate distinct from `workstation install`'s own build: it proves the
    MCP server actually answers before spending time on the atomic install, and it fails
    loudly (never silently) if the source tree changed under it mid-build.
    """
    binary, metadata = dev_mcp.build(staging)
    source = metadata["source_sha256"]
    if dev_mcp.source_hash() != source:
        raise RuntimeError("Go sources changed during the MCP probe build; rerun")
    metadata["checks"] = probe(binary, dev_mcp.ROOT, metadata)["passed"]
    if dev_mcp.source_hash() != source:
        raise RuntimeError("Go sources changed during the MCP probe; rerun")
    return metadata


def install(bin_dir, manifest_path=None):
    """Delegate the atomic install to `workstation install` and return its JSON report."""
    command = standardsctl_argv() + [
        "workstation", "install", "--source", str(dev_mcp.ROOT), "--bin-dir", str(bin_dir),
    ]
    if manifest_path is not None:
        command += ["--manifest", str(manifest_path)]
    result = subprocess.run(command, cwd=dev_mcp.ROOT, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=INSTALL_TIMEOUT, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"workstation install failed: {result.stderr.decode(errors='replace').strip()}")
    return json.loads(result.stdout.decode())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin-dir", type=Path, default=Path.home() / ".local/bin")
    parser.add_argument("--manifest", type=Path, default=None,
                        help="Install manifest path (default: the per-user configuration directory)")
    args = parser.parse_args()
    # Before anything is installed: a host whose hooks cannot start is not a working install.
    interpreter = settle_hook_interpreter()
    with tempfile.TemporaryDirectory(prefix="praetor-dev-install-") as temporary:
        probe_mcp(Path(temporary))
    report = install(args.bin_dir.resolve(), args.manifest)
    report["hook_interpreter"] = interpreter
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        print(f"Development install failed: {error}", file=sys.stderr)
        sys.exit(1)
