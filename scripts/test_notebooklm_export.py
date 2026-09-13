import asyncio
from pathlib import Path
import tempfile
from types import SimpleNamespace as Obj
import unittest
from unittest.mock import AsyncMock

import notebooklm_export as export

IDENTITY = "00000000-0000-4000-8000-000000000001"


class NotebookExportTests(unittest.TestCase):
    def client(self):
        source = Obj(id="source-1", title="Scope", url=None, status=2,
                     drive_status=None, is_ready=True, is_drive_degraded=False)
        client = Obj(notebooks=Obj(get=AsyncMock(return_value=Obj(id=IDENTITY, title="Fixture"))),
                     sources=Obj(list=AsyncMock(return_value=[source]),
                                 get_fulltext=AsyncMock(return_value=Obj(source_id="source-1", content="Keep originals."))),
                     notes=Obj(list=AsyncMock(return_value=[])))
        return client, source

    def test_read_only_snapshot_and_private_no_overwrite(self):
        client, _ = self.client()
        bundle = asyncio.run(export.collect(client, IDENTITY))
        self.assertEqual(bundle["sources"][0]["content"], "Keep originals.")
        self.assertEqual(len(bundle["sources"][0]["sha256"]), 64)
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "snapshot.json"
            result = export.write_snapshot(path, bundle)
            self.assertEqual(result["sources"], 1)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            before = path.read_bytes()
            with self.assertRaises(FileExistsError):
                export.write_snapshot(path, bundle)
            self.assertEqual(path.read_bytes(), before)

    def test_inventory_and_readiness_failures(self):
        for attr in ("is_ready", "is_drive_degraded"):
            client, source = self.client()
            setattr(source, attr, attr == "is_drive_degraded")
            with self.assertRaises(ValueError):
                asyncio.run(export.collect(client, IDENTITY))
        client, _ = self.client()
        client.sources.list.side_effect = [client.sources.list.return_value, []]
        with self.assertRaises(ValueError):
            asyncio.run(export.collect(client, IDENTITY))

    def test_source_and_count_bounds(self):
        for content in ("", "\x00", "x" * (export.MAX_SOURCE_BYTES + 1)):
            with self.assertRaises(ValueError):
                export.source_record("a", "a", "source", "a", content)
        self.assertEqual(len(export.source_record("a", "a", "source", "a", "x" * export.MAX_SOURCE_BYTES)["content"]), export.MAX_SOURCE_BYTES)
        with self.assertRaises(ValueError):
            export.inventory([Obj(id="x")] * 65)

    def test_url_and_identity(self):
        self.assertEqual(export.notebook_id("https://notebook.google.com/notebook/" + IDENTITY), IDENTITY)
        for value in ("invalid", "https://example.com/notebook/" + IDENTITY,
                      "https://user:secret@notebook.google.com/notebook/" + IDENTITY):
            with self.assertRaises(ValueError):
                export.notebook_id(value)


if __name__ == "__main__":
    unittest.main()
