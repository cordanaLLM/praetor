"""Installation behavior against disposable destinations, including rollback."""

import json
import os
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
        for name in dev_install.COMMANDS:
            (self.staging / name).write_bytes(b"new-" + name.encode())
            (self.staging / name).chmod(0o755)
            (self.bin_dir / name).write_bytes(b"old-" + name.encode())
            (self.bin_dir / name).chmod(0o755)
        # The pre-rename installer left one alias link per binary.
        for legacy, name in dev_install.LEGACY_LINKS.items():
            (self.bin_dir / legacy).symlink_to(name)

    def install(self):
        return dev_install.install_files(self.staging, self.bin_dir, self.backups, {"source_sha256": "fixture"})

    def test_command_map_names_praetor_packages_only(self):
        self.assertEqual(dev_install.COMMANDS, {"praetorctl": "praetorctl", "praetor-mcp": "praetor-mcp",
                                                "praetor-lsp": "praetor-lsp"})
        self.assertFalse(set(dev_install.LEGACY_LINKS) & set(dev_install.COMMANDS))
        self.assertEqual(set(dev_install.LEGACY_LINKS.values()), set(dev_install.COMMANDS))

    def test_installs_binaries_manifest_removes_legacy_links_and_retains_old_files(self):
        report = self.install()
        backup = Path(report["backup_dir"])
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        for name in dev_install.COMMANDS:
            self.assertEqual((self.bin_dir / name).read_bytes(), b"new-" + name.encode())
            self.assertEqual((backup / name).read_bytes(), b"old-" + name.encode())
        for legacy, name in dev_install.LEGACY_LINKS.items():
            self.assertFalse(os.path.lexists(self.bin_dir / legacy))
            self.assertEqual(os.readlink(backup / legacy), name)
        self.assertEqual(report["removed_legacy_links"], list(dev_install.LEGACY_LINKS))
        manifest = json.loads((self.bin_dir / dev_install.MANIFEST).read_text())
        self.assertEqual(manifest["removed_legacy_links"], list(dev_install.LEGACY_LINKS))

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
        for name in dev_install.COMMANDS:
            self.assertEqual((self.bin_dir / name).read_bytes(), b"old-" + name.encode())
        for legacy, name in dev_install.LEGACY_LINKS.items():
            self.assertEqual(os.readlink(self.bin_dir / legacy), name)
        self.assertFalse((self.bin_dir / dev_install.MANIFEST).exists())

    def test_rollback_restores_removed_legacy_link(self):
        original = Path.unlink

        def fail_last_legacy(path, missing_ok=False):
            if path.name == "standards-lsp" and path.parent == self.bin_dir:
                raise OSError("fixture unlink failure")
            return original(path, missing_ok=missing_ok)

        with patch.object(Path, "unlink", fail_last_legacy):
            with self.assertRaisesRegex(RuntimeError, "fixture unlink failure.*rollback errors: \\[\\]"):
                self.install()
        for legacy, name in dev_install.LEGACY_LINKS.items():
            self.assertEqual(os.readlink(self.bin_dir / legacy), name)
        for name in dev_install.COMMANDS:
            self.assertEqual((self.bin_dir / name).read_bytes(), b"old-" + name.encode())
        self.assertFalse((self.bin_dir / dev_install.MANIFEST).exists())

    def test_legacy_regular_file_refused_before_any_change(self):
        legacy = self.bin_dir / "standardsctl"
        legacy.unlink()
        legacy.write_bytes(b"stale-standardsctl")
        message = (f"Refusing legacy installation entry {legacy}: it is not a symlink to praetorctl; "
                   "remove it manually, Praetor installs only praetorctl")
        with self.assertRaises(RuntimeError) as raised:
            self.install()
        self.assertEqual(str(raised.exception), message)
        self.assertEqual(legacy.read_bytes(), b"stale-standardsctl")
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")
        self.assertEqual(os.readlink(self.bin_dir / "standards-mcp"), "praetor-mcp")
        self.assertFalse(self.backups.exists())

    def test_legacy_link_to_other_binary_refused(self):
        legacy = self.bin_dir / "standards-mcp"
        legacy.unlink()
        legacy.symlink_to("praetorctl")
        with self.assertRaisesRegex(RuntimeError, "standards-mcp: it is not a symlink to praetor-mcp"):
            self.install()
        self.assertEqual(os.readlink(legacy), "praetorctl")
        self.assertEqual((self.bin_dir / "praetor-mcp").read_bytes(), b"old-praetor-mcp")

    def test_legacy_directory_refused(self):
        legacy = self.bin_dir / "standards-lsp"
        legacy.unlink()
        legacy.mkdir()
        with self.assertRaisesRegex(RuntimeError, "Refusing legacy installation entry"):
            self.install()
        self.assertTrue(legacy.is_dir())

    def test_dangling_legacy_link_to_own_binary_is_removed(self):
        (self.bin_dir / "praetor-lsp").unlink()
        report = self.install()
        self.assertFalse(os.path.lexists(self.bin_dir / "standards-lsp"))
        self.assertIn("standards-lsp", report["removed_legacy_links"])
        self.assertEqual((self.bin_dir / "praetor-lsp").read_bytes(), b"new-praetor-lsp")

    def test_foreign_symlink_rejected_without_touching_target(self):
        target = self.bin_dir / "praetorctl"
        target.unlink()
        target.symlink_to(self.staging / "praetorctl")
        with self.assertRaisesRegex(RuntimeError, "unexpected.*symlink"):
            self.install()
        self.assertTrue(target.is_symlink())
        self.assertEqual(os.readlink(self.bin_dir / "standardsctl"), "praetorctl")

    def test_directory_target_rejected_before_any_replacement(self):
        target = self.bin_dir / "praetor-lsp"
        target.unlink()
        target.mkdir()
        with self.assertRaisesRegex(RuntimeError, "non-regular"):
            self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")

    def test_empty_install_and_repeat_install_create_no_legacy_names(self):
        for target in self.bin_dir.iterdir():
            target.unlink()
        first = self.install()
        second = self.install()
        self.assertNotEqual(first["backup_dir"], second["backup_dir"])
        self.assertEqual(first["removed_legacy_links"], [])
        self.assertEqual(second["removed_legacy_links"], [])
        self.assertEqual(sorted(path.name for path in self.bin_dir.iterdir()),
                         sorted([*dev_install.COMMANDS, dev_install.MANIFEST]))
        self.assertEqual((Path(second["backup_dir"]) / "praetorctl").read_bytes(), b"new-praetorctl")

    def test_concurrent_install_rejected_before_replacing_files(self):
        with dev_install.installation_lock(self.bin_dir):
            with self.assertRaisesRegex(RuntimeError, "Installation lock exists"):
                self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")
        self.assertFalse((self.bin_dir / ".praetor-dev-install.lock").exists())

    def test_restrictive_permissions_apply_to_replaced_binary(self):
        (self.bin_dir / "praetorctl").chmod(0o700)
        self.install()
        self.assertEqual((self.bin_dir / "praetorctl").stat().st_mode & 0o777, 0o700)
        self.assertEqual((self.bin_dir / "praetor-mcp").stat().st_mode & 0o777, 0o755)

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

    def test_build_compiles_each_praetor_package_directory(self):
        calls = []
        metadata = {"source_sha256": "fixture"}
        with patch.object(dev_install.dev_mcp, "build", return_value=(self.staging / "praetor-mcp", metadata)), \
                patch.object(dev_install.dev_mcp, "command_output", side_effect=lambda argv, timeout: calls.append(argv)), \
                patch.object(dev_install.dev_mcp, "source_hash", return_value="fixture"), \
                patch.object(dev_install, "probe", return_value={"passed": ["fixture"]}):
            dev_install.build(self.staging)
        self.assertEqual([call[-1] for call in calls], ["./cmd/praetorctl", "./cmd/praetor-lsp"])
        self.assertFalse(any("standards" in part for call in calls for part in call))

    def test_existing_non_executable_target_fails_before_replacement(self):
        (self.bin_dir / "praetorctl").chmod(0o600)
        with self.assertRaisesRegex(RuntimeError, "permissions prevent"):
            self.install()
        self.assertEqual((self.bin_dir / "praetorctl").read_bytes(), b"old-praetorctl")


if __name__ == "__main__":
    unittest.main()
