#!/usr/bin/env python3
"""The cloud session bootstrap provisions lefthook and praetorctl in a cloud VM, and nowhere else."""

import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(".config/agent/hooks/cloud_bootstrap.py")
SPEC = importlib.util.spec_from_file_location("cloud_bootstrap", ROOT / SCRIPT)
BOOTSTRAP = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BOOTSTRAP)
REMOTE = {"CLAUDE_CODE_REMOTE": "true", "PATH": os.pathsep.join(["/usr/local/bin", "/usr/bin"])}
WORKFLOWS = ("ci.yml", "portability.yml")


def lefthook_pin_problems(root):
    """What breaks the one lefthook pin under root: go.mod in tools/lefthook holds the tool directive and names one
    version, go.sum verifies that version, and the bootstrap and the workflows build or install from that module with no
    second pin. The version is read from go.mod and never written here, so a lefthook update needs no edit of this test
    (order engine-bundle-20261026112535-lefthook-pin: a pinned v2.1.14 refused the update to v2.2.2)."""
    module_dir = Path(root) / BOOTSTRAP.LEFTHOOK_MODULE
    module = (module_dir / "go.mod").read_text(encoding="utf-8")
    problems = [] if f"tool {BOOTSTRAP.LEFTHOOK}\n" in module else ["go.mod holds no tool directive for lefthook"]
    versions = re.findall(rf"^\s*{re.escape(BOOTSTRAP.LEFTHOOK)} (v\S+)", module, re.M)
    if len(versions) != 1:
        return problems + [f"go.mod names {len(versions)} lefthook versions, not one"]
    if f"{BOOTSTRAP.LEFTHOOK} {versions[0]} h1:" not in (module_dir / "go.sum").read_text(encoding="utf-8"):
        problems.append(f"go.sum does not verify lefthook {versions[0]}")
    sources = [(str(SCRIPT), Path(root) / SCRIPT)] + [(w, Path(root) / ".github/workflows" / w) for w in WORKFLOWS]
    for name, path in sources:
        text = path.read_text(encoding="utf-8")
        if name in WORKFLOWS and f"go install -C {BOOTSTRAP.LEFTHOOK_MODULE} {BOOTSTRAP.LEFTHOOK}\n" not in text:
            problems.append(f"{name} does not install lefthook from {BOOTSTRAP.LEFTHOOK_MODULE}")
        if f"{BOOTSTRAP.LEFTHOOK}@" in text:
            problems.append(f"{name} keeps a second lefthook pin")
    return problems


