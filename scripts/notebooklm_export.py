#!/usr/bin/env python3
"""Read a personal NotebookLM notebook into a private Praetor snapshot.

Uses notebooklm-py 0.8.2's public client API (an unofficial Google transport).
No notebook writes, model calls, browser-cookie extraction, or automatic login.
"""
import argparse
import asyncio
from datetime import datetime, timezone
import hashlib
from importlib.metadata import version
import json
import logging
import os
from pathlib import Path
import sys
from urllib.parse import urlparse
from uuid import UUID

CLIENT_VERSION = "0.8.2"
MAX_SOURCES = 64
MAX_SOURCE_BYTES = 128 * 1024
MAX_BUNDLE_BYTES = 1024 * 1024


def notebook_id(value):
    if value.startswith("https://"):
        url = urlparse(value)
        if url.hostname not in {"notebook.google.com", "notebooklm.google.com"}:
            raise ValueError("expected a personal NotebookLM URL")
        if url.username or url.password or not url.path.startswith("/notebook/"):
            raise ValueError("invalid NotebookLM URL")
        value = url.path.removeprefix("/notebook/").rstrip("/")
    return str(UUID(value))


def source_record(identity, title, role, locator, content):
    if not isinstance(content, str) or not content.strip():
        raise ValueError("source has no readable text")
    raw = content.encode("utf-8")
    if len(raw) > MAX_SOURCE_BYTES or "\x00" in content:
        raise ValueError("source exceeds text bounds; select or prepare a smaller source")
    return {"id": identity, "title": title or identity, "role": role,
            "locator": locator, "content": content,
            "sha256": hashlib.sha256(raw).hexdigest()}


def inventory(sources):
    if len(sources) > MAX_SOURCES:
        raise ValueError("notebook has more than 64 sources; explicit subset export is not implemented")
    ids = [source.id for source in sources]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate notebook source IDs")
    return sorted((s.id, s.title, s.url, int(s.status), str(s.drive_status)) for s in sources)


async def collect(client, identity):
    book = await client.notebooks.get(identity)
    if book.id != identity:
        raise ValueError("notebook response identity mismatch")
    sources = await client.sources.list(identity, strict=True)
    before = inventory(sources)
    notes = await client.notes.list(identity)
    if not 1 <= len(sources) + len(notes) <= MAX_SOURCES:
        raise ValueError("snapshot requires 1..64 sources plus notes")
    records = []
    for source in sources:
        if not source.is_ready or source.is_drive_degraded:
            raise ValueError("a source is not ready or its Drive backing is degraded")
        fulltext = await client.sources.get_fulltext(identity, source.id)
        if fulltext.source_id != source.id:
            raise ValueError("source text identity mismatch")
        records.append(source_record(source.id, source.title, "source",
                                     "notebooklm:" + identity + "/source/" + source.id,
                                     fulltext.content))
    for note in notes:
        if note.notebook_id != identity:
            raise ValueError("note belongs to another notebook")
        records.append(source_record("note_" + note.id, note.title, "planning_artifact",
                                     "notebooklm:" + identity + "/note/" + note.id, note.content))
    if inventory(await client.sources.list(identity, strict=True)) != before:
        raise ValueError("source inventory changed during export; retry a fresh snapshot")
    after_notes = await client.notes.list(identity)
    if [(n.id, n.title, n.content) for n in notes] != [(n.id, n.title, n.content) for n in after_notes]:
        raise ValueError("notes changed during export; retry a fresh snapshot")
    return {"format": "praetor-notebook-v1", "notebook_id": identity,
            "title": book.title, "captured_at": datetime.now(timezone.utc).isoformat(),
            "connector": "notebooklm-py/" + CLIENT_VERSION,
            "coverage": ["Indexed source text and text notes only; studio artifacts, chats, media and original binaries are not exported.",
                         "Inventory was rechecked; this is not an atomic Google snapshot or proof that Drive content is current.",
                         "Quotes refer to captured indexed text; Google citation offsets are not preserved."],
            "sources": records}


async def export(identity, auth_path=None):
    if version("notebooklm-py") != CLIENT_VERSION:
        raise ValueError("requires notebooklm-py==" + CLIENT_VERSION)
    from notebooklm import NotebookLMClient
    async with asyncio.timeout(120):
        async with NotebookLMClient.from_storage(
                path=auth_path, timeout=15, rate_limit_max_retries=0,
                server_error_max_retries=0) as client:
            return await collect(client, identity)


def write_snapshot(path, bundle):
    raw = (json.dumps(bundle, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
    if len(raw) > MAX_BUNDLE_BYTES:
        raise ValueError("snapshot exceeds 1 MiB; no output written")
    target = Path(path).absolute()
    if target.parent.resolve(strict=True) != target.parent:
        raise ValueError("output parent must not contain symlinks")
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as output:
        output.write(raw)
        output.flush()
        os.fsync(output.fileno())
    return {"snapshot_sha256": hashlib.sha256(raw).hexdigest(),
            "sources": len(bundle["sources"]), "bytes": len(raw)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--notebook", required=True)
    parser.add_argument("--output", required=True, help="New private JSON file; existing parent")
    parser.add_argument("--auth-path", help="Explicit notebooklm-py login state; never printed")
    args = parser.parse_args()
    logging.disable(logging.CRITICAL)
    try:
        identity = notebook_id(args.notebook)
        bundle = asyncio.run(export(identity, args.auth_path))
        print(json.dumps(write_snapshot(args.output, bundle)))
    except Exception as error:
        # Upstream exception messages may include private response data or auth.
        print("Notebook export failed (" + type(error).__name__ + "). Check login, selected notebook access, readiness and snapshot limits. No success was recorded.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
