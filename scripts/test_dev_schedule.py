"""User timer installation against disposable files and an observable service manager."""

from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import dev_schedule as schedule


class ScheduleTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="praetor-schedule-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.directory, self.backups = self.root / "units", self.root / "backups"
        self.config = self.root / "schedule.json"
        self.config.write_text('{"version":1}')
        self.name = "praetor-dogfood-test"
        self.calls = []
        self.enabled, self.active, self.busy = False, False, False
        self.failure = None
        self.foreign = False
        # A private copy, not /usr/bin/true directly: runner_digest's O_NOFOLLOW open
        # refuses a symlink, and some platform images ship /usr/bin/true as one. shutil.copy
        # follows the source and always writes a fresh regular file at the destination.
        self.runner_binary = self.root / "true"
        shutil.copy("/usr/bin/true", self.runner_binary)
        self.runner_binary.chmod(0o755)
        self.runner = str(self.runner_binary)
        self.runner_hash = schedule.runner_digest(self.runner_binary)
        self.mock = patch.object(schedule, "command", side_effect=self.command)
        self.mock.start()
        self.addCleanup(self.mock.stop)

    def command(self, args, check=True):
        self.calls.append(args)
        verb = args[2] if args[0] == "systemctl" else args[0]
        if self.failure == verb:
            self.failure = None
            raise RuntimeError("fixture command failure")
        output = ""
        if args[0] == str(self.runner_binary):
            import json
            output = json.dumps({"config": {"runner_binary": self.runner}, "runner_sha256": self.runner_hash})
        elif verb == "is-enabled":
            output = "enabled" if self.enabled else "disabled"
        elif verb == "is-active":
            busy = self.active if args[3].endswith(".timer") else self.busy
            output = "active" if busy else "inactive"
        elif verb == "show" and self.foreign:
            output = "/foreign/unit"
        elif verb == "start":
            self.active = True
        elif verb == "stop":
            self.active = False
        elif verb == "enable":
            self.enabled = True
        elif verb == "disable":
            self.enabled = False
            if "--now" in args:
                self.active = False
        return subprocess.CompletedProcess(args, 0, output, "")

    def install(self, activate=True):
        return schedule.install(self.name, self.runner_binary, self.config,
                                self.directory, self.backups, activate)

    def test_activate_and_repeat_retains_previous_units_and_timer(self):
        first = self.install()
        second = self.install(activate=False)
        self.assertNotEqual(first["backup_dir"], second["backup_dir"])
        backup = Path(second["backup_dir"])
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        for suffix in (".service", ".timer"):
            target = self.directory / (self.name + suffix)
            self.assertEqual(target.read_bytes(), (backup / target.name).read_bytes())
            self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        self.assertEqual(second["timer"], {"active": True, "enabled": True})

    def test_install_without_activation_keeps_disabled(self):
        self.assertEqual(self.install(False)["timer"], {"active": False, "enabled": False})

    def test_mismatched_config_runner_fails_before_creating_units(self):
        self.runner = "/different/runner"
        with self.assertRaisesRegex(ValueError, "runner_binary must match"):
            self.install()
        self.assertFalse(self.directory.exists())

    def test_changed_runner_fails_before_creating_units(self):
        self.runner_hash = "0" * 64
        with self.assertRaisesRegex(ValueError, "runner digest changed"):
            self.install()
        self.assertFalse(self.directory.exists())

    def test_disable_retains_running_service_units_and_evidence(self):
        self.install()
        self.busy = True
        report = schedule.disable(self.name, self.directory, self.backups)
        self.assertTrue(self.busy)
        self.assertEqual(report["timer"], {"active": False, "enabled": False})
        self.assertEqual(len(list(self.directory.iterdir())), 2)
        self.assertEqual(len(list(self.backups.glob("install-*/manifest.json"))), 1)

    def test_disable_rejects_concurrent_installer_and_loaded_override(self):
        self.install()
        with schedule.installation_lock(self.backups):
            with self.assertRaisesRegex(RuntimeError, "Another schedule installer"):
                schedule.disable(self.name, self.directory, self.backups)
        self.foreign = True
        with self.assertRaisesRegex(RuntimeError, "foreign loaded"):
            schedule.disable(self.name, self.directory, self.backups)
        self.assertTrue(self.active)

    def test_activation_failure_rolls_back_new_files_and_enable_state(self):
        self.failure = "start"
        with self.assertRaisesRegex(RuntimeError, r"rollback errors: \[\]"):
            self.install()
        self.assertEqual(list(self.directory.iterdir()), [])
        self.assertFalse(self.enabled)
        self.assertFalse(self.active)
        self.assertEqual(len(list(self.backups.glob("install-*/manifest.json"))), 1)

    def test_reload_failure_restores_previous_active_timer_and_files(self):
        self.install()
        before = {path.name: path.read_bytes() for path in self.directory.iterdir()}
        self.failure = "daemon-reload"
        with self.assertRaisesRegex(RuntimeError, r"rollback errors: \[\]"):
            self.install()
        self.assertTrue(self.enabled)
        self.assertTrue(self.active)
        self.assertEqual(before, {path.name: path.read_bytes() for path in self.directory.iterdir()})

    def test_error_after_atomic_replace_still_restores_file(self):
        original = schedule.atomic_write
        failed = False

        def fail_after_replace(path, data, mode=0o600):
            nonlocal failed
            original(path, data, mode)
            if path.parent == self.directory and not failed:
                failed = True
                raise OSError("fixture post-replace failure")

        with patch.object(schedule, "atomic_write", side_effect=fail_after_replace):
            with self.assertRaisesRegex(RuntimeError, r"rollback errors: \[\]"):
                self.install()
        self.assertEqual(list(self.directory.iterdir()), [])

    def test_timeout_during_rollback_does_not_prevent_file_restore(self):
        original = self.command
        failed = False

        def fail_twice(args, check=True):
            nonlocal failed
            if args[:3] == ["systemctl", "--user", "start"]:
                failed = True
                raise subprocess.TimeoutExpired(args, 30)
            if failed and args[:3] == ["systemctl", "--user", "stop"]:
                raise subprocess.TimeoutExpired(args, 30)
            return original(args, check)

        with patch.object(schedule, "command", side_effect=fail_twice):
            with self.assertRaisesRegex(RuntimeError, "rollback errors: .*timed out"):
                self.install()
        self.assertEqual(list(self.directory.iterdir()), [])

    def test_read_only_units_and_backups_keep_restrictive_permissions(self):
        self.install()
        for path in self.directory.iterdir():
            path.chmod(0o400)
        report = self.install()
        for path in self.directory.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o400)
            self.assertEqual((Path(report["backup_dir"]) / path.name).stat().st_mode & 0o777, 0o400)

    def test_verification_failure_never_changes_timer_or_units(self):
        self.failure = "systemd-analyze"
        with self.assertRaises(RuntimeError):
            self.install()
        self.assertEqual(list(self.directory.iterdir()), [])
        self.assertFalse(any(call[2] in {"enable", "stop", "start"}
                             for call in self.calls if call[0] == "systemctl"))

    def test_rejects_foreign_file_link_and_loaded_override(self):
        self.directory.mkdir()
        target = self.directory / (self.name + ".service")
        target.write_text("[Service]\nExecStart=/usr/bin/true\n")
        with self.assertRaisesRegex(ValueError, "unmanaged"):
            self.install()
        target.unlink()
        target.symlink_to(self.config)
        with self.assertRaises(OSError):
            self.install()
        target.unlink()
        self.foreign = True
        with self.assertRaisesRegex(RuntimeError, "foreign loaded"):
            self.install()
        self.assertEqual(self.config.read_text(), '{"version":1}')

    def test_running_service_and_concurrent_install_rejected(self):
        self.busy = True
        with self.assertRaisesRegex(RuntimeError, "may be running"):
            self.install()
        self.busy = False
        with schedule.installation_lock(self.backups):
            with self.assertRaisesRegex(RuntimeError, "Another schedule installer"):
                self.install()

    def test_path_and_name_injection_rejected_and_literals_escaped(self):
        for bad in ("../foreign", "foreign", "praetor-dogfood-a.service", "praetor-dogfood-a\n"):
            with self.assertRaises(ValueError):
                schedule.unit_name(bad)
        for bad in ("relative", "/tmp/../etc/passwd", "/tmp/a\nExecStart=bad"):
            with self.assertRaises(ValueError):
                schedule.quote_argument(bad)
        self.assertEqual(schedule.quote_argument('/tmp/a $USER %h "q" \\b'),
                         '"/tmp/a $$USER %%h \\"q\\" \\\\b"')
        self.assertEqual(schedule.quote_argument('/tmp/a $USER %h/runner', executable=True),
                         '"/tmp/a $USER %%h/runner"')
        for bad in ('/tmp/a"b/runner', '/tmp/a\\b/runner'):
            with self.assertRaisesRegex(ValueError, "systemd executable paths"):
                schedule.quote_argument(bad, executable=True)

    def test_special_file_and_oversized_config_rejected(self):
        import os
        self.config.unlink()
        os.mkfifo(self.config)
        with self.assertRaisesRegex(ValueError, "regular"):
            self.install()
        self.config.unlink()
        self.config.write_bytes(b"x" * (schedule.MAX_FILE + 1))
        with self.assertRaisesRegex(ValueError, "bounded"):
            self.install()


if __name__ == "__main__":
    unittest.main()
