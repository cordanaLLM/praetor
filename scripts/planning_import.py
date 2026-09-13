#!/usr/bin/env python3
"""Import a bounded, hashed planning bundle into private .workingdir state."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import sys

sys.dont_write_bytecode = True

MAX_ENTRIES = 128
MAX_FILE_BYTES = 4 * 1024 * 1024
MAX_TOTAL_BYTES = 8 * 1024 * 1024
MAX_MANIFEST_BYTES = 256 * 1024
READ_CHUNK_BYTES = 64 * 1024


class ImportErrorValue(ValueError):
    """A caller supplied an invalid planning import."""


def reject_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ImportErrorValue(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def read_manifest(path):
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
        if not stat.S_ISREG(os.fstat(descriptor).st_mode):
            os.close(descriptor)
            raise ImportErrorValue("manifest must be a regular file")
        with os.fdopen(descriptor, "rb") as source:
            content = source.read(MAX_MANIFEST_BYTES + 1)
        if len(content) > MAX_MANIFEST_BYTES:
            raise ImportErrorValue("manifest exceeds 256 KiB")
        return json.loads(content.decode("utf-8"), object_pairs_hook=reject_duplicates)
    except (OSError, UnicodeError, json.JSONDecodeError, ImportErrorValue) as exc:
        raise ImportErrorValue(f"read manifest: {exc}") from exc


def relative_path(value, label, allow_subdirs=False):
    if not isinstance(value, str) or not value or "\\" in value or "\x00" in value:
        raise ImportErrorValue(f"{label} must be a relative path")
    raw_parts = value.split("/")
    if len(raw_parts) > 32 or any(part in ("", ".", "..") for part in raw_parts):
        raise ImportErrorValue(f"{label} escapes its root")
    path = Path(value)
    if path.is_absolute():
        raise ImportErrorValue(f"{label} escapes its root")
    if not allow_subdirs and len(path.parts) != 1:
        raise ImportErrorValue(f"{label} must name a top-level file")
    return path


def read_source(path):
    size = 0
    digest = hashlib.sha256()
    content = bytearray()
    descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
    if not stat.S_ISREG(os.fstat(descriptor).st_mode):
        os.close(descriptor)
        raise ImportErrorValue(f"source is not a regular file: {path.name}")
    with os.fdopen(descriptor, "rb") as source:
        for _ in range(MAX_FILE_BYTES // READ_CHUNK_BYTES + 1):
            chunk = source.read(READ_CHUNK_BYTES)
            if not chunk:
                break
            size += len(chunk)
            if size > MAX_FILE_BYTES:
                raise ImportErrorValue(f"source file exceeds {MAX_FILE_BYTES} bytes: {path.name}")
            content.extend(chunk)
            digest.update(chunk)
        else:
            raise ImportErrorValue("source read iteration budget exhausted")
    return bytes(content), size, digest.hexdigest()


def load_entries(manifest, source):
    if (not isinstance(manifest, dict) or set(manifest) != {"version", "entries"}
            or type(manifest.get("version")) is not int or manifest["version"] != 1):
        raise ImportErrorValue("manifest version must be 1")
    entries = manifest.get("entries")
    if not isinstance(entries, list) or not entries or len(entries) > MAX_ENTRIES:
        raise ImportErrorValue(f"manifest entries must contain 1..{MAX_ENTRIES} items")
    source_names = set()
    with os.scandir(source) as listing:
        for index, item in enumerate(listing):
            if index >= MAX_ENTRIES + 1:
                raise ImportErrorValue("source directory exceeds 129 entries")
            source_names.add(item.name)
    seen_sources = set()
    seen_destinations = set()
    planned = []
    total = 0
    for item in entries:
        if not isinstance(item, dict) or set(item) != {"source", "destination", "sha256"}:
            raise ImportErrorValue("each entry requires exactly source, destination, sha256")
        source_rel = relative_path(item["source"], "source")
        destination = relative_path(item["destination"], "destination", allow_subdirs=True)
        if destination.parts[0] == "report.json":
            raise ImportErrorValue("destination is reserved: report.json")
        digest = item["sha256"]
        if not isinstance(digest, str) or len(digest) != 64 or any(c not in "0123456789abcdef" for c in digest):
            raise ImportErrorValue(f"invalid SHA-256 for {source_rel}")
        if source_rel.name in seen_sources or destination.as_posix() in seen_destinations:
            raise ImportErrorValue("duplicate source or destination")
        if any(destination.as_posix().startswith(previous + "/")
               or previous.startswith(destination.as_posix() + "/")
               for previous in seen_destinations):
            raise ImportErrorValue("destination path collision")
        seen_sources.add(source_rel.name)
        seen_destinations.add(destination.as_posix())
        path = source / source_rel
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode):
            raise ImportErrorValue(f"source is not a regular file: {source_rel}")
        content, size, actual = read_source(path)
        if actual != digest:
            raise ImportErrorValue(f"digest mismatch: {source_rel}")
        total += size
        if total > MAX_TOTAL_BYTES:
            raise ImportErrorValue(f"source bundle exceeds {MAX_TOTAL_BYTES} bytes")
        planned.append((content, destination, size, actual))
    if seen_sources != source_names:
        missing = sorted(source_names - seen_sources)
        unknown = sorted(seen_sources - source_names)
        raise ImportErrorValue(f"source inventory mismatch: missing={missing} unknown={unknown}")
    return planned, total


def validate_output(output, private_root):
    if output.exists() or output.is_symlink():
        raise ImportErrorValue(f"output already exists: {output}")
    parent = output.parent
    if not parent.is_dir() or parent.is_symlink():
        raise ImportErrorValue("output parent must be an existing real directory")
    if not private_root.is_dir() or private_root.is_symlink():
        raise ImportErrorValue(".workingdir must be an existing real directory")
    private = private_root.resolve()
    parent_real = parent.resolve()
    try:
        output.relative_to(private)
    except ValueError as exc:
        raise ImportErrorValue("output must be under .workingdir") from exc
    if not parent_real.is_relative_to(private):
        raise ImportErrorValue("output parent escapes .workingdir")


def reject_symlink_ancestors(path):
    current = Path(path.anchor or "/")
    for part in path.parts[1:]:
        current /= part
        if current.is_symlink():
            raise ImportErrorValue(f"symlink path component is forbidden: {current}")


def write_checked(path, content, expected_size, expected_digest):
    actual = hashlib.sha256(content).hexdigest()
    if len(content) != expected_size or actual != expected_digest:
        raise ImportErrorValue("invalid preflight payload")
    target_fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(target_fd, "wb") as target:
        target.write(content)


def rollback(output):
    try:
        shutil.rmtree(output)
    except OSError as exc:
        raise ImportErrorValue(f"import rollback failed: {exc}") from exc


def import_plan(manifest_path, source_path, output, dry_run=False):
    paths = [Path(manifest_path), Path(source_path), Path(output)]
    if any(".." in path.parts for path in paths):
        raise ImportErrorValue("explicit roots must not contain parent traversal")
    manifest_path, source_path, output = [path.absolute() for path in paths]
    reject_symlink_ancestors(manifest_path)
    reject_symlink_ancestors(source_path.absolute())
    reject_symlink_ancestors(output.absolute().parent)
    if source_path.is_symlink():
        raise ImportErrorValue("source must be an existing real directory")
    source = source_path.resolve()
    if not source.is_dir() or source.is_symlink():
        raise ImportErrorValue("source must be an existing real directory")
    private_root = next((item for item in output.parents if item.name == ".workingdir"), None)
    if private_root is None:
        raise ImportErrorValue("output must be under .workingdir")
    validate_output(output, private_root)
    source_real = source.resolve()
    output_real = output.resolve()
    if source_real == output_real or source_real in output_real.parents or output_real in source_real.parents:
        raise ImportErrorValue("source and output must be disjoint")
    try:
        planned, total = load_entries(read_manifest(manifest_path), source)
    except OSError as exc:
        raise ImportErrorValue(f"preflight source: {exc}") from exc
    report = {"schema_version": 1, "verified": False, "untrusted": True, "dry_run": dry_run,
              "entries": len(planned), "bytes": total, "output": str(output)}
    if dry_run:
        return report
    created = False
    try:
        output.mkdir(mode=0o700)
        created = True
        for content, destination, expected_size, expected_digest in planned:
            target = output / destination
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            write_checked(target, content, expected_size, expected_digest)
            os.chmod(target, 0o600)
        (output / "report.json").write_text(json.dumps(report, sort_keys=True) + "\n", encoding="utf-8")
        os.chmod(output / "report.json", 0o600)
    except (OSError, ValueError) as exc:
        if created:
            try:
                rollback(output)
            except ImportErrorValue as rollback_error:
                raise ImportErrorValue(f"{exc}; {rollback_error}") from exc
        raise ImportErrorValue(f"import rolled back: {exc}") from exc
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args(argv)
    try:
        report = import_plan(args.manifest, args.source, args.output, args.dry_run)
    except ImportErrorValue as exc:
        parser.error(str(exc))
    print(json.dumps(report, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
