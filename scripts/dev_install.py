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


def hook_interpreter_spelling():
    """Return (variable, default): the one spelling the Git hooks read their interpreter from.

    It is declared once, in the hook policy (toolchain.RESOLVED, applied by
    .config/lefthook/python.sh), and read from there instead of being repeated here.
    """
    return hook_toolchain().RESOLVED["python"]


def store_user_variable(variable, value, default, platform=sys.platform, run=subprocess.run):
    """Store variable=value for the user's later processes, where the platform keeps such a store.

    Windows has per-user environment variables, written with setx and read by every process
    started afterwards, Git hooks included. No other platform has one store every shell
    reads, so there the install fails with the line to add rather than guessing a profile.
    """
    if platform != "win32":
        raise RuntimeError(
            f"{default} is not on PATH, so the Git hooks cannot start; this platform has no "
            f"per-user environment store, so export {variable}={value} in the shell profile "
            "that starts git and run the install again")
    result = run(["setx", variable, value], stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                 timeout=SETX_TIMEOUT, check=False)
    if result.returncode != 0:
        raise RuntimeError(f"setx {variable} failed: {result.stderr.decode(errors='replace').strip()}")


def settle_hook_interpreter(environ=None, which=shutil.which, store=store_user_variable):
    """Make sure the Git hooks can start their interpreter on this host, and report how.

    Every hook starts through .config/lefthook/python.sh, which runs the interpreter the
    variable names, or the default name where it is unset or empty. A set variable is the
    operator's choice: it is checked, never replaced. Where neither resolves -- a stock
    Windows install has python.exe and no python3 -- the interpreter running this install
    is stored as the user's variable, which a shell opened afterwards reads.
    """
    environ = os.environ if environ is None else environ
    variable, default = hook_interpreter_spelling()
    configured = environ.get(variable, "")
    if configured:
        if which(configured) is None:
            raise RuntimeError(f"{variable}={configured} names no program on PATH; unset it "
                               "or point it at a Python 3 interpreter")
        return {"variable": variable, "value": configured, "source": "environment"}
    if which(default) is not None:
        return {"variable": variable, "value": default, "source": "default"}
    store(variable, sys.executable, default)
    return {"variable": variable, "value": sys.executable, "source": "stored"}


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
