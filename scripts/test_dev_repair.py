"""Repair queue boundaries and one-dispatch semantics without provider access."""

import hashlib
import io
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import dev_process
import dev_repair as repair


class RepairQueueTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="praetor-repair-queue-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.policy = self.root / "repair.json"
        self.write(self.policy, {"version": 1})
        self.state = self.root / "state"
        self.state.mkdir(mode=0o700)
        self.config = self.root / "queue.json"
        self.values = {"version": 1, "runner_binary": "/usr/bin/true",
                       "runner_sha256": repair.schedule.runner_digest(Path("/usr/bin/true")),
                       "schedule_state_dir": str(self.state), "repair_config": str(self.policy),
                       "repair_config_sha256": hashlib.sha256(self.policy.read_bytes()).hexdigest()}
        self.write(self.config, self.values)

    def write(self, path, value):
        path.write_text(json.dumps(value))
        path.chmod(0o600)

    def report(self, number, status="failed"):
        folder = self.state / f"run-{number:06d}" / "suite"
        folder.mkdir(parents=True, mode=0o700)
        path = folder / "report.json"
        self.write(path, {"status": status})
        return path

    def test_idle_does_not_execute_or_change_evidence(self):
        report = self.report(1, "verified")
        before = report.read_bytes()
        with patch.object(repair, "call_runner") as runner:
            self.assertEqual(repair.queue(self.config, True)["status"], "idle")
            runner.assert_not_called()
        self.assertEqual(report.read_bytes(), before)

    def test_consumed_case_skipped_and_only_one_ready_report_dispatched(self):
        first, second, third = [self.report(n) for n in range(1, 4)]
        calls = []

        def runner(config, action, report, timeout):
            calls.append((action, report))
            return ({"status": "consumed" if report == first else
                     "ready" if action == "status" else "scoped_test_verified"}, 0)

        with patch.object(repair, "call_runner", side_effect=runner):
            result = repair.queue(self.config, True)
        self.assertEqual(result["status"], "scoped_test_verified")
        self.assertEqual(calls, [("status", first), ("status", second), ("run", second)])
        self.assertTrue(third.exists())

    def test_read_only_status_never_dispatches_ready_case(self):
        self.report(1)
        with patch.object(repair, "call_runner", return_value=({"status": "ready"}, 0)) as runner:
            self.assertEqual(repair.queue(self.config)["status"], "ready")
        self.assertEqual([call.args[1] for call in runner.call_args_list], ["status"])

    def test_failed_run_remains_visible_to_service_manager(self):
        args = ["dev_repair.py", "run", "--config", str(self.config)]
        with patch.object(repair.sys, "argv", args), patch.object(repair.sys, "stdout", io.StringIO()), \
                patch.object(repair, "queue", return_value={"status": "verification_failed", "runner_exit_code": 1}):
            with self.assertRaises(SystemExit) as result:
                repair.main()
        self.assertEqual(result.exception.code, 1)

    def test_unknown_report_status_is_not_idle(self):
        self.report(1, "unexpected")
        with self.assertRaisesRegex(ValueError, "terminal"):
            repair.queue(self.config)

    def test_blocked_route_is_visible_and_cannot_appear_idle(self):
        self.report(1)
        with patch.object(repair, "call_runner", return_value=({"status": "blocked"}, 0)):
            result = repair.queue(self.config, True)
        self.assertEqual(result["status"], "blocked")
        self.assertEqual(result["runner_exit_code"], 1)
        self.assertEqual(result["admission_counts"]["blocked"], 1)

    def test_changed_policy_and_runner_rejected_before_scan(self):
        for key in ("runner_sha256", "repair_config_sha256"):
            bad = dict(self.values, **{key: "0" * 64})
            self.write(self.config, bad)
            with self.assertRaisesRegex(ValueError, "changed"):
                repair.queue(self.config, True)

    def test_unknown_duplicate_and_public_config_rejected(self):
        self.write(self.config, dict(self.values, extra=True))
        with self.assertRaises(ValueError):
            repair.configuration(self.config)
        self.config.write_text('{"version":1,"version":1}')
        with self.assertRaisesRegex(ValueError, "Duplicate"):
            repair.configuration(self.config)
        self.write(self.config, self.values)
        self.config.chmod(0o644)
        with self.assertRaisesRegex(ValueError, "private"):
            repair.configuration(self.config)

    def test_symlink_run_and_report_refused(self):
        report = self.report(1)
        report.unlink()
        report.symlink_to(self.policy)
        with self.assertRaisesRegex(ValueError, "Symlink"):
            repair.failed_reports(self.state)
        report.unlink()
        os.symlink(self.root, self.state / "run-000002")
        with self.assertRaisesRegex(ValueError, "symlink"):
            repair.failed_reports(self.state)

    def test_inventory_and_file_size_bounds(self):
        for number in range(1, repair.MAX_REPORTS + 2):
            (self.state / f"run-{number:06d}").mkdir()
        with self.assertRaisesRegex(ValueError, "64 runs"):
            repair.failed_reports(self.state)
        with self.assertRaisesRegex(ValueError, "byte bound"):
            repair.private_bytes(self.policy, 1)

    def test_policy_changed_after_status_prevents_dispatch(self):
        self.report(1)

        def runner(*args):
            self.write(self.policy, {"version": 2})
            return {"status": "ready"}, 0

        with patch.object(repair, "call_runner", side_effect=runner) as calls:
            with self.assertRaisesRegex(ValueError, "policy changed"):
                repair.queue(self.config, True)
        self.assertEqual(calls.call_count, 1)

    def test_service_quotes_paths_and_keeps_separate_execution_limits(self):
        units = repair.render_units("praetor-dogfood-repair-test", self.root / "a %$.py", self.config)
        service = units["praetor-dogfood-repair-test.service"].decode()
        self.assertIn("a %%$$.py", service)
        self.assertIn("MemoryMax=4G", service)
        self.assertIn("MemorySwapMax=0", service)
        self.assertIn("CPUQuota=200%", service)
        self.assertIn("TimeoutStartSec=12min", service)
        self.assertIn("KillMode=control-group", service)

    def runner(self, handler):
        """A runner that reports ready, then sleeps past any deadline; ``handler`` runs on SIGTERM."""
        ready = self.root / "runner-ready"
        script = self.root / "runner"
        script.write_text(f"""#!{sys.executable}
import signal, sys, time
from pathlib import Path
signal.signal(signal.SIGTERM, {handler})
Path({str(ready)!r}).touch()
time.sleep(30)
""")
        script.chmod(0o700)
        return dict(self.values, runner_binary=str(script)), ready

    def test_runner_deadline_lets_the_runner_stop_its_commands(self):
        # The runner is praetorctl: it forwards SIGTERM to the commands it runs in process
        # groups of their own. SIGKILL on its group ended it and left them running.
        marker = self.root / "terminated"
        handler = f"lambda number, _frame: (Path({str(marker)!r}).touch(), sys.exit(128 + number))"
        config, ready = self.runner(handler)
        with self.assertRaisesRegex(RuntimeError, "deadline exceeded"):
            repair.call_runner(config, "status", self.report(1), 2)
        self.assertTrue(ready.exists(), "the runner never started")
        self.assertTrue(marker.exists(), "the runner never got SIGTERM")

    def test_runner_deadline_kills_a_runner_that_ignores_sigterm(self):
        config, ready = self.runner("signal.SIG_IGN")
        with patch.object(dev_process, "STOP_GRACE", 0.3), \
                patch.object(repair.subprocess.Popen, "wait", autospec=True,
                             side_effect=repair.subprocess.Popen.wait) as wait:
            started = time.monotonic()
            with self.assertRaisesRegex(RuntimeError, "deadline exceeded"):
                repair.call_runner(config, "status", self.report(1), 2)
        self.assertTrue(ready.exists(), "the runner never started")
        self.assertGreaterEqual(time.monotonic() - started, 2.3)
        process = wait.call_args.args[0]
        self.assertEqual(process.returncode, -signal.SIGKILL)

    def test_snapshot_is_private_repeatable_and_detects_tampering(self):
        backups = self.root / "backups"
        backups.mkdir(mode=0o700)
        first = repair.snapshot_scripts(backups)
        self.assertEqual(repair.snapshot_scripts(backups), first)
        self.assertEqual(first.stat().st_mode & 0o777, 0o600)
        first.write_text("changed")
        with self.assertRaisesRegex(ValueError, "snapshot changed"):
            repair.snapshot_scripts(backups)


if __name__ == "__main__":
    unittest.main()
