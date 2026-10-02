"""dev_install.py is a thin wrapper: an MCP probe gate, then a delegated call to
`workstation install` (internal/workstation, HISS-19 -- the atomic lock/backup/swap/
manifest logic lives there once, not twice). These tests stub that call through
dev_install's own PRAETOR_STANDARDSCTL seam with a recording executable and assert on
what the stub recorded, per the repository rule against proving behavior by running a
real mutating command: no real `go build` and no real bin directory are touched here.
"""

import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import dev_install
import dev_mcp

# Why the cases that start a stand-in program skip on Windows (HISS-21).
POSIX_STUB = ("a PATH stub is a shebang script, which the Windows loader does not start; "
              "the injected-check cases cover the same decisions on this platform")

STUB = """#!/usr/bin/env python3
import json, os, sys
record = os.environ.get("PRAETOR_TEST_RECORD")
if record:
    with open(record, "w") as handle:
        json.dump(sys.argv[1:], handle)
if os.environ.get("PRAETOR_TEST_OUTCOME") == "fail":
    sys.stderr.write("fixture failure\\n")
    sys.exit(1)
sys.stdout.write(os.environ.get("PRAETOR_TEST_STDOUT", "{}"))
"""


class StubbedEngineTests(unittest.TestCase):
    """Exercise install() and standardsctl_argv() against a real recording stub process,
    reached only through the PRAETOR_STANDARDSCTL environment seam."""

    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="praetor-dev-install-test-")
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        self.stub = root / "stub.py"
        self.stub.write_text(STUB)
        self.stub.chmod(self.stub.stat().st_mode | stat.S_IXUSR)
        self.record_path = root / "record.json"
        self.bin_dir = root / "bin"
        self.env_patch = patch.dict(os.environ, {
            "PRAETOR_STANDARDSCTL": str(self.stub),
            "PRAETOR_TEST_RECORD": str(self.record_path),
        })
        self.env_patch.start()
        self.addCleanup(self.env_patch.stop)

    def recorded_argv(self):
        return json.loads(self.record_path.read_text())

    def test_install_invokes_workstation_install_with_source_and_bin_dir(self):
        report = {"manifest": {"engine_commit": "a" * 40}, "manifest_path": "/x"}
        with patch.dict(os.environ, {"PRAETOR_TEST_STDOUT": json.dumps(report)}):
            got = dev_install.install(self.bin_dir)
        self.assertEqual(got, report)
        argv = self.recorded_argv()
        self.assertEqual(argv[:2], ["workstation", "install"])
        self.assertIn("--source", argv)
        self.assertEqual(argv[argv.index("--source") + 1], str(dev_mcp.ROOT))
        self.assertIn("--bin-dir", argv)
        self.assertEqual(argv[argv.index("--bin-dir") + 1], str(self.bin_dir))
        self.assertNotIn("--manifest", argv)

    def test_install_passes_an_explicit_manifest_path(self):
        manifest_path = Path(self.record_path.parent) / "install.json"
        with patch.dict(os.environ, {"PRAETOR_TEST_STDOUT": "{}"}):
            dev_install.install(self.bin_dir, manifest_path)
        argv = self.recorded_argv()
        self.assertIn("--manifest", argv)
        self.assertEqual(argv[argv.index("--manifest") + 1], str(manifest_path))

    def test_install_surfaces_engine_failure(self):
        with patch.dict(os.environ, {"PRAETOR_TEST_OUTCOME": "fail"}):
            with self.assertRaisesRegex(RuntimeError, "workstation install failed.*fixture failure"):
                dev_install.install(self.bin_dir)

    def test_install_rejects_malformed_engine_output(self):
        with patch.dict(os.environ, {"PRAETOR_TEST_STDOUT": "not json"}):
            with self.assertRaises(json.JSONDecodeError):
                dev_install.install(self.bin_dir)


