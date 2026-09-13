"""Installation behavior against disposable destinations, including rollback."""

from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import dev_install


class InstallTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="praetor-install-test-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.bin_dir, self.staging, self.backups = root / "bin", root / "build", root / "backups"
        self.bin_dir.mkdir()
        self.staging.mkdir()
        for name, alias in dev_install.COMMANDS.items():
            (self.staging / name).write_bytes(b"new-" + name.encode())
            (self.staging / name).chmod(0o755)
            for previous in (name, alias):
                (self.bin_dir / previous).write_bytes(b"old-" + previous.encode())
                (self.bin_dir / previous).chmod(0o755)

    def install(self):
        return dev_install.install_files(self.staging, self.bin_dir, self.backups, {"source_sha256": "fixture"})

    def test_installs_binaries_aliases_manifest_and_retains_old_files(self):
        report = self.install()
        backup = Path(report["backup_dir"])
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        for name, alias in dev_install.COMMANDS.items():
            self.assertEqual((self.bin_dir / name).read_bytes(), b"new-" + name.encode())
            self.assertEqual((self.bin_dir / alias).read_bytes(), b"new-" + name.encode())
            self.assertEqual((backup / alias).read_bytes(), b"old-" + alias.encode())
            self.assertTrue((self.bin_dir / alias).is_symlink())
        self.assertTrue((self.bin_dir / dev_install.MANIFEST).exists())

    def test_write_failure_restores_replaced_files(self):
        original = dev_install.replace_from
        failed = False

        def fail_once(source, destination):
            nonlocal failed
            if destination.name == "praetor-mcp" and not failed:
                failed = True
                raise OSError("fixture disk failure")
            original(source, destination)

        with patch.object(dev_install, "replace_from", side_effect=fail_once):
            with self.assertRaisesRegex(RuntimeError, "rollback errors: \\[\\]"):
                self.install()
        for name, alias in dev_install.COMMANDS.items():
            for previous in (name, alias):
                self.assertEqual((self.bin_dir / previous).read_bytes(), b"old-" + previous.encode())
        self.assertFalse((self.bin_dir / dev_install.MANIFEST).exists())

    def test_foreign_symlink_rejected_without_touching_target(self):
        target = self.bin_dir / "praetorctl"
        target.unlink()
        target.symlink_to(self.staging / "praetorctl")
        with self.assertRaisesRegex(RuntimeError, "unexpected.*symlink"):
            self.install()
        self.assertTrue(target.is_symlink())
        self.assertEqual((self.bin_dir / "standardsctl").read_bytes(), b"old-standardsctl")

    def test_directory_target_rejected_before_any_replacement(self):
        target = self.bin_dir / "praetor-lsp"
        target.unlink()
        target.mkdir()
        with self.assertRaisesRegex(RuntimeError, "non-regular"):
            self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")

    def test_empty_install_and_repeat_install_accept_own_aliases(self):
        for target in self.bin_dir.iterdir():
            target.unlink()
        first = self.install()
        for alias in dev_install.COMMANDS.values():
            (self.staging / alias).unlink()
        second = self.install()
        self.assertNotEqual(first["backup_dir"], second["backup_dir"])
        self.assertTrue((Path(second["backup_dir"]) / "standardsctl").is_symlink())

    def test_concurrent_install_rejected_before_replacing_files(self):
        with dev_install.installation_lock(self.bin_dir):
            with self.assertRaisesRegex(RuntimeError, "Installation lock exists"):
                self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")
        self.assertFalse((self.bin_dir / ".praetor-dev-install.lock").exists())

    def test_restrictive_permissions_apply_to_binary_and_alias(self):
        (self.bin_dir / "praetorctl").chmod(0o700)
        (self.bin_dir / "standards-mcp").chmod(0o700)
        self.install()
        for name in ("praetorctl", "standardsctl", "praetor-mcp", "standards-mcp"):
            self.assertEqual((self.bin_dir / name).stat().st_mode & 0o777, 0o700)

    def test_post_replacement_failure_also_restores_binary(self):
        original = dev_install.replace_from
        failed = False

        def fail_after_replace(source, destination):
            nonlocal failed
            original(source, destination)
            if destination.name == "praetor-mcp" and not failed:
                failed = True
                raise OSError("fixture cleanup failure after atomic replacement")

        with patch.object(dev_install, "replace_from", side_effect=fail_after_replace):
            with self.assertRaisesRegex(RuntimeError, "rollback errors: \\[\\]"):
                self.install()
        self.assertEqual((self.bin_dir / "praetor-mcp").read_bytes(), b"old-praetor-mcp")

    def test_existing_non_executable_target_fails_before_replacement(self):
        (self.bin_dir / "praetorctl").chmod(0o600)
        with self.assertRaisesRegex(RuntimeError, "permissions prevent"):
            self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")


if __name__ == "__main__":
    unittest.main()
