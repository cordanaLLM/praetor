"""Development provenance must follow the working tree, including uncommitted code."""

import hashlib
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import call, mock_open, patch

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

    def test_source_hash_keeps_the_per_file_read_bound(self):
        """A source file over the per-file read bound still fails the source hash."""
        self.source.write_bytes(b"1234")
        with patch.object(dev_mcp, "MAX_FILE_BYTES", 4), \
                patch.object(dev_mcp, "command_output", return_value=b"main.go\0"):
            expected = hashlib.sha256(b"main.go\0" + hashlib.sha256(b"1234").digest()).hexdigest()
            self.assertEqual(dev_mcp.source_hash(), expected)
            self.source.write_bytes(b"12345")
            with self.assertRaisesRegex(RuntimeError, "File exceeds development provenance bound"):
                dev_mcp.source_hash()

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


class BinaryDigestTests(unittest.TestCase):
    def setUp(self):
        """Create a temporary folder for the binary under test."""
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-binary-digest-")
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.binary = self.directory / "praetor-mcp"
        self.override = patch.multiple(dev_mcp, MAX_FILE_BYTES=4, MAX_BINARY_BYTES=12,
                                       HASH_CHUNK_BYTES=4)
        self.override.start()
        self.addCleanup(self.override.stop)

    def test_build_hashes_binary_larger_than_source_read_bound(self):
        """A built binary larger than the source read bound is hashed in full."""
        content = b"123456789"
        self.binary.write_bytes(content)
        with patch.object(dev_mcp, "source_hash", return_value="fingerprint"), \
                patch.object(dev_mcp, "command_output", return_value=b""):
            _, metadata = dev_mcp.build(self.directory)
        self.assertEqual(metadata["binary_sha256"], hashlib.sha256(content).hexdigest())

    def test_digest_matches_whole_file_at_chunk_and_size_boundaries(self):
        """The streamed digest equals the whole-file digest at chunk and size boundaries."""
        for limit in (11, 12):
            for size in (0, 1, 4, 5, 8, 9, limit):
                with self.subTest(limit=limit, size=size), \
                        patch.object(dev_mcp, "MAX_BINARY_BYTES", limit):
                    content = bytes(range(size))
                    self.binary.write_bytes(content)
                    self.assertEqual(dev_mcp.hash_binary(self.binary),
                                     hashlib.sha256(content).hexdigest())

    def test_binary_over_sanity_limit_fails_with_its_name(self):
        """A binary over the sanity limit fails, naming the file."""
        for limit in (11, 12):
            with self.subTest(limit=limit), patch.object(dev_mcp, "MAX_BINARY_BYTES", limit):
                self.binary.write_bytes(b"x" * (limit + 1))
                with self.assertRaisesRegex(RuntimeError, "Binary exceeds development sanity limit") \
                        as raised:
                    dev_mcp.hash_binary(self.binary)
                self.assertIn(str(self.binary), str(raised.exception))

    def test_binary_reads_fixed_size_chunks(self):
        """The binary is read in fixed-size chunks, never whole."""
        opened = mock_open(read_data=b"123456789")
        with patch.object(Path, "open", opened):
            self.assertEqual(dev_mcp.hash_binary(self.binary),
                             hashlib.sha256(b"123456789").hexdigest())
        self.assertEqual(opened.return_value.read.call_args_list, [call(4)] * 4)


class BuildVersionTests(unittest.TestCase):
    """The development build writes its fingerprint into main.version, the variable every
    praetor binary declares and the release config writes (internal/buildid, #666)."""

    def build_command(self, fingerprint):
        """Run the build command with fingerprint and return the recorded calls."""
        calls = []

        def fake_output(command, timeout=10):
            calls.append(command)
            return b""

        with patch.object(dev_mcp, "source_hash", return_value=fingerprint), \
                patch.object(dev_mcp, "command_output", side_effect=fake_output), \
                patch.object(dev_mcp, "hash_binary", return_value=hashlib.sha256(b"binary").hexdigest()):
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