class StandardsctlArgvTests(unittest.TestCase):
    def test_default_invokes_go_run(self):
        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("PRAETOR_STANDARDSCTL", None)
            self.assertEqual(dev_install.standardsctl_argv(), ["go", "run", "./cmd/standardsctl"])

    def test_override_replaces_the_default(self):
        with patch.dict(os.environ, {"PRAETOR_STANDARDSCTL": "/opt/praetorctl"}):
            self.assertEqual(dev_install.standardsctl_argv(), ["/opt/praetorctl"])


class ProbeMCPTests(unittest.TestCase):
    """probe_mcp's own logic (source-changed-under-us detection, metadata merge) is
    covered here with dev_mcp.build/probe mocked out; a real build is dev_mcp's own test
    responsibility (test_dev_mcp.py), not this script's."""

    def test_probe_mcp_merges_checks_into_metadata(self):
        binary = Path("/fixture/praetor-mcp")
        with patch.object(dev_mcp, "build", return_value=(binary, {"source_sha256": "fixed"})), \
             patch.object(dev_mcp, "source_hash", return_value="fixed"), \
             patch.object(dev_install, "probe", return_value={"passed": ["ok"]}) as probe_mock:
            result = dev_install.probe_mcp(Path("/fixture"))
        self.assertEqual(result["checks"], ["ok"])
        # The metadata dict probe_mcp hands to probe() is mutated in place right after the
        # call (checks is added to it), so call_args aliases the post-mutation object; only
        # the stable positional arguments are asserted here.
        self.assertEqual(probe_mock.call_args.args[0], binary)
        self.assertEqual(probe_mock.call_args.args[1], dev_mcp.ROOT)
        self.assertEqual(probe_mock.call_args.args[2]["source_sha256"], "fixed")

    def test_probe_mcp_rejects_a_source_change_during_build(self):
        with patch.object(dev_mcp, "build", return_value=(Path("/fixture/praetor-mcp"), {"source_sha256": "fixed"})), \
             patch.object(dev_mcp, "source_hash", return_value="changed"), \
             patch.object(dev_install, "probe") as probe_mock:
            with self.assertRaisesRegex(RuntimeError, "changed during the MCP probe build"):
                dev_install.probe_mcp(Path("/fixture"))
        probe_mock.assert_not_called()

    def test_probe_mcp_rejects_a_source_change_during_the_probe_itself(self):
        hashes = iter(["fixed", "changed"])
        with patch.object(dev_mcp, "build", return_value=(Path("/fixture/praetor-mcp"), {"source_sha256": "fixed"})), \
             patch.object(dev_mcp, "source_hash", side_effect=lambda: next(hashes)), \
             patch.object(dev_install, "probe", return_value={"passed": []}):
            with self.assertRaisesRegex(RuntimeError, "changed during the MCP probe;"):
                dev_install.probe_mcp(Path("/fixture"))


