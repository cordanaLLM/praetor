#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
"""Vet every Go module of the tree for Windows and macOS from any host (#1026).

`make verify-all` built and vetted only for the host, so a Windows- or macOS-only compile
break (a test file calling syscall.Mkfifo without a build constraint) surfaced only on the
hosted Platform Neutrality legs an hour later. `go vet` compiles test files too, and cross
compiling needs no target machine, so this runs the same way on Linux, macOS and Windows.

cgo is off for the cross targets: a cgo package cannot cross-compile without a target C
toolchain, and the tree builds without cgo. A package that needs cgo for a target gets a
build constraint, never a skip here.
"""

import argparse
import os
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TARGETS = (("windows", "amd64"), ("darwin", "arm64"))
# One vet of the whole main module for one target takes about a minute on a workstation;
# the bound keeps a hung toolchain from holding verify-all (HISS-02).
TIMEOUT_SECONDS = 900
SKIPPED_DIRS = {".git", "node_modules", "testdata", "vendor"}
MAX_MODULES = 64
OUTPUT_LINES = 40


@dataclass
class Result:
    ok: bool
    output: str


def modules(root):
    """Return the directories of root that hold a go.mod, outside testdata, vendor and .git."""
    found = []
    for directory, subdirs, files in os.walk(root):
        subdirs[:] = sorted(d for d in subdirs if d not in SKIPPED_DIRS)
        if "go.mod" in files:
            found.append(Path(directory))
            if len(found) > MAX_MODULES:
                raise SystemExit(f"cross-vet: more than {MAX_MODULES} Go modules under {root}")
    return found


def vet_module(module, goos, goarch):
    """Run go vet ./... in module for goos/goarch and return whether it passed, with output."""
    env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0", GOFLAGS="-mod=readonly")
    try:
        completed = subprocess.run(["go", "vet", "./..."], cwd=module, env=env, capture_output=True,
                                   text=True, timeout=TIMEOUT_SECONDS, check=False)
    except subprocess.TimeoutExpired:
        return Result(False, f"go vet did not finish within {TIMEOUT_SECONDS} seconds")
    output = (completed.stdout + completed.stderr).strip()
    if completed.returncode != 0 and "matched no packages" in output:
        return Result(True, output)
    return Result(completed.returncode == 0, output)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--module", action="append", type=Path,
                        help="vet only this module directory (repeatable); default: every module of the tree")
    args = parser.parse_args(argv)
    targets = args.module or modules(ROOT)
    failed = 0
    for module in targets:
        name = os.path.relpath(module, ROOT) if module.is_relative_to(ROOT) else str(module)
        for goos, goarch in TARGETS:
            result = vet_module(module, goos, goarch)
            if result.ok:
                print(f"[PASS] cross-vet {goos}/{goarch}: {name}")
                continue
            failed += 1
            print(f"[FAIL] cross-vet {goos}/{goarch}: {name}")
            for line in result.output.splitlines()[:OUTPUT_LINES]:
                print(f"       {line}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
