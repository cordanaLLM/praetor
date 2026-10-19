#!/usr/bin/env python3
"""The cloud session bootstrap provisions lefthook and praetorctl in a cloud VM, and nowhere else."""

import importlib.util
import json
import os
from pathlib import Path
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(".config/agent/hooks/cloud_bootstrap.py")
SPEC = importlib.util.spec_from_file_location("cloud_bootstrap", ROOT / SCRIPT)
BOOTSTRAP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BOOTSTRAP)
REMOTE = {"CLAUDE_CODE_REMOTE": "true", "PATH": "/usr/local/bin:/usr/bin"}


class Which:
    """shutil.which as a cloud VM answers it: go always, each tool only once a step installed it."""

    def __init__(self, installed=()):
        self.installed = set(installed)

    def __call__(self, name):
        if name == "go" or name in self.installed:
            return f"/usr/local/bin/{name}"
        return None


class CloudBootstrap(unittest.TestCase):
    def run_bootstrap(self, which, steps=None, env=REMOTE, gosum="https://sum.golang.org"):
        commands = []

        def step(command, _timeout, _extra):
            commands.append(command)
            if steps is not None:
                return steps(command, which)
            which.installed.add("lefthook" if command[1] == "install" else "praetorctl")
            return ""
        values = {"GOSUMDB": gosum, "GONOSUMDB": "", "GOINSECURE": ""}
        with mock.patch.object(BOOTSTRAP.shutil, "which", which), \
                mock.patch.object(BOOTSTRAP, "run_step", step), \
                mock.patch.object(BOOTSTRAP, "go_env", values.get), \
                mock.patch.object(BOOTSTRAP, "target_dir", lambda _path: Path("/usr/local/bin")):
            return BOOTSTRAP.bootstrap(env), commands

    def test_outside_a_cloud_session_does_nothing(self):
        line, commands = self.run_bootstrap(Which(), env={"PATH": "/usr/bin"})
        self.assertIsNone(line)
        self.assertEqual(commands, [])
        line, commands = self.run_bootstrap(Which(), env={"CLAUDE_CODE_REMOTE": "false", "PATH": "/usr/bin"})
        self.assertIsNone(line, "only the literal true marks a cloud session")
        self.assertEqual(commands, [])

    def test_installs_the_pins_and_initializes_the_ledger(self):
        line, commands = self.run_bootstrap(Which())
        self.assertEqual(line, "ready in /usr/local/bin")
        self.assertEqual(commands[0], ["go", "install", BOOTSTRAP.LEFTHOOK])
        self.assertEqual(commands[1], ["go", "build", "-o", "/usr/local/bin/praetorctl", "./cmd/standardsctl"])
        self.assertEqual(commands[2], ["/usr/local/bin/praetorctl", "state", "init", "--if-absent", "."])
        self.assertIn("lefthook/v2@v2.1.14", BOOTSTRAP.LEFTHOOK)

    def test_tools_present_skip_the_installs(self):
        line, commands = self.run_bootstrap(Which({"lefthook", "praetorctl"}))
        self.assertEqual(line, "ready in /usr/local/bin")
        self.assertEqual([c[1] for c in commands], ["state"])

    def test_negative_checksum_verification_off_refuses(self):
        line, commands = self.run_bootstrap(Which(), gosum="off")
        self.assertIn("checksum verification is off", line)
        self.assertEqual(commands, [], "nothing is installed unverified")

    def test_negative_failed_build_keeps_the_hooks_closed(self):
        def steps(command, which):
            if command[1] == "install":
                which.installed.add("lefthook")
                return ""
            return "go build exit 1: compile error"
        line, commands = self.run_bootstrap(Which(), steps=steps)
        self.assertIn("missing praetorctl; the engine hooks stay closed", line)
        self.assertIn("go build exit 1", line)
        self.assertNotIn("state", [c[1] for c in commands], "no ledger step without praetorctl")

    def test_negative_no_go_toolchain(self):
        line, commands = self.run_bootstrap(lambda name: None)
        self.assertEqual(line, "no go toolchain; the engine hooks stay closed")
        self.assertEqual(commands, [])

    def test_boundary_install_directory_must_be_on_path_and_writable(self):
        with mock.patch.object(BOOTSTRAP.os, "access", lambda _p, _m: True), \
                mock.patch.object(Path, "is_dir", lambda self: True):
            self.assertEqual(BOOTSTRAP.target_dir("/usr/local/bin:/usr/bin"), Path("/usr/local/bin"))
            self.assertIsNone(BOOTSTRAP.target_dir("/usr/bin"), "a directory off PATH is never used")
            self.assertIsNone(BOOTSTRAP.target_dir(""))
        with mock.patch.object(BOOTSTRAP.os, "access", lambda _p, _m: False), \
                mock.patch.object(Path, "is_dir", lambda self: True):
            self.assertIsNone(BOOTSTRAP.target_dir("/usr/local/bin"), "a read-only directory is never used")

    def test_registered_for_claude_session_start_only(self):
        hooks = json.loads((ROOT / ".claude/settings.json").read_text(encoding="utf-8"))["hooks"]
        groups = hooks["SessionStart"]
        self.assertEqual(len(groups), 1)
        self.assertEqual(groups[0]["matcher"], "startup|resume")
        action = groups[0]["hooks"][0]
        self.assertEqual([action["command"], *action["args"]],
                         ["python3", "-B", "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/cloud_bootstrap.py"])
        self.assertGreaterEqual(action["timeout"], BOOTSTRAP.BOOTSTRAP_LIMIT + 2)
        self.assertLessEqual(BOOTSTRAP.BOOTSTRAP_BUDGET + BOOTSTRAP.BOOTSTRAP_STOP, BOOTSTRAP.BOOTSTRAP_LIMIT)

    def test_main_prints_one_line_and_never_blocks(self):
        with mock.patch.object(BOOTSTRAP, "bootstrap", lambda _env: "ready in /usr/local/bin"), \
                mock.patch.object(BOOTSTRAP.sys, "stdout") as out:
            self.assertEqual(BOOTSTRAP.main({}), 0)
        out.write.assert_called_once_with("Praetor cloud session bootstrap: ready in /usr/local/bin\n")
        with mock.patch.object(BOOTSTRAP, "bootstrap", lambda _env: None), \
                mock.patch.object(BOOTSTRAP.sys, "stdout") as out:
            self.assertEqual(BOOTSTRAP.main({}), 0)
        out.write.assert_not_called()


if __name__ == "__main__":
    os.environ.pop("CLAUDE_CODE_REMOTE", None)
    unittest.main()