class HookInterpreterTests(unittest.TestCase):
    """settle_hook_interpreter decides what the Git hooks start on this host (#339). The
    per-user store is injected, so no case writes a user environment; the interpreter check
    is injected where the decision is under test and real where the check itself is."""

    setx_status = 0

    def settle(self, environ, faults, platform="linux", fault=None):
        """Settle with faults naming why a program cannot run the hooks; any other one starts."""
        stored = []

        def run(argv, **settings):
            stored.append((argv, settings["timeout"]))
            return subprocess.CompletedProcess(argv, self.setx_status, b"", b"access denied\n")

        def store(variable, value, reason):
            dev_install.store_user_variable(variable, value, reason, platform=platform, run=run)

        report = dev_install.settle_hook_interpreter(environ, fault=fault or faults.get, store=store)
        return report, stored

    def program(self, name, body):
        """A POSIX program on a PATH of its own; return a PATH lookup that sees only it."""
        temporary = tempfile.TemporaryDirectory(prefix="praetor-interpreter-")
        self.addCleanup(temporary.cleanup)
        path = Path(temporary.name) / name
        path.write_text("#!/bin/sh\n" + body, newline="\n")
        path.chmod(0o755)
        return path, lambda program: shutil.which(program, path=temporary.name)

    def test_spelling_is_read_from_the_hook_policy(self):
        self.assertEqual(dev_install.hook_interpreter_spelling(), ("PRAETOR_PYTHON", "python3"))
        self.assertNotIn(str(dev_mcp.ROOT / ".config" / "lefthook" / "scripts"), sys.path)
        launcher = (dev_mcp.ROOT / ".config/lefthook/python.sh").read_text(encoding="utf-8")
        self.assertIn('"${PRAETOR_PYTHON:-python3}"', launcher)

    def test_default_name_that_starts_needs_nothing_stored(self):
        report, stored = self.settle({}, {})
        self.assertEqual(report, {"variable": "PRAETOR_PYTHON", "value": "python3", "source": "default"})
        self.assertEqual(stored, [])
        # Boundary: an empty variable is unset, as python.sh reads it.
        report, stored = self.settle({"PRAETOR_PYTHON": ""}, {})
        self.assertEqual((report["source"], stored), ("default", []))

    def test_a_set_variable_is_kept_when_it_starts_and_refused_when_it_does_not(self):
        report, stored = self.settle({"PRAETOR_PYTHON": "py"}, {"python3": "python3 is not on PATH"})
        self.assertEqual(report, {"variable": "PRAETOR_PYTHON", "value": "py", "source": "environment"})
        self.assertEqual(stored, [])
        # A set variable is never replaced: not where the default would start, and not on
        # the platform that has a store.
        for platform in ("linux", "win32"):
            with self.subTest(platform=platform), self.assertRaisesRegex(
                    RuntimeError, "PRAETOR_PYTHON names an interpreter the Git hooks cannot "
                                  "run: py is not on PATH; unset it"):
                self.settle({"PRAETOR_PYTHON": "py"}, {"py": "py is not on PATH"}, platform=platform)

    def test_windows_without_python3_stores_the_running_interpreter(self):
        reason = "python3 is not on PATH"
        report, stored = self.settle({}, {"python3": reason}, platform="win32")
        executable = sys.executable
        self.assertEqual(report, {"variable": "PRAETOR_PYTHON", "value": executable,
                                  "source": "stored", "reason": reason})
        self.assertEqual(stored, [(["setx", "PRAETOR_PYTHON", executable], dev_install.SETX_TIMEOUT)])

    def test_a_python3_on_path_that_is_no_interpreter_is_not_taken_for_the_default(self):
        # The injected check stands for the Microsoft Store alias on every platform: the
        # name resolves, the program is no interpreter. A lookup alone reported "default".
        reason = "python3 does not start as Python 3 (python3.exe -V exited 9009)"
        report, stored = self.settle({}, {"python3": reason}, platform="win32")
        self.assertEqual((report["source"], report["value"], report["reason"]),
                         ("stored", sys.executable, reason))
        self.assertEqual(len(stored), 1)
        with self.assertRaisesRegex(RuntimeError, r"exited 9009\), so the Git hooks cannot start"
                                                  ".*export PRAETOR_PYTHON="):
            self.settle({}, {"python3": reason}, platform="linux")

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_the_store_alias_is_started_and_found_out(self):
        # What the alias does when a hook starts it: a message, and a status of its own.
        alias, which = self.program(
            "python3", 'echo "Python was not found; run without arguments to install from the '
                       'Microsoft Store" >&2\nexit 49\n')
        self.assertEqual(which("python3"), str(alias))
        report, stored = self.settle(
            {}, {}, platform="win32", fault=lambda name: dev_install.interpreter_fault(name, which))
        self.assertEqual((report["source"], report["value"]), ("stored", sys.executable))
        self.assertRegex(report["reason"], r"\Apython3 does not start as Python 3 \(.*python3 -V "
                                           r"exited 49 Python was not found; .*\)\Z")
        self.assertEqual(stored, [(["setx", "PRAETOR_PYTHON", sys.executable],
                                   dev_install.SETX_TIMEOUT)])
        with self.assertRaisesRegex(RuntimeError, "PRAETOR_PYTHON names an interpreter the Git "
                                                  "hooks cannot run: python3 does not start"):
            self.settle({"PRAETOR_PYTHON": "python3"}, {}, platform="win32",
                        fault=lambda name: dev_install.interpreter_fault(name, which))

    def test_interpreter_fault_starts_the_program_it_names(self):
        # Positive, on every platform: the interpreter running this suite, by path and by a
        # name the lookup resolves to it.
        self.assertIsNone(dev_install.interpreter_fault(sys.executable))
        self.assertIsNone(dev_install.interpreter_fault("python3", which=lambda _: sys.executable))
        # Negative: nothing to start. A name is looked up; a path is the file itself.
        self.assertEqual(dev_install.interpreter_fault("praetor-no-such-interpreter"),
                         "praetor-no-such-interpreter is not on PATH")
        gone = str(Path(tempfile.gettempdir()) / "praetor-no-such-directory" / "python")
        self.assertEqual(dev_install.interpreter_fault(gone), f"{gone} is not an executable file")
        # Negative: the lookup resolves to a file the platform cannot start.
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temporary:
            self.assertRegex(dev_install.interpreter_fault("python3", which=lambda _: temporary),
                             r"\Apython3 does not start as Python 3 \(")

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_interpreter_fault_reads_what_the_program_states(self):
        # Boundary: the stated version decides, not the name and not a zero status.
        for body, starts in (('echo "Python 3.13.1"\n', True),
                             ('echo "Python 2.7.18"\n', False),
                             ('echo "Python 2.7.18" >&2\n', False),
                             ("exit 0\n", False),
                             ('echo "Python 3.13.1"\nexit 1\n', False)):
            with self.subTest(body=body):
                _, which = self.program("python3", body)
                fault = dev_install.interpreter_fault("python3", which)
                self.assertEqual(fault is None, starts, fault)
        # Boundary: a long failure is quoted up to the bound, on one line.
        _, which = self.program("python3", 'yes "not an interpreter" | head -n 500 >&2\nexit 3\n')
        fault = dev_install.interpreter_fault("python3", which)
        self.assertNotIn("\n", fault)
        self.assertEqual(len(fault), len("python3 does not start as Python 3 ()")
                         + dev_install.MAX_FAULT_DETAIL)

    def test_a_failed_store_and_a_platform_without_one_fail_the_install(self):
        self.setx_status = 1
        missing = {"python3": "python3 is not on PATH"}
        with self.assertRaisesRegex(RuntimeError, "setx PRAETOR_PYTHON failed: access denied"):
            self.settle({}, missing, platform="win32")
        with self.assertRaisesRegex(RuntimeError, "python3 is not on PATH, so the Git hooks cannot "
                                                  "start.*export PRAETOR_PYTHON="):
            self.settle({}, missing, platform="linux")

    def test_main_reports_the_interpreter_and_settles_it_before_installing(self):
        order = []
        settled = {"variable": "PRAETOR_PYTHON", "value": "python3", "source": "default"}
        with patch.object(dev_install, "settle_hook_interpreter",
                          side_effect=lambda: order.append("settle") or settled), \
             patch.object(dev_install, "probe_mcp", side_effect=lambda _: order.append("probe")), \
             patch.object(dev_install, "install",
                          side_effect=lambda *_: order.append("install") or {"manifest_path": "/x"}), \
             patch("sys.argv", ["dev_install.py"]), patch("builtins.print") as printed:
            dev_install.main()
        self.assertEqual(order, ["settle", "probe", "install"])
        self.assertEqual(json.loads(printed.call_args.args[0]),
                         {"manifest_path": "/x", "hook_interpreter": settled})
        with patch.object(dev_install, "settle_hook_interpreter", side_effect=RuntimeError("no")), \
             patch.object(dev_install, "install") as install, patch("sys.argv", ["dev_install.py"]):
            with self.assertRaises(RuntimeError):
                dev_install.main()
        install.assert_not_called()


if __name__ == "__main__":
    unittest.main()
