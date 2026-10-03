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


def hook_interpreter(which=shutil.which):
    """Return the interpreter the Git hooks start on this host, or stop the install.

    Every hook starts through .config/lefthook/python.sh, which tries a fixed list of
    candidates and runs the first that proves itself: it must answer a version probe as
    Python 3 at or above the supported floor. A name that merely resolves on PATH is not
    proof. Windows 10 and 11 put python3.exe and python.exe there as Microsoft Store aliases,
    which a lookup finds and which answer the probe with "Python was not found".

    The check here is the hook policy's own resolver for that list (toolchain.python_program),
    so the install reports what the hooks will run and refuses a host where every hook would
    fail as a missing dependency. Nothing is stored: no environment variable selects the
    interpreter, so there is none to set.
    """
    toolchain = hook_toolchain()
    try:
        argv, version = toolchain.python_program(which)
    except toolchain.HookError as error:
        raise RuntimeError(f"the Git hooks cannot start on this host: {error}") from error
    return {"command": argv, "version": version}


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
    interpreter = hook_interpreter()
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
