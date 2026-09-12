"""Development provenance must follow the working tree, including uncommitted code."""

from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import dev_mcp


class SourceIdentityTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-source-identity-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.override = patch.object(dev_mcp, "ROOT", self.root)
        self.override.start()
        self.addCleanup(self.override.stop)
        self.git("init", "-q")
        (self.root / "go.mod").write_text("module fixture\n")
        self.source = self.root / "main.go"
        self.source.write_text("package main\n")
        self.git("add", "go.mod", "main.go")

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True,
                       capture_output=True, timeout=10)

    def test_uncommitted_edits_additions_and_deletions_change_identity(self):
        initial = dev_mcp.source_hash()
        self.assertEqual(dev_mcp.source_hash(), initial)
        self.source.write_text("package main\nfunc WorkInProgress() {}\n")
        edited = dev_mcp.source_hash()
        self.assertNotEqual(edited, initial)
        extra = self.root / "new.go"
        extra.write_text("package main\n")
        added = dev_mcp.source_hash()
        self.assertNotEqual(added, edited)
        self.source.unlink()
        self.assertNotEqual(dev_mcp.source_hash(), added)

    def test_empty_and_oversized_source_inventory_fail(self):
        with patch.object(dev_mcp, "command_output", return_value=b""):
            with self.assertRaisesRegex(RuntimeError, "empty"):
                dev_mcp.source_hash()
        with patch.object(dev_mcp, "MAX_SOURCE_FILES", 1):
            with self.assertRaisesRegex(RuntimeError, "bound"):
                dev_mcp.source_hash()

    def test_file_read_accepts_exact_bound_and_rejects_overflow(self):
        self.source.write_bytes(b"1234")
        with patch.object(dev_mcp, "MAX_FILE_BYTES", 4):
            self.assertEqual(dev_mcp.read_bounded(self.source), b"1234")
            self.source.write_bytes(b"12345")
            with self.assertRaisesRegex(RuntimeError, "bound"):
                dev_mcp.read_bounded(self.source)

    def test_server_identity_mismatch_fails(self):
        class Client:
            initialize_response = {"result": {"serverInfo": {"version": "old"}}}
        with self.assertRaisesRegex(RuntimeError, "fingerprint"):
            dev_mcp.check_identity(Client(), {"server_version": "new"})


if __name__ == "__main__":
    unittest.main()
