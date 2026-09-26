#!/usr/bin/env python3
"""The tracked hook launcher never lets a skewed or missing engine block a client."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(".config/agent/hooks/praetor_hook.py")
SPEC = importlib.util.spec_from_file_location("praetor_hook", ROOT / SCRIPT)
LAUNCHER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LAUNCHER)
EXE = ".exe" if os.name == "nt" else ""
PAIR = ["claude", "pre-dispatch"]
# The pair list of the engine installed on the measured host (f41e74d8), which predates the
# subagent rows: its `praetorctl hook` output, verbatim.
OLD_LISTING = b"""praetor hook: unsupported hook client or event: arguments must match ^[a-z-]+$
usage: praetorctl hook <client> <event>
  praetorctl hook claude pre-tool
  praetorctl hook claude pre-edit
  praetorctl hook claude post-tool
  praetorctl hook claude stop
  praetorctl hook codex pre-tool
  praetorctl hook codex post-tool
  praetorctl hook codex stop
  praetorctl hook gemini pre-tool
  praetorctl hook gemini pre-edit
  praetorctl hook gemini post-tool
  praetorctl hook gemini stop
  praetorctl hook lefthook pre-tool
  praetorctl hook lefthook environment
  praetorctl hook agy pre-tool
  praetorctl hook agy stop
