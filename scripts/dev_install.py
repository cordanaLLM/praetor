#!/usr/bin/env python3
"""Build and install this checkout's local binaries, retaining previous executables."""

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True

import dev_mcp
from dev_mcp_probe import probe

COMMANDS = {
    "praetorctl": "standardsctl",
    "praetor-mcp": "standards-mcp",
    "praetor-lsp": "standards-lsp",
}
MANIFEST = ".praetor-dev-install.json"


@contextmanager
def installation_lock(bin_dir):
    """Serialize cooperative installers; a killed process leaves a visible lock."""
    lock = bin_dir / ".praetor-dev-install.lock"
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError as error:
        raise RuntimeError(f"Installation lock exists: {lock}; check for an active installer before recovery") from error
    try:
        yield
    finally:
        lock.rmdir()


def file_state(path):
    """Reject special files and foreign links before touching an installation."""
    if path.is_symlink():
        target = os.readlink(path)
        expected = {alias: name for name, alias in COMMANDS.items()}.get(path.name)
        if target != expected:
            raise RuntimeError(f"Refusing unexpected installation symlink: {path}")
        return {"kind": "symlink", "target": target}
    if not path.exists():
        return {"kind": "absent"}
    if not stat.S_ISREG(path.stat().st_mode):
        raise RuntimeError(f"Refusing non-regular installation target: {path}")
    return {"kind": "file", "mode": stat.S_IMODE(path.stat().st_mode),
            "sha256": hashlib.sha256(dev_mcp.read_bounded(path)).hexdigest()}


def replace_from(source, destination):
    """Stage beside the destination so replacing an in-use executable is atomic."""
    with tempfile.TemporaryDirectory(prefix=".praetor-install-", dir=destination.parent) as temporary:
        staged = Path(temporary) / destination.name
        shutil.copy2(source, staged, follow_symlinks=False)
        os.replace(staged, destination)


def prepare_backup(bin_dir, backup_root, names):
    previous = {name: file_state(bin_dir / name) for name in names}
    backup_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    backup = Path(tempfile.mkdtemp(prefix="install-", dir=backup_root))
    for name, info in previous.items():
        if info["kind"] != "absent":
            shutil.copy2(bin_dir / name, backup / name, follow_symlinks=False)
        if file_state(bin_dir / name) != info:
            raise RuntimeError(f"Installation changed during backup: {name}; evidence: {backup}")
    (backup / "previous.json").write_text(json.dumps(previous, indent=2) + "\n")
    return backup, previous


def restore(backup, bin_dir, previous, changed):
    failures = []
    for name in reversed(changed):
        try:
            if previous[name]["kind"] == "absent":
                (bin_dir / name).unlink(missing_ok=True)
            else:
                replace_from(backup / name, bin_dir / name)
        except OSError as error:
            failures.append(f"{name}: {error}")
    return failures


def install_files(staging, bin_dir, backup_root, metadata):
    """Install exactly three binaries and their aliases; preserve rollback evidence."""
    bin_dir.mkdir(parents=True, exist_ok=True)
    with installation_lock(bin_dir):
        return install_locked(staging, bin_dir, backup_root, metadata)


def install_locked(staging, bin_dir, backup_root, metadata):
    """Caller holds the destination lock for backup, replacement, and rollback."""
    names = [item for pair in COMMANDS.items() for item in pair] + [MANIFEST]
    backup, previous = prepare_backup(bin_dir, backup_root, names)
    report = dict(metadata, bin_dir=str(bin_dir), backup_dir=str(backup), binaries={})
    for name, alias in COMMANDS.items():
        mode = stat.S_IMODE((staging / name).stat().st_mode) & 0o755
        for target in (name, alias):
            if previous[target]["kind"] == "file":
                mode &= previous[target]["mode"]
        if not mode & 0o111:
            raise RuntimeError(f"Existing permissions prevent executable installation: {name}; backup: {backup}")
        (staging / name).chmod(mode)
        report["binaries"][name] = hashlib.sha256(dev_mcp.read_bounded(staging / name)).hexdigest()
        (staging / alias).symlink_to(name)
    manifest = staging / MANIFEST
    manifest.write_text(json.dumps(report, indent=2) + "\n")
    manifest.chmod(0o600 & previous[MANIFEST].get("mode", 0o600))
    changed = []
    try:
        for name in names:
            if file_state(bin_dir / name) != previous[name]:
                raise RuntimeError(f"Installation changed before replacement: {name}")
            changed.append(name)
            replace_from(staging / name, bin_dir / name)
        for name, expected in report["binaries"].items():
            if file_state(bin_dir / name).get("sha256") != expected:
                raise RuntimeError(f"Installed binary readback mismatch: {name}")
    except (OSError, RuntimeError) as error:
        failures = restore(backup, bin_dir, previous, changed)
        raise RuntimeError(f"Installation failed: {error}; rollback errors: {failures}; backup: {backup}") from error
    return report


def build(staging):
    binary, metadata = dev_mcp.build(staging)
    for name, package in COMMANDS.items():
        if name != "praetor-mcp":
            dev_mcp.command_output(["go", "build", "-o", str(staging / name),
                                    "./cmd/" + package], dev_mcp.BUILD_TIMEOUT)
    if dev_mcp.source_hash() != metadata["source_sha256"]:
        raise RuntimeError("Go sources changed during installation build; rerun")
    metadata["checks"] = probe(binary, dev_mcp.ROOT, metadata)["passed"]
    if dev_mcp.source_hash() != metadata["source_sha256"]:
        raise RuntimeError("Go sources changed during installation probe; rerun")
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin-dir", type=Path, default=Path.home() / ".local/bin")
    parser.add_argument("--backup-dir", type=Path,
                        default=Path.home() / ".local/state/praetor/dev-installs")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="praetor-dev-install-") as temporary:
        staging = Path(temporary)
        metadata = build(staging)
        report = install_files(staging, args.bin_dir.resolve(), args.backup_dir.resolve(), metadata)
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Development install failed: {error}", file=sys.stderr)
        sys.exit(1)
