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
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(".config/agent/hooks/praetor_hook.py")
# The AGY plugin directory and the launcher copy it carries (AGY runs hooks from there).
PLUGIN = Path(".agents/plugins/praetor")
SPEC = importlib.util.spec_from_file_location("praetor_hook", ROOT / SCRIPT)
LAUNCHER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LAUNCHER)
EXE = ".exe" if os.name == "nt" else ""
PAIR = ["claude", "pre-dispatch"]
AGY = ["agy", "pre-dispatch"]
ALLOW = {"decision": "allow"}
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
NEW_LISTING = OLD_LISTING + (b"  praetorctl hook claude pre-dispatch\n  praetorctl hook codex post-return\n"
                             b"  praetorctl hook agy pre-dispatch\n")


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

    def build_local(self, checkout=True):
        Path(self.local).parent.mkdir()
        Path(self.local).write_bytes(b"")
        if checkout:
            (self.root / ".git").mkdir()

    def launch(self, argv, engines, installed=True, stdin=b"{}"):
        """Run the launcher in process; the exit code and stderr are returned, stdout is kept
        in self.stdout."""
        which = (lambda name: self.installed) if installed else (lambda name: None)
        stdout, stderr = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = LAUNCHER.main(argv, root=self.root, which=which, run=engines, stdin=io.BytesIO(stdin))
        self.stdout = stdout.getvalue()
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
        self.assertEqual(self.stdout, "", "a native client goes on after exit 0 with no output")

    def test_agy_skip_prints_the_allow_answer_the_engine_gives(self):
        for pair, answer in ((AGY, ALLOW), (["agy", "pre-tool"], ALLOW), (["agy", "stop"], {})):
            with self.subTest(pair=pair):
                engines = FakeEngines({self.installed: OLD_LISTING if pair == AGY else b""})
                code, stderr = self.launch(pair, engines)
                self.assertEqual(code, 0)
                self.assertEqual(engines.served(), [])
                self.assertEqual(json.loads(self.stdout), answer)
                self.assertIn(f"no engine serves {' '.join(pair)} (checked " + self.installed, stderr)
                self.assertTrue(stderr.endswith(", skipped\n"), stderr)

    def test_agy_without_any_engine_still_gets_its_answer(self):
        code, stderr = self.launch(AGY, FakeEngines({}), installed=False)
        self.assertEqual(code, 0)
        self.assertEqual(json.loads(self.stdout), ALLOW)
        self.assertIn("no praetorctl is built or installed", stderr)

    def test_agy_engine_that_serves_the_pair_answers_itself(self):
        engines = FakeEngines({self.installed: NEW_LISTING})
        code, _ = self.launch(AGY, engines)
        self.assertEqual(code, 0)
        self.assertEqual(engines.served(), [[self.installed, "hook", *AGY]])
        self.assertEqual(self.stdout, "", "the engine's own answer is the only stdout")

    def test_agy_event_without_an_answer_shape_fails_closed(self):
        for pair in (["agy", "post-tool"], ["agy", "pre-invocation"], ["agy", "future-event"]):
            with self.subTest(pair=pair):
                engines = FakeEngines({self.installed: NEW_LISTING})
                code, stderr = self.launch(pair, engines)
                self.assertEqual(code, 2)
                self.assertIn("usage: praetor_hook.py <client> <event>", stderr)
                self.assertEqual((engines.calls, self.stdout), ([], ""))

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
                self.assertEqual(self.stdout, "")

    def test_agy_engine_that_hangs_or_vanishes_is_an_allow_not_a_block(self):
        for error in (subprocess.TimeoutExpired("praetorctl", LAUNCHER.RUN_TIMEOUT), FileNotFoundError("gone")):
            with self.subTest(error=type(error).__name__):
                engines = FakeEngines({self.installed: NEW_LISTING}, errors={(self.installed, 4): error})
                code, stderr = self.launch(AGY, engines)
                self.assertEqual(code, 0)
                self.assertEqual(json.loads(self.stdout), ALLOW)
                self.assertIn("praetor hook: " + self.installed, stderr)

    def test_engine_beside_a_tree_that_is_no_checkout_is_not_asked(self):
        self.build_local(checkout=False)
        self.assertEqual(LAUNCHER.candidates(self.root, lambda name: None), [])
        (self.root / ".git").write_text("gitdir: elsewhere\n")  # a linked worktree
        self.assertEqual(LAUNCHER.candidates(self.root, lambda name: None), [self.local])

    def test_one_engine_reached_twice_is_probed_once(self):
        self.build_local()
        engines = FakeEngines({self.local: OLD_LISTING})
        with contextlib.redirect_stderr(io.StringIO()):
            code = LAUNCHER.main(PAIR, root=self.root, which=lambda name: self.local, run=engines, stdin=io.BytesIO())
        self.assertEqual(code, 0)
        self.assertEqual(engines.calls, [[self.local, "hook"]])

    def test_drain_is_bounded_in_size_and_time(self):
        stream = io.BytesIO(b"x" * (LAUNCHER.DRAIN_LIMIT + 10))
        LAUNCHER.drain(stream.read)
        self.assertEqual(stream.tell(), LAUNCHER.DRAIN_LIMIT)
        for size in (0, 1, LAUNCHER.DRAIN_CHUNK + 1):
            with self.subTest(size=size):
                stream = io.BytesIO(b"x" * size)
                LAUNCHER.drain(stream.read)
                self.assertEqual(stream.tell(), size)
        blocked = threading.Event()

        def stuck(limit):
            blocked.wait(5)
            return b""

        LAUNCHER.drain(stuck, timeout=0.05)
        blocked.set()

        def closed(limit):
            raise OSError("bad file descriptor")

        LAUNCHER.drain(closed)

    def test_client_holding_stdin_open_still_gets_a_clean_exit(self):
        # A client may keep the pipe open after its payload; the drain then gives up while
        # its reader still blocks. Reading through sys.stdin.buffer made CPython abort at
        # shutdown with exit 134 here instead of exiting 0.
        script = self.root / "tree" / SCRIPT
        script.parent.mkdir(parents=True)
        shutil.copy(ROOT / SCRIPT, script)
        python = Path(sys.executable)
        if (python.parent / ("praetorctl" + EXE)).exists():
            self.skipTest("python3 shares a directory with an installed praetorctl")
        env = {"PATH": str(python.parent)}
        env.update({name: os.environ[name] for name in ("SYSTEMROOT",) if name in os.environ})
        with subprocess.Popen([sys.executable, "-B", str(script), *PAIR], stdin=subprocess.PIPE,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env) as proc:
            try:
                code = proc.wait(timeout=LAUNCHER.DRAIN_TIMEOUT + 30)
            finally:
                proc.stdin.close()
            stderr = proc.stderr.read()
        self.assertEqual(code, 0, stderr)
        self.assertIn(b"no praetorctl is built or installed", stderr)
        self.assertNotIn(b"Fatal Python error", stderr)


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
        self.copy_plugin(self.tree / PLUGIN)
        subprocess.run(["git", "init", "-q", str(self.tree)], capture_output=True, timeout=20, check=True)
        self.temp = Path(temp.name)
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

    @staticmethod
    def copy_plugin(target):
        target.mkdir(parents=True)
        for name in ("hooks.json", "praetor_hook.py"):
            shutil.copy(ROOT / PLUGIN / name, target / name)

    def run_agy_row(self, plugin, payload):
        """The tracked AGY row, run the way AGY documents it: `sh -c` from the directory that
        holds hooks.json."""
        groups = json.loads((ROOT / PLUGIN / "hooks.json").read_text())["praetor-subagent-register"]["PreToolUse"]
        command = next(group["hooks"][0]["command"] for group in groups if group.get("matcher") == "invoke_subagent")
        env = {"PATH": self.path, "HOME": str(self.temp)}
        return subprocess.run([self.shell, "-c", command], cwd=plugin, env=env, input=payload,
                              capture_output=True, timeout=60, check=False)

    def agy_payload(self, workspace, subagents=({"Prompt": "x"},)):
        return json.dumps({"conversationId": "c", "workspacePaths": [str(workspace)],
                           "toolCall": {"name": "invoke_subagent", "args": {"Subagents": list(subagents)}}}).encode()

    def installed_plugin(self):
        """The plugin as AGY installs it for a user: a copy outside any checkout."""
        plugin = self.temp / "home" / ".gemini" / "config" / "plugins" / "praetor"
        self.copy_plugin(plugin)
        return plugin

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

    def test_agy_row_against_an_engine_older_than_the_row_allows(self):
        self.old_engine_on_path()
        for plugin in (self.tree / PLUGIN, self.installed_plugin()):
            with self.subTest(plugin=plugin):
                result = self.run_agy_row(plugin, self.agy_payload(self.tree))
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout), ALLOW)
                self.assertIn(b"no engine serves agy pre-dispatch", result.stderr)
        self.assertEqual(set(self.log.read_text().split("\n")) - {""}, {"hook"},
                         "the old engine must only ever be probed")

    def test_agy_row_without_any_engine_allows(self):
        result = self.run_agy_row(self.tree / PLUGIN, self.agy_payload(self.tree))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), ALLOW)
        self.assertIn(b"no praetorctl is built or installed", result.stderr)

    def test_agy_row_reaches_a_current_engine_outside_a_checkout(self):
        shutil.copy(self.engine, self.stubs / "praetorctl")
        result = self.run_agy_row(self.installed_plugin(), self.agy_payload(self.tree, subagents=()))
        self.assertEqual(result.returncode, 0, result.stderr)
        answer = json.loads(result.stdout)
        self.assertEqual(answer["decision"], "deny", answer)
        self.assertIn("Subagents must contain 1..64 entries", answer["reason"])

    def test_checkout_engine_enforces_the_agy_row_over_an_old_installed_one(self):
        self.old_engine_on_path()
        (self.tree / "bin").mkdir()
        shutil.copy(self.engine, self.tree / "bin" / self.engine.name)
        result = self.run_agy_row(self.tree / PLUGIN, self.agy_payload(self.tree, subagents=()))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["decision"], "deny", result.stdout)
        self.assertFalse(self.log.exists(), "the checkout engine must answer before PATH is probed")

    def test_launcher_answers_are_the_engine_skip_answers(self):
        listing = subprocess.run([str(self.engine), "hook"], capture_output=True, timeout=30, check=False)
        served = {tuple(line.split()[2:]) for line in (listing.stdout + listing.stderr).decode().splitlines()
                  if line.strip().startswith("praetorctl hook agy ")}
        self.assertEqual(served, {pair for pair in LAUNCHER.PROCEED if pair[0] == "agy"})
        outside = self.temp / "no-repository"
        outside.mkdir()
        for pair, answer in LAUNCHER.PROCEED.items():
            with self.subTest(pair=pair):
                result = subprocess.run([str(self.engine), "hook", *pair], cwd=outside, input=self.agy_payload(outside),
                                        capture_output=True, timeout=30, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout), json.loads(answer))

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
