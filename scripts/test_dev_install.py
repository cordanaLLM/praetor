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
POSIX_STUB = ("a stand-in program is a shebang script, which the Windows loader does not "
              "start; the injected-lookup cases cover the same decisions on this platform")

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
    """hook_interpreter reports what the Git hooks start on this host and stores nothing (#339).

    The PATH lookup is injected, so a case decides which candidates exist without touching
    the process environment; the probe itself is real and starts the program it is given.
    """

    def stand_in(self, name, body, directory=None):
        """A POSIX program in a directory of its own, or beside an earlier one."""
        if directory is None:
            temporary = tempfile.TemporaryDirectory(prefix="praetor-interpreter-")
            self.addCleanup(temporary.cleanup)
            directory = Path(temporary.name)
        path = directory / name
        path.write_text("#!/bin/sh\n" + body, newline="\n")
        path.chmod(0o755)
        return path

    @staticmethod
    def only(directory):
        """A PATH lookup that sees directory alone."""
        return lambda program: shutil.which(program, path=str(directory))

    def test_the_interpreter_of_this_host_is_reported_and_nothing_is_stored(self):
        before = dict(os.environ)
        report = dev_install.hook_interpreter()
        self.assertEqual(sorted(report), ["command", "version"])
        self.assertTrue(Path(report["command"][0]).is_file(), report)
        self.assertRegex(report["version"], r"^3\.\d+$")
        self.assertEqual(dict(os.environ), before)
        self.assertNotIn(str(dev_mcp.ROOT / ".config" / "lefthook" / "scripts"), sys.path)
        source = (dev_mcp.ROOT / "scripts" / "dev_install.py").read_text(encoding="utf-8")
        for store in ("setx", "PRAETOR_PYTHON", "environ["):
            self.assertNotIn(store, source)

    def test_the_candidates_are_the_hook_policys_in_its_order(self):
        asked = []

        def which(program):
            asked.append(program)
            return sys.executable if program == "python" else None
        report = dev_install.hook_interpreter(which)
        self.assertEqual(report, {"command": [sys.executable],
                                  "version": "%d.%d" % sys.version_info[:2]})
        # Boundary: the lookup stops at the first candidate that proves itself.
        self.assertEqual(asked, ["python3", "python"])
        launcher = (dev_mcp.ROOT / ".config/lefthook/python.sh").read_text(encoding="utf-8")
        for candidate in ("python3", "python", "py -3"):
            self.assertIn(f"if proven {candidate}; then exec {candidate} ", launcher)

    def test_a_host_without_an_interpreter_stops_the_install(self):
        with self.assertRaisesRegex(
                RuntimeError, r"the Git hooks cannot start on this host: missing dependency: "
                              r"a Python 3\.\d+ or newer interpreter\. Tried: python3 \(not on "
                              r"PATH\); python \(not on PATH\); py -3 \(not on PATH\)"):
            dev_install.hook_interpreter(lambda _program: None)
        # Negative: a lookup that resolves to a file the platform cannot start is no proof.
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temporary:
            with self.assertRaisesRegex(RuntimeError, r"Tried: python3 \(.+\); python \("):
                dev_install.hook_interpreter(lambda _program: temporary)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_a_program_that_exits_zero_without_being_python_is_refused(self):
        quiet = self.stand_in("python3", "exit 0\n")
        with self.assertRaisesRegex(RuntimeError, r"python3 \(does not answer the version probe "
                                                  r"as Python\); python \(not on PATH\)"):
            dev_install.hook_interpreter(self.only(quiet.parent))
        # Boundary: what the program states decides, not its name: a release below the
        # floor, Python 2 and the answer of -V are each refused.
        for answer in ("3.9", "2.7", "Python 3.13.1"):
            with self.subTest(answer=answer):
                old = self.stand_in("python3", f'echo "{answer}"\n')
                with self.assertRaisesRegex(RuntimeError, "missing dependency"):
                    dev_install.hook_interpreter(self.only(old.parent))

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_the_store_alias_is_found_out_and_the_next_candidate_is_reported(self):
        # What the alias does when a hook starts it: a message, and a status of its own.
        alias = ('echo "Python was not found; run without arguments to install from the '
                 'Microsoft Store" >&2\nexit 49\n')
        python3 = self.stand_in("python3", alias)
        with self.assertRaisesRegex(RuntimeError, r"python3 \(.*exited 49 Python was not found; "
                                                  r".*\); python \(not on PATH\)"):
            dev_install.hook_interpreter(self.only(python3.parent))
        python = self.stand_in("python", 'echo "3.13"\n', python3.parent)
        self.assertEqual(dev_install.hook_interpreter(self.only(python3.parent)),
                         {"command": [str(python)], "version": "3.13"})

    def test_main_reports_the_interpreter_and_checks_it_before_installing(self):
        order = []
        resolved = {"command": ["/usr/bin/python3"], "version": "3.13"}
        with patch.object(dev_install, "hook_interpreter",
                          side_effect=lambda: order.append("interpreter") or resolved), \
             patch.object(dev_install, "probe_mcp", side_effect=lambda _: order.append("probe")), \
             patch.object(dev_install, "install",
                          side_effect=lambda *_: order.append("install") or {"manifest_path": "/x"}), \
             patch("sys.argv", ["dev_install.py"]), patch("builtins.print") as printed:
            dev_install.main()
        self.assertEqual(order, ["interpreter", "probe", "install"])
        self.assertEqual(json.loads(printed.call_args.args[0]),
                         {"manifest_path": "/x", "hook_interpreter": resolved})
        with patch.object(dev_install, "hook_interpreter", side_effect=RuntimeError("no")), \
             patch.object(dev_install, "install") as install, patch("sys.argv", ["dev_install.py"]):
            with self.assertRaises(RuntimeError):
                dev_install.main()
        install.assert_not_called()


if __name__ == "__main__":
    unittest.main()