"""
NEW_LISTING = OLD_LISTING + b"  praetorctl hook claude pre-dispatch\n  praetorctl hook codex post-return\n"


class FakeEngines:
    """subprocess.run stand-in: each engine path answers the probe with its listing and a
    served call with its exit code; every call is recorded."""

    def __init__(self, listings, exits=None, errors=None):
        self.listings, self.exits, self.errors, self.calls = listings, exits or {}, errors or {}, []

    def __call__(self, argv, **kwargs):
        self.calls.append(list(argv))
        error = self.errors.get((argv[0], len(argv)))
        if error is not None:
            raise error
        if argv[1:] == ["hook"]:
            return subprocess.CompletedProcess(argv, 2, b"", self.listings[argv[0]])
        return subprocess.CompletedProcess(argv, self.exits.get(argv[0], 0))

    def served(self):
        return [call for call in self.calls if len(call) == 4]


class Launcher(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="praetor-launcher-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.local = str(self.root / "bin" / ("praetorctl" + EXE))
        self.installed = str(self.root / "installed" / ("praetorctl" + EXE))

    def build_local(self):
        Path(self.local).parent.mkdir()
        Path(self.local).write_bytes(b"")

    def launch(self, argv, engines, installed=True, stdin=b"{}"):
        which = (lambda name: self.installed) if installed else (lambda name: None)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            code = LAUNCHER.main(argv, root=self.root, which=which, run=engines, stdin=io.BytesIO(stdin))
        return code, stderr.getvalue()

    def test_checkout_engine_that_serves_the_pair_answers_with_its_own_exit(self):
        self.build_local()
        for exit_code in (0, 2):
            engines = FakeEngines({self.local: NEW_LISTING, self.installed: NEW_LISTING},
                                  exits={self.local: exit_code})
            code, _ = self.launch(PAIR, engines)
            self.assertEqual(code, exit_code)
            self.assertEqual(engines.served(), [[self.local, "hook", *PAIR]])

    def test_stale_checkout_engine_falls_back_to_the_installed_one(self):
        self.build_local()
        engines = FakeEngines({self.local: OLD_LISTING, self.installed: NEW_LISTING},
                              exits={self.installed: 2})
        code, _ = self.launch(PAIR, engines)
        self.assertEqual(code, 2)
        self.assertEqual(engines.served(), [[self.installed, "hook", *PAIR]])

    def test_engine_older_than_the_row_is_never_handed_the_call(self):
        engines = FakeEngines({self.installed: OLD_LISTING})
        code, stderr = self.launch(PAIR, engines)
        self.assertEqual(code, 0)
        self.assertEqual(engines.served(), [])
        self.assertIn("no engine serves claude pre-dispatch (checked " + self.installed, stderr)
        self.assertIn("make dev-install", stderr)
        self.assertTrue(stderr.endswith(", skipped\n"), stderr)

    def test_missing_engine_is_a_stated_skip(self):
        stdin = io.BytesIO(b'{"payload": true}')
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            code = LAUNCHER.main(PAIR, root=self.root, which=lambda name: None, run=FakeEngines({}), stdin=stdin)
        self.assertEqual(code, 0)
        self.assertIn("no praetorctl is built or installed", stderr.getvalue())
        self.assertEqual(stdin.read(), b"", "the unread payload must be drained")

    def test_malformed_registration_fails_closed_without_probing(self):
        for argv in ([], ["claude"], ["Claude", "pre-dispatch"], ["claude", "pre dispatch"],
                     ["claude", "pre-dispatch", "--x"], ["claude", ""], ["claude", "pre-dispatch\n"]):
            with self.subTest(argv=argv):
                engines = FakeEngines({self.installed: NEW_LISTING})
                code, stderr = self.launch(argv, engines)
                self.assertEqual(code, 2)
                self.assertIn("usage: praetor_hook.py <client> <event>", stderr)
                self.assertEqual(engines.calls, [])

    def test_only_an_exact_listing_line_counts(self):
        for listing in (b"  praetorctl hook claude pre-dispatch-x\n", b"  praetorctl hook claude pre-dispatch --x\n",
                        b"praetorctl hook claude\n", b"  praetorctl hook codex pre-dispatch\n",
                        b"x" * LAUNCHER.PROBE_LIMIT + b"\n  praetorctl hook claude pre-dispatch\n"):
            with self.subTest(listing=listing[-48:]):
                self.assertFalse(LAUNCHER.serves(self.installed, PAIR, FakeEngines({self.installed: listing})))
        self.assertTrue(LAUNCHER.serves(self.installed, PAIR, FakeEngines({self.installed: b"praetorctl hook claude pre-dispatch"})))

    def test_probe_failures_mean_not_served(self):
        for error in (subprocess.TimeoutExpired("praetorctl", LAUNCHER.PROBE_TIMEOUT), PermissionError("denied")):
            with self.subTest(error=type(error).__name__):
                engines = FakeEngines({}, errors={(self.installed, 2): error})
                code, stderr = self.launch(PAIR, engines)
                self.assertEqual(code, 0)
                self.assertEqual(engines.served(), [])
                self.assertIn("skipped", stderr)

    def test_engine_that_hangs_or_vanishes_is_a_fault_not_a_block(self):
        for error in (subprocess.TimeoutExpired("praetorctl", LAUNCHER.RUN_TIMEOUT), FileNotFoundError("gone")):
            with self.subTest(error=type(error).__name__):
                engines = FakeEngines({self.installed: NEW_LISTING}, errors={(self.installed, 4): error})
                code, stderr = self.launch(PAIR, engines)
                self.assertEqual(code, 1)
                self.assertIn("praetor hook: " + self.installed, stderr)

    def test_one_engine_reached_twice_is_probed_once(self):
        self.build_local()
        engines = FakeEngines({self.local: OLD_LISTING})
        with contextlib.redirect_stderr(io.StringIO()):
            code = LAUNCHER.main(PAIR, root=self.root, which=lambda name: self.local, run=engines, stdin=io.BytesIO())
        self.assertEqual(code, 0)
        self.assertEqual(engines.calls, [[self.local, "hook"]])

    def test_drain_is_bounded_in_size_and_time(self):
        stream = io.BytesIO(b"x" * (LAUNCHER.DRAIN_LIMIT + 10))
        LAUNCHER.drain(stream)
        self.assertEqual(stream.tell(), LAUNCHER.DRAIN_LIMIT)
        blocked = threading.Event()

        class Stuck:
            def read(self, limit):
                blocked.wait(5)
                return b""

        LAUNCHER.drain(Stuck(), timeout=0.05)
        blocked.set()


class TrackedRegistrations(unittest.TestCase):
    """The tracked strings, run through a POSIX shell the way the clients run them, against
    the engine built from this tree and against a stand-in for the engine that predates it."""

    @classmethod
    def setUpClass(cls):
        cls.shell = shutil.which("sh")
        if os.name == "nt" or cls.shell is None:
            raise unittest.SkipTest("the tracked strings use POSIX substitution; Windows clients run "
                                    "them through Git Bash, which this runner does not provide")
        cls.build = tempfile.TemporaryDirectory(prefix="praetor-launcher-cli-")
        cls.addClassCleanup(cls.build.cleanup)
        cls.engine = Path(cls.build.name) / ("praetorctl" + EXE)
        subprocess.run(["go", "build", "-o", str(cls.engine), "./cmd/standardsctl"],
                       cwd=ROOT, capture_output=True, timeout=300, check=True)

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="praetor-launcher-tree-")
        self.addCleanup(temp.cleanup)
        self.tree = Path(temp.name) / "checkout"
        (self.tree / SCRIPT.parent).mkdir(parents=True)
        shutil.copy(ROOT / SCRIPT, self.tree / SCRIPT)
        subprocess.run(["git", "init", "-q", str(self.tree)], capture_output=True, timeout=20, check=True)
        self.stubs = Path(temp.name) / "stubs"
        self.stubs.mkdir()
        self.log = Path(temp.name) / "old-engine.log"
        tools = {str(Path(shutil.which(name)).parent) for name in ("python3", "git")}
        if any((Path(directory) / "praetorctl").exists() for directory in tools):
            self.skipTest("python3 or git shares a directory with an installed praetorctl")
        self.path = os.pathsep.join([str(self.stubs), *sorted(tools)])

    def old_engine_on_path(self):
        stub = self.stubs / "praetorctl"
        stub.write_text("#!/bin/sh\n"
                        f"echo \"$*\" >> '{self.log}'\n"
                        f"cat >&2 <<'LISTING'\n{OLD_LISTING.decode()}LISTING\n"
                        "exit 2\n")
        stub.chmod(0o755)

    def run_row(self, settings, native, matcher, payload):
        groups = json.loads((ROOT / settings).read_text())["hooks"][native]
        command = next(group["hooks"][0]["command"] for group in groups if group.get("matcher") == matcher)
        env = {"PATH": self.path, "HOME": str(self.tree), "CLAUDE_PROJECT_DIR": str(self.tree)}
        return subprocess.run([self.shell, "-c", command], cwd=self.tree, env=env, input=payload,
                              capture_output=True, timeout=60, check=False)

    def rows(self):
        return ((".claude/settings.json", "PreToolUse", "^Agent$"),
                (".claude/settings.json", "SubagentStop", "^.+$"),
                (".codex/hooks.json", "PreToolUse", "^spawn_agent$"),
                (".gemini/settings.json", "BeforeTool", "^invoke_agent$"))

    def test_real_engine_lists_the_tracked_pairs(self):
        self.assertTrue(LAUNCHER.serves(str(self.engine), PAIR))
        self.assertTrue(LAUNCHER.serves(str(self.engine), ["claude", "dispatch-abort"]))
        self.assertFalse(LAUNCHER.serves(str(self.engine), ["claude", "future-event"]))

    def test_engine_older_than_the_rows_blocks_nothing(self):
        self.old_engine_on_path()
        for settings, native, matcher in self.rows():
            with self.subTest(row=(settings, native)):
                result = self.run_row(settings, native, matcher, b'{"tool_name": "Agent"}')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(b"no engine serves", result.stderr)
        self.assertEqual(set(self.log.read_text().split("\n")) - {""}, {"hook"},
                         "the old engine must only ever be probed")

    def test_no_engine_at_all_blocks_nothing(self):
        for settings, native, matcher in self.rows():
            with self.subTest(row=(settings, native)):
                result = self.run_row(settings, native, matcher, b"{}")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(b"no praetorctl is built or installed", result.stderr)

    def test_checkout_engine_still_enforces_over_an_old_installed_one(self):
        self.old_engine_on_path()
        (self.tree / "bin").mkdir()
        shutil.copy(self.engine, self.tree / "bin" / self.engine.name)
        payload = json.dumps({"cwd": str(self.tree), "tool_name": "Agent", "tool_input": {}}).encode()
        result = self.run_row(".claude/settings.json", "PreToolUse", "^Agent$", payload)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn(b"session_id must be nonempty text", result.stderr)


if __name__ == "__main__":
    unittest.main()
