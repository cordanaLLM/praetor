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
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import dev_install
import dev_mcp

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


if __name__ == "__main__":
    unittest.main()