def plant_lefthook_pin(root, version, script_extra="", workflow_extra=""):
    """A minimal tree for lefthook_pin_problems: go.mod and go.sum at version, the bootstrap, and both workflows."""
    module_dir = Path(root) / BOOTSTRAP.LEFTHOOK_MODULE
    module_dir.mkdir(parents=True, exist_ok=True)
    (module_dir / "go.mod").write_text(f"module x\n\ntool {BOOTSTRAP.LEFTHOOK}\n\nrequire (\n"
                                       f"\t{BOOTSTRAP.LEFTHOOK} {version} // indirect\n)\n", encoding="utf-8")
    (module_dir / "go.sum").write_text(f"{BOOTSTRAP.LEFTHOOK} {version} h1:x=\n", encoding="utf-8")
    (Path(root) / SCRIPT).parent.mkdir(parents=True, exist_ok=True)
    (Path(root) / SCRIPT).write_text(f'LEFTHOOK = "{BOOTSTRAP.LEFTHOOK}"\n' + script_extra, encoding="utf-8")
    (Path(root) / ".github/workflows").mkdir(parents=True, exist_ok=True)
    for workflow in WORKFLOWS:
        (Path(root) / ".github/workflows" / workflow).write_text(
            f"run: go install -C {BOOTSTRAP.LEFTHOOK_MODULE} {BOOTSTRAP.LEFTHOOK}\n" + workflow_extra, encoding="utf-8")


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

        def step(name, command, _timeout):
            commands.append(command)
            if steps is not None:
                return steps(name, which)
            which.installed.add(name.split()[0])
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
        self.assertEqual(commands[0], ["go", "build", "-C", "tools/lefthook", "-mod=readonly", "-o",
                                       "/usr/local/bin/lefthook", "github.com/evilmartians/lefthook/v2"])
        self.assertEqual(commands[1], ["go", "build", "-mod=readonly", "-o", "/usr/local/bin/praetorctl",
                                       "./cmd/standardsctl"])
        self.assertEqual(commands[2], ["/usr/local/bin/praetorctl", "state", "init", "--if-absent", "."])

    def test_lefthook_pin_is_the_module_ci_installs_from(self):
        self.assertEqual(lefthook_pin_problems(ROOT), [])

    def test_lefthook_pin_follows_the_module_version(self):
        with tempfile.TemporaryDirectory(prefix="praetor-lefthook-pin-") as temp:
            root = Path(temp)
            plant_lefthook_pin(root, "v9.9.9")
            self.assertEqual(lefthook_pin_problems(root), [], "any version go.mod and go.sum agree on passes")
            (root / BOOTSTRAP.LEFTHOOK_MODULE / "go.sum").write_text(
                f"{BOOTSTRAP.LEFTHOOK} v2.1.14 h1:x=\n", encoding="utf-8")
            self.assertEqual(lefthook_pin_problems(root), ["go.sum does not verify lefthook v9.9.9"])
            plant_lefthook_pin(root, "v9.9.9", script_extra=f'PIN = "{BOOTSTRAP.LEFTHOOK}@v2.2.2"\n',
                               workflow_extra=f"go install {BOOTSTRAP.LEFTHOOK}@v2.2.2\n")
            self.assertEqual(lefthook_pin_problems(root), [
                f"{SCRIPT} keeps a second lefthook pin", "ci.yml keeps a second lefthook pin",
                "portability.yml keeps a second lefthook pin"])

    def test_refused_hosts_are_named(self):
        stderr = ('go: verifying module: Get "https://sum.golang.org/lookup/x@v1": Forbidden\n'
                  'go: Get "https://storage.googleapis.com/proxy-golang-org-prod/a.zip?X-Sig=abc": Forbidden\n'
                  'go: Get "https://sum.golang.org/lookup/y@v2": Forbidden\n')
        self.assertEqual(BOOTSTRAP.refused_hosts(stderr), ["sum.golang.org", "storage.googleapis.com"])
        self.assertEqual(BOOTSTRAP.refused_hosts('go: Get "https://proxy.golang.org/x": dial tcp: timeout'), [],
                         "only a refusal names a host")
        self.assertEqual(BOOTSTRAP.refused_hosts(""), [])
        result = mock.Mock(returncode=1, stderr=stderr + "x" * 400)
        with mock.patch.object(BOOTSTRAP.subprocess, "run", lambda *_a, **_k: result):
            line = BOOTSTRAP.run_step("praetorctl build", ["go", "build"], 1)
        self.assertTrue(line.startswith("praetorctl build exit 1 (the network refuses sum.golang.org, "
                                        "storage.googleapis.com; allow it in the cloud environment): "), line)

    def test_tools_present_skip_the_installs(self):
        line, commands = self.run_bootstrap(Which({"lefthook", "praetorctl"}))
        self.assertEqual(line, "ready in /usr/local/bin")
        self.assertEqual([c[1] for c in commands], ["state"])

    def test_negative_checksum_verification_off_refuses(self):
        line, commands = self.run_bootstrap(Which(), gosum="off")
        self.assertIn("checksum verification is off", line)
        self.assertEqual(commands, [], "nothing is installed unverified")

    def test_negative_failed_build_keeps_the_hooks_closed(self):
        def steps(name, which):
            if name == "lefthook build":
                which.installed.add("lefthook")
                return ""
            return "praetorctl build exit 1: compile error"
        line, commands = self.run_bootstrap(Which(), steps=steps)
        self.assertIn("missing praetorctl; the engine hooks stay closed", line)
        self.assertIn("praetorctl build exit 1", line)
        self.assertNotIn("state", [c[1] for c in commands], "no ledger step without praetorctl")

    def test_negative_no_go_toolchain(self):
        line, commands = self.run_bootstrap(lambda name: None)
        self.assertEqual(line, "no go toolchain; the engine hooks stay closed")
        self.assertEqual(commands, [])

    def test_boundary_install_directory_must_be_on_path_and_writable(self):
        with mock.patch.object(BOOTSTRAP.os, "access", lambda _p, _m: True), \
                mock.patch.object(Path, "is_dir", lambda self: True):
            self.assertEqual(BOOTSTRAP.target_dir(os.pathsep.join(["/usr/local/bin", "/usr/bin"])), Path("/usr/local/bin"))
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
