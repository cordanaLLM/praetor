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


class BuildDirectoryTests(unittest.TestCase):
    """The build lands inside the checkout, where the engine-build check accepts a -dirty
    build for a context write (internal/workstation/freshness.go)."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-build-directory-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.override = patch.object(dev_mcp, "ROOT", self.root)
        self.override.start()
        self.addCleanup(self.override.stop)

    def test_build_directory_lies_under_the_checkout_bin_and_is_removed(self):
        with dev_mcp.build_directory() as directory:
            path = Path(directory)
            self.assertEqual(path.parent, self.root / "bin")
            self.assertTrue(path.is_dir())
        self.assertFalse(path.exists())

    def test_existing_bin_contents_survive(self):
        (self.root / "bin").mkdir()
        kept = self.root / "bin" / "praetorctl"
        kept.write_bytes(b"binary")
        with dev_mcp.build_directory() as directory:
            self.assertNotEqual(Path(directory), self.root / "bin")
        self.assertEqual(kept.read_bytes(), b"binary")

    def test_bin_occupied_by_a_file_fails(self):
        (self.root / "bin").write_text("not a directory\n")
        with self.assertRaises(OSError):
            dev_mcp.build_directory()

    def test_main_builds_inside_the_checkout(self):
        seen = []

        def fake_build(directory):
            seen.append(directory)
            raise RuntimeError("stop after the build directory is chosen")

        args = dev_mcp.argparse.Namespace(action="call", root=self.root, tool="t", arguments={},
                                          allow_remote_benchmarks=False, timeout=30)
        with patch.object(dev_mcp, "parse_args", return_value=args), \
                patch.object(dev_mcp, "build", side_effect=fake_build):
            with self.assertRaisesRegex(RuntimeError, "stop after"):
                dev_mcp.main()
        self.assertEqual(len(seen), 1)
        self.assertEqual(seen[0].parent, self.root / "bin")


class BuildVersionTests(unittest.TestCase):
    """The development build writes its fingerprint into main.version, the variable every
    praetor binary declares and the release config writes (internal/buildid, #666)."""

    def build_command(self, fingerprint):
        calls = []

        def fake_output(command, timeout=10):
            calls.append(command)
            return b""

        with patch.object(dev_mcp, "source_hash", return_value=fingerprint), \
                patch.object(dev_mcp, "command_output", side_effect=fake_output), \
                patch.object(dev_mcp, "read_bounded", return_value=b"binary"):
            _, metadata = dev_mcp.build(Path("out"))
        return calls[0], metadata

    def test_fingerprint_is_injected_into_main_version(self):
        command, metadata = self.build_command("abc123")
        self.assertEqual(command[:4], ["go", "build", "-ldflags", "-X main.version=dev-abc123"])
        self.assertEqual(metadata["server_version"], "dev-abc123")

    def test_retired_mcp_version_target_is_not_written(self):
        command, _ = self.build_command("abc123")
        self.assertFalse(any("mcpVersion" in part for part in command))

    def test_sources_changing_during_the_build_fail(self):
        hashes = iter(["before", "after"])
        with patch.object(dev_mcp, "source_hash", side_effect=lambda: next(hashes)), \
                patch.object(dev_mcp, "command_output", return_value=b""):
            with self.assertRaisesRegex(RuntimeError, "changed during build"):
                dev_mcp.build(Path("out"))


class PublicLoopLauncherTests(unittest.TestCase):
    def test_remote_clones_require_explicit_launcher_opt_in(self):
        command = dev_mcp.server_command(Path("binary"), Path("root"))
        self.assertNotIn("--allow-remote-benchmarks", command)
        command = dev_mcp.server_command(Path("binary"), Path("root"), True)
        self.assertIn("--allow-remote-benchmarks", command)

    def test_public_call_timeout_is_bounded(self):
        for value in ("1", "30", "300"):
            self.assertEqual(dev_mcp.rpc_timeout(value), int(value))
        for value in ("0", "301"):
            with self.assertRaises(dev_mcp.argparse.ArgumentTypeError):
                dev_mcp.rpc_timeout(value)
        with self.assertRaises(ValueError):
            dev_mcp.rpc_timeout("unbounded")


if __name__ == "__main__":
    unittest.main()
