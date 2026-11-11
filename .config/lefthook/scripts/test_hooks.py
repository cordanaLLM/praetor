#!/usr/bin/env python3
"""Behavioral gates in disposable Git repositories; never disable real hooks."""

import ast
import contextlib
import importlib.util
import io
import json
import os
import random
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

import checks
import common
from checks import (
    Command,
    HOOK_LINT_BIN_ENV,
    HOOK_LINT_SUITE,
    hook_lint_cache_root,
    hook_lint_digest,
    hook_sources_changed,
    load_hook_lint,
    parallel,
    provision_hook_lint,
    require_hook_lint_tools,
    venv_bin,
)
import hooks
import sandbox
import toolchain
from checks import (
    FIXTURE_DIRECTORY,
    GATE_LAUNCH_MARGIN,
    GATE_QUERY_TIMEOUT,
    SEMGREP_LANGUAGE_EXTENSIONS,
    SEMGREP_SUFFIXES,
    audit_scope,
    checkpoint_checks,
    context_changed,
    file_checks,
    gate_timeout,
    go_packages,
    gofmt_check,
    golangci_lint_command,
    governance_commands,
    is_chart_template,
    is_fixture,
    lint_commands,
    local_package_patterns,
    run_full_gate,
    self_test_commands,
    semgrep_commands,
    source_checks,
)
from common import (
    MANAGED_PROCESS_ENV,
    MAX_PROCESS_ENV_ENTRIES,
    HookError,
    clean_env,
    run,
    snapshot,
    stop_process_group,
)
from hooks import (
    new_branch_base,
    pre_push,
    prepare_message,
    push_check_mode,
    push_updates,
)
from privacy import check_private_history, check_private_index

ROOT = Path(__file__).resolve().parents[3]
RUNNER = Path(".config/lefthook/scripts/hooks.py")
GUARD = ROOT / ".config/agent/hooks/block_evasion.py"
# The scan budget is 5 s of child CPU time (user + system) on POSIX, where host
# load cannot inflate it. On Windows, where os.times() reports no child times,
# fall back to wall clock with a stated looser bound (8 s) to tolerate scheduler
# contention while staying inside the 10 s subprocess wall-clock hang guard.
GUARD_TIMING_BOUND = 8.0 if os.name == "nt" else 5.0
# Make and Git localize their diagnostics; pin the message catalogue so
# assertions on tool output hold on workstations with non-English locales.
LOCALE_ENV = {"LC_ALL": "C", "LANGUAGE": "C"}
# windows-latest's git defaults core.autocrlf=true (the same runner default #282 pinned
# .gitattributes eol=lf against for the real tree's *.go and archetype yaml files). Every
# fixture repo this file builds is its own throwaway git repository with no .gitattributes
# of its own, so `git checkout-index` inside common.snapshot() -- the pre-commit hook's own
# export step -- reintroduced CRLF into a fixture-committed .go file before gofmt ever saw
# it, and gofmt reports any CRLF file as unformatted. That masked
# test_vet_checks_staged_go_package's intended "go vet: wrong type" assertion behind
# "Run gofmt and stage the intended changes: main.go" on Windows. NO_AUTOCRLF_ENV forces
# autocrlf off for every git invocation this suite makes itself (env, not a per-repo
# `git config`). It does not reach the hook's nested Git: common.index_env() and clean_env()
# strip GIT_CONFIG_COUNT and its indexed keys at the gate boundary, so each nested checkout in
# common.snapshot() passes SNAPSHOT_GIT_CONFIG (-c core.autocrlf=false) itself, matching how
# internal/bump pins the same flag (#282).
# test_snapshot_pins_autocrlf_against_persistent_configuration replays that pin.
NO_AUTOCRLF_ENV = {"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.autocrlf",
                   "GIT_CONFIG_VALUE_0": "false"}
AUTOCRLF_ENV = {**NO_AUTOCRLF_ENV, "GIT_CONFIG_VALUE_0": "true"}


# A command the way git runs one: it holds a lock and removes it on a terminating signal,
# leaving `cleaned` as evidence. Its marker `survived` appears only if nothing stopped it.
LOCK_HOLDING_COMMAND = r"""
import signal, sys, time
from pathlib import Path
root = Path(sys.argv[1])
def cleanup(number, _frame):
    (root / "lock").unlink(missing_ok=True)
    (root / "cleaned").touch()
    raise SystemExit(128 + number)
signal.signal(signal.SIGTERM, cleanup)
signal.signal(signal.SIGINT, cleanup)
(root / "lock").touch()
(root / "command-ready").touch()
time.sleep(30)
(root / "survived").touch()
"""

# The praetor CLI's shape (internal/util/command_bytes_unix.go): it runs the command in a
# process group of its own, and forwards the signal that stops it to that group before it
# exits. It reports the command's pid in `ready` once both handlers are installed.
FORWARDING_CLI = r"""
import os, signal, subprocess, sys, time
from pathlib import Path
root = Path(sys.argv[1])
command = subprocess.Popen([sys.executable, "-c", sys.argv[2], str(root)], start_new_session=True)
received = []
def forward(number, _frame):
    received.append(number)
    os.killpg(command.pid, number)
signal.signal(signal.SIGTERM, forward)
signal.signal(signal.SIGINT, forward)
for _ in range(400):
    if (root / "command-ready").exists():
        break
    time.sleep(0.025)
(root / "ready.tmp").write_text(str(command.pid))
(root / "ready.tmp").rename(root / "ready")
command.wait()
raise SystemExit(128 + received[0] if received else 0)
"""

# A CLI that ignores the catchable signals, so only SIGKILL stops it.
STUBBORN_CLI = r"""
import signal, sys, time
from pathlib import Path
signal.signal(signal.SIGTERM, signal.SIG_IGN)
signal.signal(signal.SIGINT, signal.SIG_IGN)
Path(sys.argv[1], "ready").write_text("")
time.sleep(30)
"""


def start_cli(test, root, script, *args):
    """Start ``script`` the way ``run`` starts a child and return it once it reported ready."""
    process = subprocess.Popen([sys.executable, "-c", script, str(root), *args],
                               start_new_session=True)
    test.addCleanup(lambda: process.poll() is not None or (process.kill(), process.wait()))
    for _ in range(400):
        if (root / "ready").exists():
            return process
        time.sleep(0.025)
    test.fail("the child never reported ready")


def reap_command_group(root):
    """Kill the command's group if a failing assertion left it running, and report whether it was."""
    raw = (root / "ready").read_text()
    if not raw:
        return False
    try:
        pid = int(raw)
        if pid <= 1 or pid == os.getpgrp():
            return False
        os.killpg(pid, signal.SIGKILL)
    except (ProcessLookupError, ValueError):
        return False
    return True


class _WithoutProcessGroup:
    """``os`` as Windows presents it: everything except ``killpg``.

    common._kill_bounded selects its branch on ``hasattr(os, "killpg")``, so this makes the
    non-POSIX branch reachable from a POSIX host instead of leaving it to the one platform
    that cannot run the rest of this suite yet.
    """

    def __getattr__(self, name):
        if name == "killpg":
            raise AttributeError(name)
        return getattr(os, name)
# The CLI a fixture carries, named as hooks.praetorctl_path() looks for it: with the host's
# executable suffix. The fixture used to link an extensionless bin/praetorctl, which the hooks
# never found on Windows.
PRAETORCTL = "bin/praetorctl" + (".exe" if os.name == "nt" else "")
# Codex runs a command hook through the login shell on Linux and macOS and through %COMSPEC% /C
# on Windows (codex-rs/hooks/src/engine/command_runner.rs; see docs/guides/agent-hooks.md). The
# registration's $( ) Git-root lookup is shell syntax cmd.exe lacks, so the Windows form is the
# `commandWindows` override, and the test runs it through cmd.exe as Codex does (#209).


def codex_command(action):
    """The process Codex's hook runner starts for one registered action on this host.

    Linux and macOS: the login shell with -lc and the `command` string. Windows: %COMSPEC% /C
    with the `commandWindows` override, else `command`, passed raw inside one pair of quotes
    (build_command in codex-rs/hooks/src/engine/command_runner.rs). On Windows the result is a
    command line, not an argv, because Codex passes it raw and argv quoting would change it.
    """
    if os.name != "nt":
        return ["/bin/sh", "-c", action["command"]]
    line = action.get("commandWindows", action["command"])
    return '"' + os.environ.get("COMSPEC", "cmd.exe") + '" /C "' + line + '"'
# The policy prints job output and failures only, so a passing job is evidenced by what it
# printed, never by Lefthook's success line naming it. commit-msg and pre-push print this after
# verifying the live ledger.
STATE_VERIFIED = b'PRAETOR_STATE_RESULT={"schema_version":1,"verified":true}'


LAUNCHER = ".config/lefthook/python.sh"
# Why the cases that put a stand-in program on PATH skip on Windows (HISS-21).
POSIX_STUB = ("a stand-in program is a shebang script, which the Windows loader does not "
              "start; the cases that answer the version probe from a stubbed process cover "
              "the same decisions on this platform")


def stand_in(directory, name, body):
    """Write a POSIX program named name into directory and return its path."""
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / name
    path.write_text("#!/bin/sh\n" + body, newline="\n")
    path.chmod(0o755)
    return path


def interpreter_stand_in(directory, name, answer):
    """A stand-in that answers the version probe with printf format answer.

    Started any other way it prints its own path, its argument count and its arguments, so a
    test reads which candidate the launcher ran and with what.
    """
    body = ('if [ "$1" = -c ] || [ "$2" = -c ]; then printf \'' + answer + "'; exit 0; fi\n"
            'echo "stub:$0:$#:$*"\n')
    return stand_in(directory, name, body)


def only(directory):
    """A PATH lookup that sees directory alone."""
    return lambda program: shutil.which(program, path=str(directory))


def cli_path(repo):
    """The fixture CLI as an absolute path.

    CreateProcess resolves a relative program path against the parent's working directory, not
    the cwd it is given, so "bin/praetorctl" ran whatever binary sat under the directory the suite
    was started from -- on CI the real repository's, and inside a pre-commit snapshot none at all.
    """
    return str(repo / PRAETORCTL)


def command(repo, *args, data=None, ok=True, maintain_state=True):
    """Run one command in the fixture repository, syncing Praetor state before a commit or push unless maintain_state is false."""
    # Most fixtures model an agent obeying the sync obligation. Negative state
    # tests opt out and execute the same real hooks against stale/missing state.
    if (maintain_state and args[:2] in (("git", "commit"), ("git", "push"))
            and (repo / PRAETORCTL).is_file()):
        run_command(repo, (cli_path(repo), "state", "sync", "."), None, ok)
    result = run_command(repo, args, data, ok)
    if args[:2] == ("git", "init") and "--bare" not in args and (repo / ".git").is_dir():
        run_command(repo, ("git", "config", "commit.gpgsign", "false"), None, False)
        run_command(repo, ("git", "config", "tag.gpgsign", "false"), None, False)
    return result


def run_command(repo, args, data, ok):
    """Run one fixture command; command() adds the state sync a commit or push obliges.

    Kept apart from command() so the sync runs through the same environment without the
    helper calling itself (HISS-01).
    """
    env = dict(os.environ, PYTHONDONTWRITEBYTECODE="1", **LOCALE_ENV, **NO_AUTOCRLF_ENV)
    for key in ("GIT_DIR", "GIT_INDEX_FILE", "GIT_WORK_TREE"):
        env.pop(key, None)
    # A test command without explicit input must see EOF, not inherit the operator's
    # terminal. Lefthook post hooks otherwise wait on a live TTY until this helper's
    # 120-second timeout; commands that exercise stdin still receive their payload.
    stdin = b"" if data is None else data
    result = subprocess.run(args, cwd=repo, env=env, input=stdin, capture_output=True,
                            timeout=120, check=False)
    if ok and result.returncode:
        raise AssertionError(result.stdout.decode() + result.stderr.decode())
    return result



def race_detector_available():
    """Report whether `go test -race` can build here, and why not when it cannot.

    The race detector needs cgo and a host C toolchain. On a Windows box without gcc every race
    leg fails to build, so the harness self-tests could never pass there -- and those tests run
    precisely when `.config/lefthook/` changes, which is exactly what a contributor fixing Windows
    support has to touch. The gate could not be repaired from the platform it was broken on.

    Skipping is reported rather than silent. A skipped race leg that reads as a pass would be the
    same defect this repository keeps finding elsewhere: an unexamined thing certified as clean.
    CI runs on Linux with cgo, so coverage is not lost, only deferred to where it can run.
    """
    if os.environ.get("CGO_ENABLED") == "0":
        return False, "CGO_ENABLED=0"
    try:
        enabled = subprocess.run(["go", "env", "CGO_ENABLED"], capture_output=True, text=True,
                                 timeout=30, check=False).stdout.strip()
    except (OSError, subprocess.TimeoutExpired) as error:
        return False, f"go env unavailable: {error}"
    if enabled == "0":
        return False, "go env CGO_ENABLED=0"
    # CGO_ENABLED defaults to 1 whether or not a compiler exists, so it alone reported the race
    # detector available on a Windows host without gcc, and every race leg then failed to build.
    # The compiler go would invoke is what the answer actually depends on.
    try:
        compiler = subprocess.run(["go", "env", "CC"], capture_output=True, text=True,
                                  timeout=30, check=False).stdout.strip()
    except (OSError, subprocess.TimeoutExpired) as error:
        return False, f"go env unavailable: {error}"
    if not compiler or shutil.which(compiler.split()[0]) is None:
        return False, f"C compiler {compiler or '(unset)'!r} not found"
    return True, ""


def skip_without_race_detector(test):
    """Skip one test when the race detector cannot build, naming the reason."""
    available, reason = race_detector_available()
    if not available:
        test.skipTest(f"race detector unavailable ({reason}); CI runs these legs on Linux with cgo")


class GitHooks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cli_temp = tempfile.TemporaryDirectory(prefix="praetor-hook-cli-")
        cls.addClassCleanup(cls.cli_temp.cleanup)
        cls.binary = Path(cls.cli_temp.name) / Path(PRAETORCTL).name
        command(ROOT, "go", "build", "-o", str(cls.binary), "./cmd/standardsctl")

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-hook-test-")
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.initialize_repo()

    def initialize_repo(self, initial_files=()):
        """Create the fixture repository: git init on main, a test identity, the hook folders and initial_files."""
        self.repo.mkdir()
        command(self.repo, "git", "init", "-q", "-b", "main")
        command(self.repo, "git", "config", "user.name", "Hook Test")
        command(self.repo, "git", "config", "user.email", "hook@example.test")
        for path in (".config/lefthook", ".config/agent/hooks"):
            shutil.copytree(ROOT / path, self.repo / path)
        shutil.copy(ROOT / "lefthook.yml", self.repo / "lefthook.yml")
        # Fixture policy self-tests are data, not another recursive full suite.
        for self_test in ("test_hooks.py", "test_checkpoint.py", "test_security_scope.py", "test_race_packages.py"):
            (self.repo / f".config/lefthook/scripts/{self_test}").write_text(
                'print("fixture hook self-tests passed")\n', newline="\n")
        (self.repo / "README.md").write_text("# Fixture\n", newline="\n")
        for name, content in initial_files:
            self.write(name, content, stage=False)
        command(self.repo, "git", "add", ".")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "chore: initialize fixture")
        self.initialize_state()
        command(self.repo, "lefthook", "install")

    def initialize_state(self):
        (self.repo / "bin").mkdir()
        os.link(self.binary, self.repo / PRAETORCTL)
        (self.repo / ".git/info/exclude").write_text("/bin/\n/.workingdir/\n/Makefile\n")
        (self.repo / "Makefile").write_text(f"hook-cli:\n\t@test -x {PRAETORCTL}\n")
        command(self.repo, cli_path(self.repo), "state", "init", ".")
        command(self.repo, cli_path(self.repo), "state", "sync", ".")

    def write(self, name, data, stage=True):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        # Fixture content is the bytes given. Text mode on Windows writes CRLF, which git diff
        # --check reports as trailing whitespace before the check a test targets can run.
        path.write_text(data, newline="\n")
        if stage:
            command(self.repo, "git", "add", "--", name)

    def assertMissingMakeTarget(self, result, target):
        # GNU make quotes the missing target differently per build: 4.x prints
        # 'state-audit', while the make 3.81 macOS still ships prints `state-audit'.
        # The property under test is that the hook surfaced make's refusal, not which
        # quoting the platform's make chose, so match the message and the target
        # separately.
        output = result.stdout + result.stderr
        self.assertIn(b"No rule to make target", output, output)
        self.assertIn(target.encode(), output, output)

    def hook(self, name="pre-commit", *args, data=None, maintain_state=True):
        # Path arguments are given as git gives them to hooks, with forward slashes. Lefthook on
        # Windows substitutes arguments into `sh -c "..."`, and a backslashed native path there
        # left an unterminated quote, so the message hooks failed before running -- and the
        # negative message cases passed for that reason alone.
        if maintain_state and name in {"pre-commit", "pre-push", "commit-msg"}:
            command(self.repo, cli_path(self.repo), "state", "sync", ".")
        return command(self.repo, "lefthook", "run", name, *args, data=data, ok=False)

    def test_no_variable_takes_a_hook_job_off_its_interpreter_or_its_make(self):
        # PRAETOR_PYTHON and PRAETOR_MAKE once named the programs a job started, so a value
        # naming anything that exits 0 passed every gate without running it. Both are plain
        # variables now: the commit-msg job still judges the message and the pre-commit job
        # still runs the hook-cli target, which this fixture makes fail.
        skipped = {"PRAETOR_PYTHON": "true", "PRAETOR_MAKE": "true"}
        message = self.repo / "message.txt"
        message.write_text("bad subject\n")
        with mock.patch.dict(os.environ, skipped):
            refused = self.hook("commit-msg", message.as_posix())
        self.assertNotEqual(refused.returncode, 0, refused.stdout + refused.stderr)
        self.assertIn(b"Commit subject must be", refused.stdout + refused.stderr)
        self.write("README.md", "# Staged\n")
        with mock.patch.dict(os.environ, skipped):
            passed = self.hook()
        self.assertEqual(passed.returncode, 0, passed.stdout + passed.stderr)
        self.assertIn(b"Index: 1 changed paths checked", passed.stdout + passed.stderr)
        (self.repo / "Makefile").write_text("hook-cli:\n\t@exit 3\n")
        with mock.patch.dict(os.environ, skipped):
            built = self.hook()
        self.assertNotEqual(built.returncode, 0, built.stdout + built.stderr)
        # GNU Make names the target whose recipe failed: the target ran.
        self.assertIn(b"hook-cli] Error 3", built.stdout + built.stderr)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_a_hook_job_refuses_stand_ins_for_its_interpreter_and_its_make(self):
        # A program that exits 0 is neither Python nor GNU Make. First on PATH under every
        # candidate name, it must fail the job as a missing dependency, never pass it.
        self.write("README.md", "# Staged\n")
        with tempfile.TemporaryDirectory(prefix="praetor-stand-in-") as temp:
            shadowed = os.pathsep.join([temp, os.environ["PATH"]])
            for names, missing in (
                    (toolchain.MAKE_CANDIDATES, b"praetor hooks: missing dependency: GNU Make"),
                    (toolchain.PYTHON_CANDIDATES,
                     b"praetor hooks: missing dependency: no Python 3.10 or newer interpreter")):
                for candidate in names:
                    stand_in(Path(temp), candidate[0], "exit 0\n")
                with self.subTest(names=names), mock.patch.dict(os.environ, {"PATH": shadowed}):
                    refused = self.hook()
                    self.assertNotEqual(refused.returncode, 0, refused.stdout + refused.stderr)
                    self.assertIn(missing, refused.stdout + refused.stderr)
                    self.assertNotIn(b"changed paths checked", refused.stdout + refused.stderr)

    def test_docs_commit_preserves_unstaged_and_untracked_files(self):
        self.write("README.md", "# Intended\n")
        self.write("README.md", "# Unstaged\n", stage=False)
        self.write("private scratch.txt", "keep me\n", stage=False)
        before = command(self.repo, "git", "diff", "--cached", "--binary").stdout
        checked = self.hook()
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertEqual(before, command(self.repo, "git", "diff", "--cached", "--binary").stdout)
        result = command(self.repo, "git", "commit", "-s", "-m", "docs: add staged text")
        self.assertIn(b"Index: 1 changed paths checked", result.stdout + result.stderr)
        self.assertIn(STATE_VERIFIED, result.stdout + result.stderr)
        self.assertIn(b"Dedupe review due", result.stdout + result.stderr)
        self.assertEqual(command(self.repo, "git", "show", "HEAD:README.md").stdout, b"# Intended\n")
        self.assertEqual((self.repo / "README.md").read_text(), "# Unstaged\n")
        self.assertTrue((self.repo / "private scratch.txt").exists())
        self.assertTrue((self.repo / ".workingdir/STATE.md").is_file())
        command(self.repo, cli_path(self.repo), "state", "sync", "--verify", ".")

    def test_staged_python_failure_cannot_be_hidden_by_worktree_fix(self):
        self.write("bad name 'quoted'.py", "def invalid(:\n")
        self.write("bad name 'quoted'.py", "value = 1\n", stage=False)
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"invalid syntax", result.stdout + result.stderr)

    def test_forced_private_state_staging_blocks_commit_without_reading_content(self):
        self.write(".gitignore", "/.workingdir/\n")
        self.write(".workingdir/docs/cluster.json", "not JSON: PRIVATE_FIXTURE\n", stage=False)
        command(self.repo, "git", "add", "-f", "--", ".workingdir/docs/cluster.json")
        before = command(self.repo, "git", "rev-parse", "HEAD").stdout
        result = command(self.repo, "git", "commit", "-s", "-m", "docs: accidental private guide", ok=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertNotIn(b"PRIVATE_FIXTURE", result.stdout + result.stderr)
        self.assertEqual(before, command(self.repo, "git", "rev-parse", "HEAD").stdout)
        self.assertEqual((self.repo / ".workingdir/docs/cluster.json").read_text(),
                         "not JSON: PRIVATE_FIXTURE\n")

    def test_untracking_legacy_private_state_keeps_local_file_and_allows_commit(self):
        self.repo = Path(self.temp.name) / "legacy"
        self.initialize_repo(((".workingdir/docs/cluster.md", "private fixture\n"),))
        self.write(".gitignore", "/.workingdir/\n")
        command(self.repo, "git", "rm", "--cached", "--", ".workingdir/docs/cluster.md")
        result = command(self.repo, "git", "commit", "-s", "-m", "chore: keep state private")
        self.assertIn(b"Index: 2 changed paths checked", result.stdout + result.stderr)
        self.assertEqual(command(self.repo, "git", "ls-files", ".workingdir").stdout, b"")
        self.assertEqual((self.repo / ".workingdir/docs/cluster.md").read_text(), "private fixture\n")

    def test_private_gitlinks_block_commit_before_snapshot_export(self):
        for index, name in enumerate((".workingdir", ".workingdir/submodule")):
            with self.subTest(name=name):
                self.repo = Path(self.temp.name) / f"gitlink-{index}"
                self.initialize_repo()
                if name == ".workingdir":
                    shutil.rmtree(self.repo / ".workingdir")
                head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
                command(self.repo, "git", "clone", "-q", str(self.repo), str(self.repo / name))
                command(self.repo, "git", "update-index", "--add", "--cacheinfo", f"160000,{head},{name}")
                result = command(self.repo, "git", "commit", "-s", "-m", "chore: private gitlink", ok=False)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
                self.assertEqual(command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip(), head)

    def incoming_private_history(self, legacy=False, gitlink=False, old_topic=False):
        # Simulate commits received from a plain repository that never installed
        # Praetor hooks. The receiving fixture keeps all real hooks enabled.
        external = Path(self.temp.name) / "external"
        remote = Path(self.temp.name) / "remote.git"
        external.mkdir()
        command(external, "git", "init", "-q", "-b", "main")
        command(external, "git", "config", "user.name", "External Fixture")
        command(external, "git", "config", "user.email", "external@example.test")
        if old_topic:
            (external / "README.md").write_text("# Earlier public baseline\n")
            command(external, "git", "add", ".")
            command(external, "git", "commit", "-q", "-s", "-m", "docs: earlier baseline")
            command(external, "git", "branch", "feat/old-topic")
        (external / "README.md").write_text("# External fixture\n")
        if legacy:
            (external / ".workingdir").mkdir()
            (external / ".workingdir/private.txt").write_text("PRIVATE_HISTORY_SENTINEL\n")
        command(external, "git", "add", ".")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: published baseline")
        base = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(external, "git", "init", "--bare", "-q", str(remote))
        command(external, "git", "remote", "add", "origin", str(remote))
        command(external, "git", "push", "-q", "origin", "main")
        if old_topic:
            command(external, "git", "push", "-q", "origin", "feat/old-topic")
        if not legacy:
            if gitlink:
                command(external, "git", "update-index", "--add", "--cacheinfo", f"160000,{base},.workingdir")
            else:
                (external / ".workingdir").mkdir()
                (external / ".workingdir/private.txt").write_text("PRIVATE_HISTORY_SENTINEL\n")
                command(external, "git", "add", ".workingdir")
            command(external, "git", "commit", "-q", "-s", "-m", "docs: incoming private content")
        command(external, "git", "rm", "-r", "--cached", "--", ".workingdir")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: remove private tracking")
        head = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        command(self.repo, "git", "fetch", "-q", "origin")
        if old_topic:
            command(self.repo, "git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
        command(self.repo, "git", "fetch", "-q", str(external), "main")
        return remote, base, head

    def test_public_snapshot_initializes_private_state_before_flavor_and_gate(self):
        public = Path(self.temp.name) / "snapshot-source"
        public.mkdir()
        command(public, "git", "init", "-q", "-b", "main")
        command(public, "git", "config", "user.name", "Snapshot Fixture")
        command(public, "git", "config", "user.email", "snapshot@example.test")
        # The fixture carries a go.mod and an internal package so it genuinely matches a
        # registered flavor. It previously matched nothing and was classified go-library only by
        # the silent fallback that detection no longer has; the .golangci.yml it already shipped
        # made sense for no other language.
        #
        # lefthook.yml and the ruleset file must parse as non-empty (validYAMLMapping /
        # validJSONObject in internal/flavor/flavor.go reject `{}`: a lefthook.yml or ruleset
        # holding nothing installs or enforces nothing, so it no longer counts as configuration).
        files = {".standards.yaml": "repository: {}\n", ".standards.lock": "version: 1\n",
                 "go.mod": "module fixture\n\ngo 1.25\n", "internal/doc.go": "package internal\n",
                 ".golangci.yml": "version: '2'\n", ".github/workflows/ci.yml": "name: fixture\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n",
                 "lefthook.yml": "pre-commit:\n  commands:\n    fixture: {}\n",
                 ".github/rulesets/main.json": '{"name": "main"}\n',
                 ".gitignore": "/.workingdir/\n",
                 "Makefile": 'state-audit: state-init\n\t"' + str(self.binary) + '" state audit .\n'
                             'state-init:\n\t"' + str(self.binary) + '" state init --if-absent .\n'}
        for name, value in files.items():
            path = public / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(value)
        command(public, "git", "add", ".")
        command(public, "git", "commit", "-q", "-s", "-m", "chore: public snapshot fixture")
        private = public / ".workingdir/STATE.md"
        private.parent.mkdir()
        private.write_text("PRIVATE_SNAPSHOT_SENTINEL\n")
        with contextlib.chdir(public), snapshot("HEAD") as directory:
            self.assertFalse((directory / ".workingdir").exists())
            def check_flavor(commands, root):
                for argv in commands:
                    if argv[3:5] == ["flavor", "audit"]:
                        run([str(self.binary), *argv[3:]], cwd=root)
            def check_gate(root, packages):
                run([str(self.binary), "state", "audit", "."], cwd=root)
                return True
            with mock.patch("checks.parallel", side_effect=check_flavor), \
                    mock.patch("checks.run_full_gate", side_effect=check_gate) as gated:
                self.assertTrue(source_checks(directory, [".standards.yaml"], base="HEAD"))
                gated.assert_called_once_with(directory, [])
            self.assertTrue((directory / ".workingdir/OPEN.md").is_file())
            self.assertNotIn("PRIVATE_SNAPSHOT_SENTINEL", (directory / ".workingdir/STATE.md").read_text())
            self.assertEqual(command(directory, "git", "status", "--porcelain").stdout, b"")
            # Existing partial state must not be repaired or permitted into later gates.
            (directory / ".workingdir/OPEN.md").unlink()
            before = {p.name: p.read_bytes() for p in (directory / ".workingdir").iterdir() if p.is_file()}
            with mock.patch("checks.parallel") as scans, mock.patch("checks.run_full_gate") as gated:
                with self.assertRaisesRegex(HookError, "state audit failed"):
                    source_checks(directory, [".standards.yaml"], base="HEAD")
                scans.assert_not_called()
                gated.assert_not_called()
            self.assertEqual(before, {p.name: p.read_bytes() for p in (directory / ".workingdir").iterdir() if p.is_file()})
        self.assertEqual(private.read_text(), "PRIVATE_SNAPSHOT_SENTINEL\n")

    def test_new_branch_push_prefers_remote_default_over_older_topic_for_private_removal(self):
        remote, base, head = self.incoming_private_history(legacy=True, old_topic=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/removal")
        self.assertIn(STATE_VERIFIED, result.stdout + result.stderr)
        self.assertEqual((result.stdout + result.stderr).count(f"Push: {head[:12]} checked".encode()), 1)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/review/removal").stdout.decode().strip(), head)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)
        self.assertEqual(command(remote, "git", "ls-tree", "-r", "--name-only", head, "--", ".workingdir").stdout, b"")

    def test_new_branch_default_baseline_rejects_new_private_add_then_delete(self):
        remote, base, head = self.incoming_private_history(old_topic=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/private", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertNotIn(b"PRIVATE_HISTORY_SENTINEL", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/review/private").stdout, b"")
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)

    def test_missing_vendored_push_script_rejects_actual_push(self):
        remote, _, head = self.incoming_private_history(legacy=True, old_topic=True)
        (self.repo / ".config/lefthook/pre-push/pushed-checks.sh").unlink()
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/review/removal", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"script does not exist", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/review/removal").stdout, b"")

    def test_real_push_rejects_private_add_then_delete_in_requested_history(self):
        remote, base, head = self.incoming_private_history()
        self.write("README.md", "# Unstaged unrelated content\n", stage=False)
        for destination in ("refs/heads/main", "refs/heads/checkpoint/private"):
            result = command(self.repo, "git", "push", "origin", f"{head}:{destination}", ok=False)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
            self.assertNotIn(b"PRIVATE_HISTORY_SENTINEL", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)
        self.assertEqual(command(remote, "git", "for-each-ref", "refs/heads/checkpoint/private").stdout, b"")
        self.assertEqual((self.repo / "README.md").read_text(), "# Unstaged unrelated content\n")

    def test_real_push_rejects_deleted_private_gitlink_history(self):
        remote, base, head = self.incoming_private_history(gitlink=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/main", ok=False)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), base)

    def test_real_push_allows_removal_of_already_remote_private_state(self):
        remote, _, head = self.incoming_private_history(legacy=True)
        result = command(self.repo, "git", "push", "origin", f"{head}:refs/heads/main")
        self.assertIn(f"Push: {head[:12]} checked".encode(), result.stdout + result.stderr)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/main").stdout.decode().strip(), head)
        self.assertEqual(command(remote, "git", "ls-tree", "-r", "--name-only", head, "--", ".workingdir").stdout, b"")
        self.assertEqual((Path(self.temp.name) / "external/.workingdir/private.txt").read_text(),
                         "PRIVATE_HISTORY_SENTINEL\n")

    def test_command_line_git_config_stops_at_the_registered_pre_push_boundary(self):
        persisted = Path(self.temp.name) / "persisted.git"
        transient = Path(self.temp.name) / "transient.git"
        command(self.repo, "git", "init", "--bare", "-q", str(persisted))
        command(self.repo, "git", "init", "--bare", "-q", str(transient))
        command(self.repo, "git", "remote", "add", "origin", str(persisted))
        probe = '''import os
import subprocess

expected = os.environ.get("PRAETOR_TEST_SNAPSHOT_ORIGIN")
if expected:
    transient = [key for key in os.environ if key in {"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"}
                 or key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"))]
    if transient:
        raise SystemExit("transient Git config reached gate child: " + ",".join(sorted(transient)))
    actual = subprocess.run(["git", "config", "--get", "remote.origin.url"],
                            capture_output=True, text=True, timeout=10, check=True).stdout.strip()
    if actual != expected:
        raise SystemExit(f"snapshot origin {actual!r} != persisted origin {expected!r}")
print("fixture hook self-tests passed")
'''
        self.write(".config/lefthook/scripts/test_hooks.py", probe)
        command(self.repo, "git", "commit", "-q", "-s", "-m", "test: add isolation probe")
        marker = {"PRAETOR_TEST_SNAPSHOT_ORIGIN": str(persisted)}
        with mock.patch.dict(os.environ, marker, clear=False):
            pushed = command(self.repo, "git", "-c", f"remote.origin.url={transient}",
                             "push", "-q", "origin", "HEAD:refs/heads/config-isolation")
        self.assertIn(b"fixture hook self-tests passed", pushed.stdout + pushed.stderr)
        self.assertTrue(command(transient, "git", "show-ref", "--verify",
                                "refs/heads/config-isolation").stdout)

    def hook_env(self, index=None, **extra):
        """The environment a pre-commit hook inherits; ``index`` is the one Git records."""
        env = {key: value for key, value in os.environ.items()
               if key not in {"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"}
               and key not in {"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"}
               and not key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"))}
        if index is not None:
            env["GIT_INDEX_FILE"] = str(index)
        return {**env, **LOCALE_ENV, **extra}

    def test_index_snapshot_exports_the_hook_index_without_command_line_config(self):
        # `git commit -a` and `git commit <path>` run pre-commit with GIT_INDEX_FILE naming
        # index.lock or next-index-*.lock. The export must hold the bytes that commit records,
        # not the stale .git/index, while the caller's -c configuration stays outside it.
        self.write("data.json", '{"index": "stale"}\n')
        alternate = Path(self.temp.name) / "next-index-hook.lock"
        shutil.copyfile(self.repo / ".git/index", alternate)
        (self.repo / "data.json").write_text('{"index": "recorded"}\n', newline="\n")
        subprocess.run(["git", "add", "--", "data.json"], cwd=self.repo, check=True,
                       env=self.hook_env(alternate), capture_output=True, timeout=30)
        attributes = Path(self.temp.name) / "leak-attributes"
        attributes.write_text("* filter=leak\n", newline="\n")
        smudge = "sed s/^/LEAKED/"
        encodings = {
            "count": {"GIT_CONFIG_COUNT": "2",
                      "GIT_CONFIG_KEY_0": "core.attributesFile",
                      "GIT_CONFIG_VALUE_0": attributes.as_posix(),
                      "GIT_CONFIG_KEY_1": "filter.leak.smudge", "GIT_CONFIG_VALUE_1": smudge},
            "parameters": {"GIT_CONFIG_PARAMETERS":
                           f"'core.attributesfile'='{attributes.as_posix()}' "
                           f"'filter.leak.smudge'='{smudge}'"},
        }
        for name, leak in encodings.items():
            with self.subTest(encoding=name):
                env = self.hook_env(alternate, **leak)
                # Control: the injected configuration is live for an unscrubbed Git, so the
                # clean export below is evidence rather than an assertion that cannot fail.
                with tempfile.TemporaryDirectory() as control:
                    subprocess.run(["git", "checkout-index", "--all", "--force",
                                    f"--prefix={control}/"], cwd=self.repo, env=env,
                                   check=True, capture_output=True, timeout=30)
                    self.assertTrue((Path(control) / "data.json").read_bytes()
                                    .startswith(b"LEAKED"))
                with mock.patch.dict(os.environ, env, clear=True), \
                        contextlib.chdir(self.repo), snapshot() as directory:
                    self.assertEqual((directory / "data.json").read_bytes(),
                                     b'{"index": "recorded"}\n')
                    self.assertEqual((directory / "README.md").read_bytes(), b"# Fixture\n")

    def test_snapshot_pins_autocrlf_against_persistent_configuration(self):
        # Scrubbing -c configuration leaves the operator's persistent configuration in force,
        # and Git for Windows sets core.autocrlf=true there. Each nested checkout must pin it.
        self.write("data.json", '{"line": "lf"}\n')
        command(self.repo, "git", "commit", "-q", "-s", "-m", "test: commit lf fixture")
        persistent = Path(self.temp.name) / "autocrlf-gitconfig"
        persistent.write_text("[core]\n\tautocrlf = true\n", newline="\n")
        env = self.hook_env(GIT_CONFIG_GLOBAL=str(persistent), GIT_CONFIG_NOSYSTEM="1")
        with tempfile.TemporaryDirectory() as control:
            subprocess.run(["git", "checkout-index", "--all", "--force", f"--prefix={control}/"],
                           cwd=self.repo, env=env, check=True, capture_output=True, timeout=30)
            self.assertEqual((Path(control) / "data.json").read_bytes(), b'{"line": "lf"}\r\n')
        for ref in (None, "HEAD"):
            with self.subTest(ref=ref), mock.patch.dict(os.environ, env, clear=True), \
                    contextlib.chdir(self.repo), snapshot(ref) as directory:
                self.assertEqual((directory / "data.json").read_bytes(), b'{"line": "lf"}\n')

    def test_commit_all_and_path_commit_check_the_bytes_git_records(self):
        self.write("data.json", '{"seed": 0}\n')
        command(self.repo, "git", "commit", "-q", "-s", "-m", "test: seed json fixture")
        for index, mode in enumerate((("-a",), ("--", "data.json"))):
            with self.subTest(mode=mode):
                head = command(self.repo, "git", "rev-parse", "HEAD").stdout
                # .git/index holds valid JSON; the bytes the commit records do not.
                self.write("data.json", '{"broken":\n', stage=False)
                rejected = command(self.repo, "git", "commit", "-q", "-s", "-m",
                                   "test: record broken json", *mode, ok=False)
                self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
                self.assertIn(b"data.json: Expecting value", rejected.stdout + rejected.stderr)
                self.assertEqual(command(self.repo, "git", "rev-parse", "HEAD").stdout, head)
                # .git/index holds invalid JSON; the bytes the commit records are valid.
                self.write("data.json", '{"broken":\n')
                recorded = f'{{"recorded": {index}}}\n'
                self.write("data.json", recorded, stage=False)
                accepted = command(self.repo, "git", "commit", "-q", "-s", "-m",
                                   "test: record valid json", *mode, ok=False)
                self.assertEqual(accepted.returncode, 0, accepted.stdout + accepted.stderr)
                self.assertEqual(command(self.repo, "git", "show", "HEAD:data.json").stdout,
                                 recorded.encode())

    def test_merge_retaining_remote_private_baseline_is_not_new_private_content(self):
        external = Path(self.temp.name) / "merge-source"
        external.mkdir()
        command(external, "git", "init", "-q", "-b", "main")
        command(external, "git", "config", "user.name", "External Fixture")
        command(external, "git", "config", "user.email", "external@example.test")
        (external / "README.md").write_text("# Fixture\n")
        command(external, "git", "add", ".")
        command(external, "git", "commit", "-q", "-s", "-m", "chore: initial source")
        command(external, "git", "branch", "side")
        (external / ".workingdir").mkdir()
        (external / ".workingdir/private.txt").write_text("old published fixture\n")
        command(external, "git", "add", ".workingdir")
        command(external, "git", "commit", "-q", "-s", "-m", "docs: old remote state")
        base = command(external, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(external, "git", "checkout", "-q", "side")
        (external / "other.md").write_text("public change\n")
        command(external, "git", "add", "other.md")
        command(external, "git", "commit", "-q", "-s", "-m", "docs: side change")
        command(external, "git", "checkout", "-q", "main")
        command(external, "git", "merge", "-q", "--no-ff", "-m", "Merge side fixture", "side")
        original = Path.cwd()
        try:
            os.chdir(external)
            check_private_history("HEAD", base)
        finally:
            os.chdir(original)

    def test_unknown_push_baseline_still_checks_private_history(self):
        _, _, head = self.incoming_private_history()
        protocol = f"refs/heads/incoming {head} refs/heads/main {'f' * 40}\n".encode()
        result = self.hook("pre-push", "origin", data=protocol)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"scope: full tree", result.stdout + result.stderr)
        self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)

    def test_private_symlink_and_ancestor_rejected_without_reading_target(self):
        target = Path(self.temp.name) / "outside-private"
        target.write_text("PRIVATE_SYMLINK_SENTINEL\n")
        for index, name in enumerate((".workingdir", ".workingdir/docs/link")):
            with self.subTest(name=name):
                self.repo = Path(self.temp.name) / f"symlink-{index}"
                self.initialize_repo()
                if name == ".workingdir":
                    shutil.rmtree(self.repo / ".workingdir")
                link = self.repo / name
                link.parent.mkdir(parents=True, exist_ok=True)
                link.symlink_to(target)
                command(self.repo, "git", "add", "-f", "--", name)
                result = command(self.repo, "git", "commit", "-s", "-m", "docs: private symlink", ok=False)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"Private .workingdir content must stay untracked", result.stdout + result.stderr)
                self.assertNotIn(b"PRIVATE_SYMLINK_SENTINEL", result.stdout + result.stderr)
                self.assertTrue(link.is_symlink())
                self.assertEqual(target.read_text(), "PRIVATE_SYMLINK_SENTINEL\n")

    def test_private_history_commit_bound_and_missing_objects_fail_closed(self):
        """Private commit limit bounds walk, and missing objects or failed checks fail closed."""
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        heads = []
        for number in range(3):
            self.write("README.md", f"# Revision {number}\n")
            command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: bounded history")
            heads.append(command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip())
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with mock.patch("privacy.MAX_PRIVATE_COMMITS", 2):
                check_private_history(heads[1], base)
                with self.assertRaisesRegex(HookError, "exceeds 2 commits"):
                    check_private_history(heads[2], base)
            with self.assertRaises(HookError):
                check_private_history("f" * 40, base)
            with (
                mock.patch("privacy.subprocess.run", return_value=subprocess.CompletedProcess([], 2)),
                self.assertRaisesRegex(HookError, "inspection failed"),
            ):
                check_private_index()
            with (
                mock.patch("privacy.subprocess.run", side_effect=subprocess.TimeoutExpired("git", 60)),
                self.assertRaisesRegex(HookError, "inspection failed"),
            ):
                check_private_index()
        finally:
            os.chdir(original)

    def test_private_history_rejects_incomplete_shallow_ancestry(self):
        shallow = Path(self.temp.name) / "shallow"
        command(self.repo, "git", "clone", "-q", "--depth=1", self.repo.as_uri(), str(shallow))
        original = Path.cwd()
        try:
            os.chdir(shallow)
            with self.assertRaisesRegex(HookError, "shallow repository"):
                check_private_history("HEAD", None)
        finally:
            os.chdir(original)

    def test_codex_adapter_routes_guard_and_normalizes_failures(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        allowed = {"hook_event_name": "PreToolUse", "tool_name": "Bash",
                   "tool_input": {"command": "printf 'policy-test'"}}
        denied = {"tool_input": {"command": "git commit --no-verify"}}
        cases = [(json.dumps(allowed).encode(), 0), (json.dumps(denied).encode(), 2),
                 (b"", 2), (b"not JSON", 2), (b"{}", 2),
                 (b'{"tool_input":{"command":null}}', 2), (b" " * ((1 << 20) + 1), 2)]
        for payload, expected in cases:
            with self.subTest(size=len(payload), prefix=payload[:48]):
                result = command(self.repo, sys.executable, str(adapter), data=payload, ok=False)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertNotIn(b"policy-test\n", result.stdout)

    def test_codex_adapter_missing_guard_blocks(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        (self.repo / ".config/agent/hooks/block_evasion.py").unlink()
        result = command(self.repo, sys.executable, str(adapter),
                         data=b'{"tool_input":{"command":"git status"}}', ok=False)
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)

    def test_codex_adapter_requires_executed_lefthook_job(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        empty_bin = self.repo / "empty-bin"
        empty_bin.mkdir()
        payload = b'{"tool_input":{"command":"git status"}}'
        with mock.patch.dict(os.environ, {"PATH": str(empty_bin)}):
            missing = command(self.repo, sys.executable, str(adapter), data=payload, ok=False)
        self.assertEqual(missing.returncode, 2, missing.stdout + missing.stderr)
        fake = empty_bin / "lefthook"
        fake.write_text("#!/bin/sh\nexit 0\n")
        fake.chmod(0o755)
        with mock.patch.dict(os.environ, {"PATH": str(empty_bin)}):
            skipped = command(self.repo, sys.executable, str(adapter), data=payload, ok=False)
        self.assertEqual(skipped.returncode, 2, skipped.stdout + skipped.stderr)

    def test_lefthook_agent_job_consumes_and_checks_command(self):
        for proposed, allowed in (("git status", True), ("git commit --no-verify", False)):
            result = self.hook("agent-pre-tool", data=json.dumps({
                "tool_input": {"command": proposed},
            }).encode())
            self.assertEqual(result.returncode == 0, allowed, result.stdout + result.stderr)
            if allowed:
                self.assertIn(b"PRAETOR_COMMAND_POLICY_OK", result.stdout)

    def test_hook_output_is_job_output_and_failures_only(self):
        """Every run lands in an agent's context: a pass prints its payload, a failure its reason."""
        allowed = b'{"tool_input":{"command":"git status"}}'
        passed = self.hook("agent-pre-tool", data=allowed)
        self.assertEqual(passed.returncode, 0, passed.stdout + passed.stderr)
        self.assertEqual((passed.stdout + passed.stderr).strip(), b"PRAETOR_COMMAND_POLICY_OK")
        denied = self.hook("agent-pre-tool", data=json.dumps(
            {"tool_input": {"command": "git commit --no-" + "verify"}}).encode())
        output = denied.stdout + denied.stderr
        self.assertNotEqual(denied.returncode, 0, output)
        self.assertIn(b"[BLOCKED BY HISS]", output)
        self.assertIn(b"command-policy", output)
        self.assertNotIn(b"\x1b[", output)
        # The environment override still restores Lefthook's own reporting for a human.
        with mock.patch.dict(os.environ, {"LEFTHOOK_OUTPUT": "meta,execution_out"}):
            verbose = self.hook("agent-pre-tool", data=allowed)
        self.assertIn(b"agent-pre-tool", verbose.stdout + verbose.stderr)
        self.assertIn(b"PRAETOR_COMMAND_POLICY_OK\n", verbose.stdout)

    def test_codex_hook_configuration_runs_from_nested_directory(self):
        self.write(".codex/hooks.json", (ROOT / ".codex/hooks.json").read_text())
        settings = json.loads((self.repo / ".codex/hooks.json").read_text())
        registration = settings["hooks"]["PreToolUse"][0]
        self.assertRegex("Bash", registration["matcher"])
        self.assertNotRegex("Write", registration["matcher"])
        action = registration["hooks"][0]
        self.assertEqual(action["type"], "command")
        nested = self.repo / "nested directory"
        nested.mkdir()
        for command_text, expected in (("git status", 0), ("git commit --no-verify", 2)):
            payload = json.dumps({"hook_event_name": "PreToolUse", "tool_name": "Bash",
                                  "tool_input": {"command": command_text}}).encode()
            process = codex_command(action)
            result = (run_command(nested, process, payload, False) if isinstance(process, str)
                      else command(nested, *process, data=payload, ok=False))
            self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

    def test_guard_and_command_guard_enforce_admission_through_praetorctl(self):
        """With hooks.admission.enforce enabled, block_evasion and command_guard reach praetorctl."""
        fleet_on = Path(self.temp.name) / "fleet-on.yaml"
        fleet_on.write_text("hooks:\n  admission:\n    enforce: true\n", newline="\n")
        fleet_off = Path(self.temp.name) / "fleet-off.yaml"
        fleet_off.write_text("hooks:\n  admission:\n    enforce: false\n", newline="\n")
        payload = json.dumps({"cwd": str(self.repo), "tool_input": {"command": "make verify-all"}}).encode()
        admitted = json.dumps({"cwd": str(self.repo), "tool_input": {"command": "praetorctl admit -- make"}}).encode()
        # Enforcement off: allowed without praetorctl.
        (self.repo / PRAETORCTL).unlink()
        env_off = {**os.environ, "PATH": "", "PRAETOR_FLEET_CONFIG": str(fleet_off)}
        off_res = subprocess.run([sys.executable, "-B", str(GUARD)], cwd=self.repo, env=env_off,
                                 input=payload, capture_output=True, timeout=10, check=False)
        self.assertEqual(off_res.returncode, 0, off_res.stderr)
        self.assertIn(b"PRAETOR_COMMAND_POLICY_OK", off_res.stdout)
        # Enforcement on, praetorctl absent: refused (fail closed).
        env_absent = {**os.environ, "PATH": "", "PRAETOR_FLEET_CONFIG": str(fleet_on)}
        absent_res = subprocess.run([sys.executable, "-B", str(GUARD)], cwd=self.repo, env=env_absent,
                                    input=payload, capture_output=True, timeout=10, check=False)
        self.assertNotEqual(absent_res.returncode, 0)
        self.assertIn(b"[BLOCKED BY HISS]", absent_res.stderr)
        # Enforcement on, praetorctl present in self.repo: unwrapped make refused, admitted passes.
        os.link(self.binary, self.repo / PRAETORCTL)
        env_on = {**os.environ, "PRAETOR_FLEET_CONFIG": str(fleet_on)}
        cmd_guard = self.repo / ".config/agent/hooks/command_guard.py"
        refused = subprocess.run([sys.executable, "-B", str(cmd_guard)],
                                 cwd=self.repo, env=env_on, input=payload, capture_output=True,
                                 timeout=15, check=False)
        self.assertEqual(refused.returncode, 2, refused.stderr)
        self.assertIn(b"heavy step needs host admission: make", refused.stderr)
        allowed = subprocess.run([sys.executable, "-B", str(cmd_guard)],
                                 cwd=self.repo, env=env_on, input=admitted, capture_output=True,
                                 timeout=15, check=False)
        self.assertEqual(allowed.returncode, 0, allowed.stderr)

    @unittest.skipIf(os.name == "nt", "the stale-binary stand-in is a POSIX shell script")
    def test_guard_skips_a_praetorctl_older_than_admission_check(self):
        """A bin/praetorctl built before admission-check (it answers with the usage error) is skipped
        for praetorctl on PATH, so a stale checkout binary never refuses every command; with no binary
        that knows the check the guard refuses naming both (order engine-hook-build-scope-gate-20261027)."""
        fleet_on = Path(self.temp.name) / "fleet-on.yaml"
        fleet_on.write_text("hooks:\n  admission:\n    enforce: true\n", newline="\n")
        payload = json.dumps({"cwd": str(self.repo), "tool_input": {"command": "make verify-all"}}).encode()
        listing = json.dumps({"cwd": str(self.repo), "tool_input": {"command": "ls -la"}}).encode()
        stale = self.repo / PRAETORCTL
        stale.unlink()
        stale.write_text("#!/bin/sh\necho 'Error: unknown command: admission-check' >&2\nexit 1\n", newline="\n")
        stale.chmod(0o755)
        fresh = Path(self.temp.name) / "path-bin"
        fresh.mkdir()
        os.link(self.binary, fresh / "praetorctl")
        env = {**os.environ, "PATH": str(fresh), "PRAETOR_FLEET_CONFIG": str(fleet_on)}
        for data, code, text in ((payload, 2, b"heavy step needs host admission: make"), (listing, 0, b"")):
            result = subprocess.run([sys.executable, "-B", str(GUARD)], cwd=self.repo, env=env,
                                    input=data, capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode != 0, code != 0, result.stderr)
            self.assertIn(text, result.stderr)
        env_stale_only = {**os.environ, "PATH": "", "PRAETOR_FLEET_CONFIG": str(fleet_on)}
        result = subprocess.run([sys.executable, "-B", str(GUARD)], cwd=self.repo, env=env_stale_only,
                                input=listing, capture_output=True, timeout=10, check=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"praetorctl too old", result.stderr)

    def test_good_index_ignores_unstaged_python_syntax_error(self):
        self.write("strange ; $ name.py", "value = 1\n")
        self.write("strange ; $ name.py", "def invalid(:\n", stage=False)
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_invalid_encoded_source_bytes_are_rejected(self):
        for name in ("bad.py", "bad.json"):
            (self.repo / name).write_bytes(b'"\xff"\n')
            command(self.repo, "git", "add", "--", name)
            self.assertNotEqual(self.hook().returncode, 0)
            command(self.repo, "git", "rm", "--cached", "--", name)

    def test_delete_only_and_empty_index(self):
        self.assertEqual(self.hook().returncode, 0)
        command(self.repo, "git", "rm", "README.md")
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_staged_policy_validates_and_runs_its_behavioral_gate(self):
        self.write("lefthook.yml", (self.repo / "lefthook.yml").read_text() + "# policy edit\n")
        result = self.hook()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"fixture hook self-tests passed", result.stdout + result.stderr)
        self.write("lefthook.yml", "pre-commit: [malformed\n")
        self.assertNotEqual(self.hook().returncode, 0)

    def test_client_settings_alone_run_hook_behavioral_gate(self):
        for name in (".claude/settings.json", ".gemini/settings.json"):
            with self.subTest(client=name):
                self.write(name, (ROOT / name).read_text())
                result = self.hook()
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(b"fixture hook self-tests passed", result.stdout + result.stderr)
                command(self.repo, "git", "reset", "-q", "HEAD", "--", name)

    def test_gofmt_failure_does_not_modify_or_stage_file(self):
        self.write("go.mod", "module example.test/hooks\n\ngo 1.27\n")
        self.write("main.go", "package main\nfunc main(){println(1)}\n")
        before = (self.repo / "main.go").read_bytes()
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"gofmt", result.stdout + result.stderr)
        self.assertEqual(before, (self.repo / "main.go").read_bytes())

    def test_fmt_check_skips_testdata_and_names_unformatted_source(self):
        # `make fmt-check` and CI run this stage over every tracked Go file.
        def fmt_check():
            return command(self.repo, sys.executable, "-B", str(RUNNER), "fmt-check", ok=False)

        # Boundary: a tree without Go files passes.
        result = fmt_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Positive: formatted source passes beside a deliberately unformatted testdata fixture.
        self.write("go.mod", "module example.test/fmt\n\ngo 1.27\n")
        self.write("main.go", "package main\n\nfunc main() {}\n")
        self.write("testdata/fixture.go", "package fixture\nfunc  f(){}\n")
        result = fmt_check()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Negative: an unformatted tracked source file fails and is named.
        self.write("pkg/bad.go", "package pkg\nfunc  f(){}\n")
        result = fmt_check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"pkg/bad.go", result.stdout + result.stderr)
        self.assertNotIn(b"testdata/fixture.go", result.stdout + result.stderr)

    def test_vet_checks_staged_go_package(self):
        self.write("go.mod", "module example.test/hooks\n\ngo 1.27\n")
        self.write("main.go", 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Printf("%d", "bad") }\n')
        result = self.hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b"wrong type", result.stdout + result.stderr)

    def test_message_good_negative_and_missing_dco(self):
        message = self.repo / "message.txt"
        message.write_text("fix(hooks): gate changes\n\nSigned-off-by: Hook Test <hook@example.test>\n")
        self.assertEqual(self.hook("commit-msg", message.as_posix()).returncode, 0)
        message.write_text("fix: missing signoff\n")
        self.assertNotEqual(self.hook("commit-msg", message.as_posix()).returncode, 0)
        message.write_text("bad subject\n\nSigned-off-by: Hook Test <hook@example.test>\n")
        self.assertNotEqual(self.hook("commit-msg", message.as_posix()).returncode, 0)

    def test_commit_requires_explicit_state_sync_after_staging(self):
        self.write("README.md", "# Staged after sync\n")
        before = command(self.repo, "git", "rev-parse", "HEAD").stdout
        rejected = command(self.repo, "git", "commit", "-s", "-m", "docs: stale state",
                           ok=False, maintain_state=False)
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn(b"state synchronization stale", rejected.stdout + rejected.stderr)
        self.assertEqual(command(self.repo, "git", "rev-parse", "HEAD").stdout, before)
        command(self.repo, cli_path(self.repo), "state", "sync", ".")
        command(self.repo, "git", "commit", "-s", "-m", "docs: maintained state", maintain_state=False)
        command(self.repo, cli_path(self.repo), "state", "sync", "--verify", ".")

    def test_precommit_missing_and_incomplete_ledger_are_not_repaired(self):
        self.write("README.md", "# Need live ledger\n")
        for name in ("BACKLOG.md", "STATE.md"):
            with self.subTest(name=name):
                path = self.repo / ".workingdir" / name
                before = path.read_bytes()
                path.unlink()
                rejected = self.hook(maintain_state=False)
                self.assertNotEqual(rejected.returncode, 0)
                self.assertFalse(path.exists())
                path.write_bytes(before)
        shutil.rmtree(self.repo / ".workingdir")
        self.assertNotEqual(self.hook(maintain_state=False).returncode, 0)
        self.assertFalse((self.repo / ".workingdir").exists())

    def test_postcommit_sync_failure_is_reported(self):
        (self.repo / ".workingdir/BACKLOG.md").unlink()
        rejected = self.hook("post-commit", maintain_state=False)
        self.assertNotEqual(rejected.returncode, 0)
        self.assertIn(b"state sync failed", rejected.stdout + rejected.stderr)

    def test_initial_commit_has_valid_unborn_state(self):
        command(self.repo, "git", "checkout", "--orphan", "new-history")
        command(self.repo, "git", "rm", "-r", "--cached", ".config", "lefthook.yml")
        command(self.repo, cli_path(self.repo), "state", "sync", ".")
        command(self.repo, "git", "commit", "-s", "-m", "chore: initial history", maintain_state=False)
        command(self.repo, cli_path(self.repo), "state", "sync", "--verify", ".")

    def test_prepare_does_not_invent_attestation(self):
        message = self.repo / "message.txt"
        message.write_text("")
        # Invoked as git invokes it for a commit with no message source: one argument, never an
        # empty second one. The empty string this used to pass is not a shape git produces, and
        # on Windows Lefthook could not even hand it to sh.
        result = self.hook("prepare-commit-msg", message.as_posix())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("type(scope)", message.read_text())
        self.assertNotIn("Signed-off-by:", message.read_text())

    def test_prepare_message_sources(self):
        message = self.repo / "sourced.txt"
        for source in ("message", "template", "merge", "squash", "commit"):
            message.write_text("")
            prepare_message(str(message), source)
            self.assertEqual(message.read_text(), "", source)
        for absent in ("", "2", "{2}"):
            message.write_text("")
            prepare_message(str(message), absent)
            self.assertIn("type(scope)", message.read_text(), absent)
        message.write_text("fix: already written\n")
        prepare_message(str(message), "2")
        self.assertEqual(message.read_text(), "fix: already written\n")

    def test_refactor_range_uses_exact_base_or_skips_without_one(self):
        with mock.patch("hooks.cli") as run_cli:
            hooks.check_refactor_range("base-sha", "head-sha")
        run_cli.assert_called_once_with([
            "forge", "refactor-check", "--base=base-sha", "--head=head-sha",
        ])
        for base in (None, ""):
            with self.subTest(base=base), mock.patch("hooks.cli") as run_cli, \
                    mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                hooks.check_refactor_range(base, "head-sha")
            run_cli.assert_not_called()
            self.assertIn("Refactor check: skipped, no pushed-range base", out.getvalue())

    def test_docstring_range_uses_exact_base_or_skips_without_one(self):
        """Verify check_docstring_range runs with an exact base and skips when absent."""
        with mock.patch("hooks.cli") as run_cli:
            hooks.check_docstring_range("base-sha", "head-sha")
        run_cli.assert_called_once_with([
            "docstrings", "check", "--base=base-sha", "--head=head-sha",
        ])
        for base in (None, ""):
            with self.subTest(base=base), mock.patch("hooks.cli") as run_cli, \
                    mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                hooks.check_docstring_range(base, "head-sha")
            run_cli.assert_not_called()
            self.assertIn("Docstring check: skipped, no pushed-range base", out.getvalue())

    def test_real_push_initial_docs_update_new_branch_and_deletion(self):
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        first = command(self.repo, "git", "push", "-u", "origin", "main")
        self.assertIn(STATE_VERIFIED, first.stdout + first.stderr)
        self.assertIn(b"Push:", first.stdout + first.stderr)
        self.write("README.md", "# Pushed docs\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: update fixture")
        self.assertEqual(command(self.repo, "git", "push").returncode, 0)
        command(self.repo, "git", "checkout", "-q", "-b", "topic")
        self.assertEqual(command(self.repo, "git", "push", "origin", "topic").returncode, 0)
        self.assertEqual(command(self.repo, "git", "push", "origin", "--delete", "topic").returncode, 0)

    def test_push_checks_requested_ref_and_ignores_dirty_worktree(self):
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("bad.json", "{", stage=False)
        protocol = f"refs/heads/main {head} refs/heads/main {'0' * 40}\n".encode()
        result = self.hook("pre-push", "origin", data=protocol)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue((self.repo / "bad.json").exists())
        missing = protocol.replace(b"0" * 40, b"f" * 40)
        result = self.hook("pre-push", "origin", data=missing)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(b"scope: full tree", result.stdout + result.stderr)
        self.assertNotEqual(self.hook("pre-push", "origin", data=b"broken\n").returncode, 0)

    def test_push_passes_actual_base_to_snapshot_audit(self):
        """Verify pre_push forwards the resolved base revision to snapshot and range checks."""
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("README.md", "# Range audit\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: test pushed range")
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        protocol = f"refs/heads/main {head} refs/heads/main {base}\n"
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            # Drive the Windows checkout default on every host. The hook must inspect the
            # committed bytes, not let core.autocrlf rewrite its isolated snapshot.
            with mock.patch.dict(os.environ, AUTOCRLF_ENV), \
                    mock.patch("hooks.sys.stdin", io.StringIO(protocol)), \
                    mock.patch("hooks.source_checks", return_value=False) as check, \
                    mock.patch("hooks.check_refactor_range") as refactors, \
                    mock.patch("hooks.check_docstring_range") as docstrings, \
                    mock.patch("hooks.check_merge_range") as merges:
                pre_push("origin")
                self.assertEqual(check.call_args.kwargs, {"base": base})
                self.assertEqual(check.call_args.args[1], ["README.md"])
                refactors.assert_called_once_with(base, head)
                docstrings.assert_called_once_with(base, head)
                merges.assert_called_once_with(base, head)
            with mock.patch.dict(os.environ, AUTOCRLF_ENV), \
                    mock.patch("hooks.sys.stdin", io.StringIO(protocol.replace(base, "f" * 40))), \
                    mock.patch("hooks.source_checks", return_value=False) as check, \
                    mock.patch("hooks.check_refactor_range"), \
                    mock.patch("hooks.check_docstring_range"), \
                    mock.patch("hooks.check_merge_range"):
                pre_push("origin")
                self.assertIsNone(check.call_args.kwargs["base"])
                self.assertIn("lefthook.yml", check.call_args.args[1])
        finally:
            os.chdir(original)

    def test_force_push_of_rewritten_history_scans_against_merge_base(self):
        """Verify a rebased branch is scanned against the merge base, not its replaced head.

        Diffing against the replaced head counts every commit the rebase picked up from the
        default branch as this push's work, so the range checks judged code the push did not
        write (#1741: docstring coverage counted the default branch's new functions).
        """
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("README.md", "# Old branch head\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: old branch head")
        old = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        tree = command(self.repo, "git", "rev-parse", f"{old}^{{tree}}").stdout.decode().strip()
        orphan = command(self.repo, "git", "commit-tree", tree,
                         "-m", "docs: unrelated root").stdout.decode().strip()
        command(self.repo, "git", "reset", "-q", "--hard", base)
        self.write("README.md", "# Rewritten branch head\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: rewritten branch head")
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            for replaced in (old, orphan):
                protocol = f"refs/heads/main {head} refs/heads/main {replaced}\n"
                with self.subTest(replaced=replaced), mock.patch.dict(os.environ, AUTOCRLF_ENV), \
                        mock.patch("hooks.sys.stdin", io.StringIO(protocol)), \
                        mock.patch("hooks.new_branch_base", return_value=base) as merge_base, \
                        mock.patch("hooks.source_checks", return_value=False) as check, \
                        mock.patch("hooks.check_refactor_range") as refactors, \
                        mock.patch("hooks.check_docstring_range") as docstrings, \
                        mock.patch("hooks.check_merge_range") as merges:
                    pre_push("origin")
                    merge_base.assert_called_once_with(head, "origin", False)
                    self.assertEqual(check.call_args.kwargs, {"base": base})
                    self.assertEqual(check.call_args.args[1], ["README.md"])
                    refactors.assert_called_once_with(base, head)
                    docstrings.assert_called_once_with(base, head)
                    merges.assert_called_once_with(base, head)
            with mock.patch("hooks.new_branch_base") as merge_base:
                self.assertEqual(hooks.push_base(head, base, "origin"), base)
                self.assertEqual(hooks.push_base(head, "f" * 40, "origin"), "f" * 40)
                merge_base.assert_not_called()
        finally:
            os.chdir(original)

    def test_push_refuses_dropped_hunk_merge_and_passes_clean_merge(self):
        """Verify pre-push refuses merge commits that drop parent hunks and accepts clean merges."""
        # Order: engine-dropped-hunk-gate-20261027.
        alpha_base = "line 1\nline 2\nline 3\nline 4\nline 5\n"
        self.write("alpha.txt", alpha_base)
        command(self.repo, "git", "commit", "-q", "-s", "-m", "chore: base commit")

        # Set up a bare remote to push to
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        command(self.repo, "git", "push", "-q", "origin", "main")

        # Branch X: modify line 2
        command(self.repo, "git", "checkout", "-q", "-b", "branch-x")
        self.write("alpha.txt", "line 1\nline 2 modified by X\nline 3\nline 4\nline 5\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat: branch X edit")

        # Branch Y: from main, modify line 5
        command(self.repo, "git", "checkout", "-q", "-b", "branch-y", "main")
        self.write("alpha.txt", "line 1\nline 2\nline 3\nline 4\nline 5 modified by Y\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat: branch Y edit")

        # Merge branch-y into branch-x taking Y whole file (drops X line 2)
        command(self.repo, "git", "checkout", "-q", "branch-x")
        run_command(self.repo, ("git", "merge", "--no-commit", "branch-y"), None, False)
        command(self.repo, "git", "checkout", "branch-y", "--", "alpha.txt")
        command(self.repo, "git", "add", "alpha.txt")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat(test): bad merge taking branch-y whole file")

        # Push branch-x with bad merge: MUST BE REFUSED
        result = command(self.repo, "git", "push", "origin", "branch-x", ok=False)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertNotEqual(result.returncode, 0, output)
        self.assertIn("alpha.txt", output)
        self.assertIn("Fix: resolve inside conflict markers, re-merge.", output)

        # Reset merge and resolve keeping both hunks
        command(self.repo, "git", "reset", "--hard", "HEAD~1")
        run_command(self.repo, ("git", "merge", "--no-commit", "branch-y"), None, False)
        self.write("alpha.txt", "line 1\nline 2 modified by X\nline 3\nline 4\nline 5 modified by Y\n")
        command(self.repo, "git", "add", "alpha.txt")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat(test): merge resolved per hunk")

        # Push branch-x with clean merge: MUST PASS
        result = command(self.repo, "git", "push", "origin", "branch-x", ok=False)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertEqual(result.returncode, 0, output)

    def test_push_refuses_commit_message_body_exceeding_72_columns(self):
        """Verify pre-push enforces 72-column commit body wrapping on the pushed range (#1814)."""
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        command(self.repo, "git", "push", "-q", "origin", "main")

        command(self.repo, "git", "checkout", "-q", "-b", "topic")
        self.write("doc1.txt", "doc1\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: compliant commit one")

        # Boundary: 72 columns passes
        line72 = "word " * 14 + "ab"  # 14*5 + 2 = 72 columns
        msg72 = f"docs: boundary 72 columns\n\n{line72}\n"
        self.write("doc2.txt", "doc2\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", msg72)

        # Range of compliant commits passes
        result = command(self.repo, "git", "push", "origin", "topic", ok=False)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertEqual(result.returncode, 0, output)

        # Boundary: 73 columns is refused; bypass commit-msg with --no-verify
        line73 = "word " * 14 + "abc"  # 14*5 + 3 = 73 columns
        msg73 = f"docs: boundary 73 columns\n\n{line73}\n"
        self.write("doc3.txt", "doc3\n")
        command(self.repo, "git", "commit", "--no-verify", "-q", "-s", "-m", msg73)
        bad_73 = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        result = command(self.repo, "git", "push", "origin", "topic", ok=False)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertNotEqual(result.returncode, 0, output)
        self.assertIn(bad_73[:12], output)
        self.assertIn("73 columns", output)

        # Reset commit so branch is clean
        command(self.repo, "git", "reset", "--hard", "HEAD~1")

        # Planted defect: 90-column body line in pushed range is refused, output names commit
        line90 = "word " * 17 + "words"  # 17*5 + 5 = 90 columns
        msg90 = f"docs: planted 90 columns\n\n{line90}\n"
        self.write("doc4.txt", "doc4\n")
        command(self.repo, "git", "commit", "--no-verify", "-q", "-s", "-m", msg90)
        bad_90 = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        # Add a compliant commit on top to prove the check scans the full range, not only head
        self.write("doc5.txt", "doc5\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: compliant commit after bad")

        result = command(self.repo, "git", "push", "origin", "topic", ok=False)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertNotEqual(result.returncode, 0, output)
        self.assertIn(bad_90[:12], output)

    def test_commit_message_range_uses_exact_base_or_skips_without_one(self):
        """Verify check_commit_message_range runs with an exact base and skips when absent."""
        if not hasattr(hooks, "check_commit_message_range"):
            self.skipTest("check_commit_message_range not present")
        with mock.patch("hooks.praetorctl_path", return_value="praetorctl"), \
                mock.patch("hooks.run") as run_cmd:
            hooks.check_commit_message_range("base-sha", "head-sha")
        run_cmd.assert_called_once_with(
            ["praetorctl", "forge", "check-commits", "--base=base-sha", "--head=head-sha"],
            capture=False, timeout=180,
        )
        for base in (None, ""):
            with self.subTest(base=base), mock.patch("hooks.run") as run_cmd, \
                    mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                hooks.check_commit_message_range(base, "head-sha")
            run_cmd.assert_not_called()
            self.assertIn("Commit message check: skipped, no pushed-range base", out.getvalue())

    def test_commit_message_range_skips_published_commits_in_two_remote_fixture(self):
        """In a two-remote fixture, commits published on upstream are skipped while own commits fail."""
        origin = Path(self.temp.name) / "origin.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(origin))
        command(self.repo, "git", "remote", "add", "origin", str(origin))
        command(self.repo, "git", "push", "-q", "-u", "origin", "main")
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        upstream = Path(self.temp.name) / "upstream.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(upstream))
        command(self.repo, "git", "remote", "add", "upstream", str(upstream))

        # 1. Upstream commit with 74-column body line (simulating upstream squash commit)
        body_74 = ("word " * 14) + "abcd"
        self.write("upstream.txt", "upstream\n")
        command(
            self.repo, "git", "commit", "-q", "-s", "--no-verify",
            "-m", f"feat: upstream squash commit\n\n{body_74}",
        )
        upstream_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "push", "-q", "-u", "upstream", "main")

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            hooks.check_commit_message_range(base, upstream_sha, remote="origin")

            # 2. Own commit with 74-column body line on top of upstream commit
            self.write("own.txt", "own\n")
            command(
                self.repo, "git", "commit", "-q", "-s", "--no-verify",
                "-m", f"feat: own commit\n\n{body_74}",
            )
            own_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

            with self.assertRaises(HookError):
                hooks.check_commit_message_range(base, own_sha, remote="origin")
        finally:
            os.chdir(original)

    def setup_ops_sync_fixture(self):
        """Set up origin, upstream with a 74-column commit, and return (base, upstream_sha, body_74)."""
        origin = Path(self.temp.name) / "origin.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(origin))
        command(self.repo, "git", "remote", "add", "origin", str(origin))
        command(self.repo, "git", "push", "-q", "-u", "origin", "main")
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        upstream = Path(self.temp.name) / "upstream.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(upstream))
        command(self.repo, "git", "remote", "add", "upstream", str(upstream))

        # Upstream main branch holds a tip commit with a 74-column body line
        command(self.repo, "git", "checkout", "-q", "-b", "upstream-main")
        body_74 = ("word " * 14) + "abcd"
        self.write("upstream.txt", "upstream\n")
        command(
            self.repo, "git", "commit", "-q", "-s", "--no-verify",
            "-m", f"feat: upstream tip\n\n{body_74}",
        )
        upstream_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "push", "-q", "-u", "upstream", "upstream-main:main")
        command(self.repo, "git", "checkout", "-q", "main")
        return base, upstream_sha, body_74

    def test_commit_message_range_ops_sync_merge_passes_and_skips_upstream_ref(self):
        """Own merge commit of upstream main passes pre-push check and skips upstream ref."""
        base, _, _ = self.setup_ops_sync_fixture()
        valid_merge_msg = "Merge branch 'main' of upstream into main\n\nSigned-off-by: Hook Test <hook@example.test>"
        command(
            self.repo, "git", "merge", "--no-ff", "-q", "--no-verify",
            "-m", valid_merge_msg, "upstream/main",
        )
        merge_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            hooks.check_commit_message_range(base, merge_sha, remote="origin")
        finally:
            os.chdir(original)

    def test_commit_message_range_ops_sync_merge_with_overlong_message_fails(self):
        """Own merge commit of upstream main with 74-column body line fails pre-push check."""
        base, _, body_74 = self.setup_ops_sync_fixture()
        invalid_merge_msg = (
            f"Merge branch 'main' of upstream into main\n\n{body_74}\n\n"
            "Signed-off-by: Hook Test <hook@example.test>"
        )
        command(
            self.repo, "git", "merge", "--no-ff", "-q", "--no-verify",
            "-m", invalid_merge_msg, "upstream/main",
        )
        bad_merge_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with self.assertRaises(HookError):
                hooks.check_commit_message_range(base, bad_merge_sha, remote="origin")
        finally:
            os.chdir(original)

    def test_other_push_remotes_bound_exceeded_raises(self):
        """Verify other_push_remotes raises HookError when configured remotes exceed MAX_REMOTES."""
        self.assertTrue(hasattr(hooks, "MAX_REMOTES"))
        fake_remotes = b"\n".join(f"remote{i}".encode() for i in range(hooks.MAX_REMOTES + 10))
        with mock.patch("hooks.git", return_value=fake_remotes):
            with self.assertRaises(HookError) as ctx:
                hooks.other_push_remotes("origin")
            self.assertIn(str(hooks.MAX_REMOTES), str(ctx.exception))

    def test_other_remote_refs_bound_exceeded_raises(self):
        """Verify other_remote_refs raises HookError when tracking refs exceed bound."""
        self.assertTrue(hasattr(hooks, "MAX_COMMIT_MESSAGE_COMMITS"))
        fake_refs = b"\n".join(f"0123456789abcdef0123456789abcdef0123456{i:03d}".encode()
                               for i in range(hooks.MAX_COMMIT_MESSAGE_COMMITS + 10))
        with mock.patch("hooks.git", return_value=fake_refs):
            with self.assertRaises(HookError) as ctx:
                hooks.other_remote_refs(["origin"])
            self.assertIn(str(hooks.MAX_COMMIT_MESSAGE_COMMITS), str(ctx.exception))

    def test_other_push_remotes_url_target_excludes_matching_remote(self):
        """A push to a URL resolves remote.NAME.url/pushurl and drops the matching remote."""
        target_url = "git@github.com:example/repo.git"
        config_output = (
            f"remote.origin.url {target_url}\n"
            "remote.upstream.url https://github.com/upstream/repo.git\n"
        ).encode()

        def fake_git(*args, **kwargs):
            if args[0] == "remote":
                return b"origin\nupstream\n"
            if args[0] == "config":
                return config_output
            return b""

        with mock.patch("hooks.git", side_effect=fake_git):
            remotes = hooks.other_push_remotes(target_url)
        self.assertEqual(remotes, ["upstream"])

    def test_other_push_remotes_rejects_excess_url_entries(self):
        """A remote URL config with more entries than the bound is refused (HISS-02), not scanned."""
        excess = b"\n".join(
            f"remote.r{i}.url https://host/{i}.git".encode()
            for i in range(2 * hooks.MAX_REMOTES + 1)
        )

        def fake_git(*args, **kwargs):
            """Stub git: one remote, but an over-bound list of URL config entries."""
            if args[0] == "remote":
                return b"origin\n"
            if args[0] == "config":
                return excess
            return b""

        with mock.patch("hooks.git", side_effect=fake_git):
            with self.assertRaises(HookError) as ctx:
                hooks.other_push_remotes("origin")
        self.assertIn(str(2 * hooks.MAX_REMOTES), str(ctx.exception))

    def test_ref_deleted_after_enumeration_still_lets_check_run(self):
        """A ref deleted after enumeration passes object names so check runs without error."""
        base, upstream_sha, _ = self.setup_ops_sync_fixture()
        valid_merge_msg = (
            "Merge branch 'main' of upstream into main\n\n"
            "Signed-off-by: Hook Test <hook@example.test>"
        )
        command(
            self.repo, "git", "merge", "--no-ff", "-q", "--no-verify",
            "-m", valid_merge_msg, "upstream/main",
        )
        merge_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            refs = hooks.other_remote_refs(["upstream"])
            self.assertIn(upstream_sha, refs)
            self.assertFalse(any(r.startswith("refs/") for r in refs))

            command(self.repo, "git", "update-ref", "-d", "refs/remotes/upstream/main")
            with mock.patch("hooks.other_remote_refs", return_value=refs):
                hooks.check_commit_message_range(base, merge_sha, remote="origin")
        finally:
            os.chdir(original)

    def test_stored_commit_comment_line_and_scissors_with_bad_footer_fails(self):
        """A stored commit with comment line and scissors tail holding bad footer fails validation."""
        base, _, _ = self.setup_ops_sync_fixture()
        bad_stored_msg = (
            "feat: add feature\n\n"
            "# a comment line\n"
            "# ------------------------ >8 ------------------------\n"
            "BREAKING CHANGE: change without migration footer"
        )
        self.write("change.txt", "change\n")
        command(
            self.repo, "git", "commit", "-q", "-s", "--no-verify",
            "-m", bad_stored_msg,
        )
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with self.assertRaises(HookError):
                hooks.check_commit_message_range(base, head, remote="origin")
        finally:
            os.chdir(original)

    def test_check_commit_message_range_carries_one_exclude_per_tracking_ref(self):
        """check_commit_message_range invokes forge check-commits with one --exclude per tracking ref."""
        with mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", return_value=["1111111111111111111111111111111111111111",
                                                                     "2222222222222222222222222222222222222222"]), \
                mock.patch("hooks.run") as mock_run:
            hooks.check_commit_message_range("base_sha", "head_sha", remote="origin")
        self.assertTrue(mock_run.called)
        called_args = mock_run.call_args[0][0]
        self.assertIn("--exclude=1111111111111111111111111111111111111111", called_args)
        self.assertIn("--exclude=2222222222222222222222222222222222222222", called_args)

    def test_commit_message_range_single_remote_fixture_keeps_existing_behaviour(self):
        """A single-remote fixture retains today's behaviour: unskipped range checking."""
        origin = Path(self.temp.name) / "origin.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(origin))
        command(self.repo, "git", "remote", "add", "origin", str(origin))
        command(self.repo, "git", "push", "-q", "-u", "origin", "main")
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        self.write("valid.txt", "valid\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat: valid commit\n\nwrapped line")
        valid_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            hooks.check_commit_message_range(base, valid_sha, remote="origin")

            body_74 = ("word " * 14) + "abcd"
            self.write("invalid.txt", "invalid\n")
            command(
                self.repo, "git", "commit", "-q", "-s", "--no-verify",
                "-m", f"feat: invalid commit\n\n{body_74}",
            )
            invalid_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

            with self.assertRaises(HookError):
                hooks.check_commit_message_range(base, invalid_sha, remote="origin")
        finally:
            os.chdir(original)

    def test_ci_workflow_enforces_commit_message_policy_on_main_push(self):
        """ci.yml requires forge check-commits on push to main with BEFORE_SHA and --first-parent.

        The assertions read the workflow text directly, with no YAML parser, so the self-test interpreter
        needs no PyYAML and the policy check can neither be skipped nor reported as passing when absent.
        """
        ci_path = ROOT / ".github" / "workflows" / "ci.yml"
        self.assertTrue(ci_path.is_file(), f"{ci_path} must exist")
        text = ci_path.read_text(encoding="utf-8")
        # Slice the one named push-to-main step from its "- name:" to the next step header, so the fields
        # below are asserted against that step alone, not anywhere in the workflow.
        marker = "Enforce Commit Message Policy on Main Push"
        start = text.find(marker)
        self.assertNotEqual(start, -1, "ci.yml must contain the push-to-main policy step")
        step_start = text.rfind("- name:", 0, start)
        self.assertNotEqual(step_start, -1, "the policy step must be a named workflow step")
        next_step = text.find("\n      - name:", start)
        step = text[step_start: next_step if next_step != -1 else len(text)]
        for needle in (
            "github.event_name == 'push'",
            "refs/heads/main",
            "forge check-commits",
            "BEFORE_SHA: ${{ github.event.before }}",
            "HEAD_SHA: ${{ github.sha }}",
            '--base="$BEFORE_SHA"',
            '--head="$HEAD_SHA"',
            "--first-parent",
            "0000000000000000000000000000000000000000",
            "force push",
        ):
            self.assertIn(needle, step, f"push-to-main policy step must carry {needle!r}")

    def test_forge_check_commits_fails_on_74_column_body_commit(self):
        """A commit with a 74-column body line fails forge check-commits with non-zero exit."""
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        body_74 = ("word " * 14) + "abcd"
        self.write("change.txt", "change\n")
        command(
            self.repo, "git", "commit", "-q", "-s", "--no-verify",
            "-m", f"feat: wide body\n\n{body_74}",
        )
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        result = command(
            self.repo, cli_path(self.repo), "forge", "check-commits",
            f"--base={base}", f"--head={head}", ok=False,
        )
        self.assertNotEqual(result.returncode, 0)
        output = (result.stdout + result.stderr).decode(errors="replace")
        self.assertIn("74 columns; maximum 72", output)

    def test_signature_check_passes_all_signed_commits(self):
        """All G-status commits pass the signature check."""
        log_output = (
            "aaaa000000000000000000000000000000000000 G\n"
            "bbbb000000000000000000000000000000000000 G\n"
        )
        with mock.patch("hooks.run", side_effect=[b"true\n", log_output.encode()]) as run_cmd, \
                mock.patch("hooks.git", return_value=log_output.encode()) as git_cmd, \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            hooks.check_signature_range("base-sha", "head-sha")
        git_cmd.assert_called_once_with(
            "log", f"--max-count={hooks.MAX_SIGNATURE_COMMITS + 1}",
            "--format=%H %G?", "base-sha..head-sha", "--",
        )
        self.assertIn("2 commit(s)", out.getvalue())
        self.assertIn("verified", out.getvalue())

    def test_signature_check_refuses_unsigned_commit(self):
        """N-status commit raises HookError listing only the bad SHA."""
        log_output = (
            "aaaa000000000000000000000000000000000000 G\n"
            "cccc000000000000000000000000000000000000 N\n"
        )
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.git", return_value=log_output.encode()), \
                self.assertRaises(HookError) as ctx:
            hooks.check_signature_range("base-sha", "head-sha")
        self.assertIn("cccc00000000", str(ctx.exception))
        self.assertIn("no signature", str(ctx.exception))
        self.assertNotIn("aaaa00000000", str(ctx.exception))

    def test_signature_check_refuses_unverifiable_key(self):
        """U-status commit (unknown validity) is refused, not silently accepted."""
        log_output = "dddd000000000000000000000000000000000000 U\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.git", return_value=log_output.encode()), \
                self.assertRaises(HookError) as ctx:
            hooks.check_signature_range("base-sha", "head-sha")
        self.assertIn("dddd00000000", str(ctx.exception))
        self.assertIn("unknown validity", str(ctx.exception))

    def test_signature_check_skips_without_base(self):
        """None or empty base skips the check; git log is not called."""
        for base in (None, ""):
            with self.subTest(base=base), \
                    mock.patch("hooks.run", return_value=b"true\n"), \
                    mock.patch("hooks.git") as git_cmd, \
                    mock.patch("sys.stdout", new_callable=io.StringIO) as out:
                hooks.check_signature_range(base, "head-sha")
            git_cmd.assert_not_called()
            self.assertIn("Signature check: skipped, no pushed-range base", out.getvalue())

    def test_signature_check_skips_when_gpgsign_disabled(self):
        """commit.gpgsign=false skips the check entirely."""
        with mock.patch("hooks.run", return_value=b"false\n"), \
                mock.patch("hooks.git") as git_cmd, \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            hooks.check_signature_range("base-sha", "head-sha")
        git_cmd.assert_not_called()
        self.assertIn("Signature check: skipped, commit.gpgsign not set to true", out.getvalue())

    def test_signature_check_skips_when_gpgsign_unset(self):
        """Unset commit.gpgsign (empty output) skips the check."""
        with mock.patch("hooks.run", return_value=b""), \
                mock.patch("hooks.git") as git_cmd, \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            hooks.check_signature_range("base-sha", "head-sha")
        git_cmd.assert_not_called()
        self.assertIn("Signature check: skipped, commit.gpgsign not set to true", out.getvalue())

    def test_signature_check_refuses_range_exceeding_limit(self):
        """Range with more than MAX_SIGNATURE_COMMITS commits raises HookError."""
        lines = [f"{i:040x} G" for i in range(hooks.MAX_SIGNATURE_COMMITS + 1)]
        log_output = "\n".join(lines) + "\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.git", return_value=log_output.encode()), \
                self.assertRaises(HookError) as ctx:
            hooks.check_signature_range("base-sha", "head-sha")
        self.assertIn("exceeds", str(ctx.exception))
        self.assertIn(str(hooks.MAX_SIGNATURE_COMMITS), str(ctx.exception))

    def test_signature_check_accepts_range_at_limit(self):
        """Exactly MAX_SIGNATURE_COMMITS commits passes without overflow error."""
        lines = [f"{i:040x} G" for i in range(hooks.MAX_SIGNATURE_COMMITS)]
        log_output = "\n".join(lines) + "\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.git", return_value=log_output.encode()), \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            hooks.check_signature_range("base-sha", "head-sha")
        self.assertIn(f"{hooks.MAX_SIGNATURE_COMMITS} commit(s)", out.getvalue())

    def test_signature_check_refuses_malformed_log_line(self):
        """Unparseable git log line raises HookError instead of silent skip."""
        log_output = "aaaa000000000000000000000000000000000000 G\nmalformed-line\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.git", return_value=log_output.encode()), \
                self.assertRaises(HookError) as ctx:
            hooks.check_signature_range("base-sha", "head-sha")
        self.assertIn("unparseable", str(ctx.exception))
        self.assertIn("malformed-line", str(ctx.exception))

    def test_signature_range_skips_published_commits_in_two_remote_fixture(self):
        """In a two-remote fixture, unverifiable commits published on upstream are skipped while own commits fail."""
        origin = Path(self.temp.name) / "origin.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(origin))
        command(self.repo, "git", "remote", "add", "origin", str(origin))
        command(self.repo, "git", "push", "-q", "-u", "origin", "main")
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        upstream = Path(self.temp.name) / "upstream.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(upstream))
        command(self.repo, "git", "remote", "add", "upstream", str(upstream))

        # 1. Upstream commit without valid signature (published on upstream remote)
        self.write("upstream.txt", "upstream\n")
        command(
            self.repo, "git", "-c", "user.email=upstream@example.test",
            "commit", "-q", "-s", "--no-verify", "--no-gpg-sign",
            "-m", "feat: upstream commit without signature",
        )
        upstream_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "push", "-q", "-u", "upstream", "main")

        def fake_run(args, *r_args, **r_kwargs):
            """Report commit.gpgsign as true and pass every other command to the real run."""
            if args[:4] == ["git", "config", "--get", "--type=bool"] and args[4:] == ["commit.gpgsign"]:
                return b"true\n"
            return common.run(args, *r_args, **r_kwargs)

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with mock.patch("hooks.run", side_effect=fake_run):
                # A commit published on the other remote with an unverifiable signature is left out and passes
                with io.StringIO() as out, mock.patch("sys.stdout", out):
                    hooks.check_signature_range(base, upstream_sha, remote="origin")
                    output = out.getvalue()
                    self.assertIn("1 published commit(s) in", output)
                    self.assertIn(upstream_sha, output)

                # 2. Own unsigned commit on top of it still fails naming its sha
                self.write("own.txt", "own\n")
                command(
                    self.repo, "git", "commit", "-q", "-s", "--no-verify", "--no-gpg-sign",
                    "-m", "feat: own unsigned commit",
                )
                own_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
                command(self.repo, "git", "update-ref", "refs/remotes/upstream/own", own_sha)

                with self.assertRaises(HookError) as ctx:
                    hooks.check_signature_range(base, own_sha, remote="origin")
                self.assertIn(own_sha[:12], str(ctx.exception))
                self.assertNotIn(upstream_sha[:12], str(ctx.exception))
        finally:
            os.chdir(original)

    def test_signature_range_single_remote_fixture_refuses_unverifiable_commit(self):
        """A single-remote fixture retains today's refusal of an unverifiable commit."""
        origin = Path(self.temp.name) / "origin.git"
        command(self.repo, "git", "init", "--bare", "-q", "-b", "main", str(origin))
        command(self.repo, "git", "remote", "add", "origin", str(origin))
        command(self.repo, "git", "push", "-q", "-u", "origin", "main")
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        self.write("unverified.txt", "unverified\n")
        command(
            self.repo, "git", "commit", "-q", "-s", "--no-verify", "--no-gpg-sign",
            "-m", "feat: unverified commit",
        )
        unverified_sha = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

        def fake_run(args, *r_args, **r_kwargs):
            """Report commit.gpgsign as true and pass every other command to the real run."""
            if args[:4] == ["git", "config", "--get", "--type=bool"] and args[4:] == ["commit.gpgsign"]:
                return b"true\n"
            return common.run(args, *r_args, **r_kwargs)

        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with mock.patch("hooks.run", side_effect=fake_run):
                with self.assertRaises(HookError) as ctx:
                    hooks.check_signature_range(base, unverified_sha, remote="origin")
                self.assertIn(unverified_sha[:12], str(ctx.exception))
        finally:
            os.chdir(original)

    def test_signature_range_bound_applies_to_remaining_commits(self):
        """The range bound MAX_SIGNATURE_COMMITS still applies to the remaining commits."""
        lines = [f"{i:040x} G" for i in range(hooks.MAX_SIGNATURE_COMMITS + 1)]
        log_output = "\n".join(lines) + "\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", return_value=["upstream-sha"]), \
                mock.patch("hooks.git", side_effect=[
                    log_output.encode(),
                    b"",
                    b"600\n",
                ]), \
                self.assertRaises(HookError) as ctx:
            hooks.check_signature_range("base-sha", "head-sha", remote="origin")
        self.assertIn("exceeds", str(ctx.exception))
        self.assertIn(str(hooks.MAX_SIGNATURE_COMMITS), str(ctx.exception))

    def test_signature_check_excludes_commits_from_other_remotes(self):
        """When other remotes exist, their tracking refs are excluded from git log via --not."""
        log_output = "aaaa000000000000000000000000000000000000 G\n"
        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", return_value=["1111111111111111111111111111111111111111"]), \
                mock.patch("hooks.git", side_effect=[
                    log_output.encode(),
                    b"",
                    b"2\n",
                ]) as git_cmd, \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            hooks.check_signature_range("base-sha", "head-sha", remote="origin")
        git_cmd.assert_any_call(
            "log", f"--max-count={hooks.MAX_SIGNATURE_COMMITS + 1}",
            "--format=%H %G?", "base-sha..head-sha", "--not",
            "1111111111111111111111111111111111111111", "--",
        )
        self.assertIn("1 published commit(s) in", out.getvalue())
        self.assertIn("1111111111111111111111111111111111111111", out.getvalue())


    def test_real_checkpoint_push_builds_and_tests_without_release_receipt(self):
        skip_without_race_detector(self)
        self.write("go.mod", "module example.test/checkpoint\n\ngo 1.27\n")
        self.write("value.go", "package checkpoint\n\nfunc Value() int { return 1 }\n")
        self.write("value_test.go", 'package checkpoint\n\nimport "testing"\n\n'
                   'func TestValue(t *testing.T) {\n\tif Value() != 1 {\n\t\tt.Fatal("wrong value")\n\t}\n}\n')
        # Valid syntax, but no signing authority or release gate implementation.
        self.write(".standards.yaml", 'receipt:\n  public_key: ""\n')
        command(self.repo, "git", "commit", "-q", "-s", "-m", "feat: add checkpoint fixture")
        remote = Path(self.temp.name) / "remote.git"
        command(self.repo, "git", "init", "--bare", "-q", str(remote))
        command(self.repo, "git", "remote", "add", "origin", str(remote))
        result = command(self.repo, "git", "push", "origin", "HEAD:refs/heads/checkpoint/wip")
        output = result.stdout + result.stderr
        self.assertIn(b"Checkpoint Go scope: example.test/checkpoint", output)
        self.assertIn(b"ok  \texample.test/checkpoint", output)
        self.assertIn(b"WIP checkpoint:", output)
        self.assertIn(b"no release receipt issued", output)
        self.assertEqual(command(remote, "git", "rev-parse", "refs/heads/checkpoint/wip").stdout,
                         command(self.repo, "git", "rev-parse", "HEAD").stdout)
        self.assertFalse((self.repo / ".git/praetor-receipts").exists())
        self.assertFalse((self.repo / ".standards-receipt.json").exists())
        # Existing remote checkpoints cannot become trusted strict baselines.
        # Promotion must reach strict checks and reject the absent state-audit
        # bootstrap target before attempting the remaining release pipeline.
        for destination in ("refs/heads/review/wip", "refs/tags/checkpoint/wip"):
            rejected = command(self.repo, "git", "push", "origin", "HEAD:" + destination, ok=False)
            self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
            self.assertMissingMakeTarget(rejected, "state-audit")
            self.assertNotIn(b"WIP checkpoint:", rejected.stdout + rejected.stderr)
            self.assertEqual(command(self.repo, "git", "ls-remote", "origin", destination).stdout, b"")
        mixed_refs = ("refs/heads/checkpoint/mixed", "refs/heads/review/mixed")
        rejected = command(self.repo, "git", "push", "origin",
                           *("HEAD:" + ref for ref in mixed_refs), ok=False)
        self.assertNotEqual(rejected.returncode, 0, rejected.stdout + rejected.stderr)
        self.assertMissingMakeTarget(rejected, "state-audit")
        self.assertEqual(command(self.repo, "git", "ls-remote", "origin", *mixed_refs).stdout, b"")
        self.assertFalse((self.repo / ".git/praetor-receipts").exists())
        self.assertFalse((self.repo / ".standards-receipt.json").exists())

    def test_push_destination_controls_mode_and_mixed_refs_still_run_strict_checks(self):
        """Verify pre_push selects checkpoint or strict mode from the destination ref."""
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        self.write("README.md", "# Checkpoint routing\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: test destination policy")
        head = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        checkpoint = f"refs/heads/main {head} refs/heads/checkpoint/wip {base}\n"
        strict = f"refs/heads/checkpoint/local {head} refs/heads/main {base}\n"
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            # Only the destination determines policy; the local branch is irrelevant.
            with mock.patch("hooks.sys.stdin", io.StringIO(strict)), \
                    mock.patch("hooks.check_refactor_range"), \
                    mock.patch("hooks.check_docstring_range"), \
                    mock.patch("hooks.checkpoint_checks") as wip, \
                    mock.patch("hooks.source_checks", return_value=False) as full:
                pre_push("origin")
                full.assert_called_once()
                wip.assert_not_called()
            # An already-checked WIP commit cannot suppress the strict check for
            # that same commit/base, including after a duplicate checkpoint ref.
            for protocol in (checkpoint + strict, checkpoint + checkpoint + strict, strict + checkpoint):
                with self.subTest(protocol=protocol), \
                        mock.patch("hooks.sys.stdin", io.StringIO(protocol)), \
                        mock.patch("hooks.check_refactor_range"), \
                        mock.patch("hooks.check_docstring_range"), \
                        mock.patch("hooks.source_checks", side_effect=HookError("strict release rejected")) as full:
                    with self.assertRaisesRegex(HookError, "strict release rejected"):
                        pre_push("origin")
                    full.assert_called_once()
            # File checks are mandatory before either mode's source checks.
            with mock.patch("hooks.sys.stdin", io.StringIO(checkpoint)), \
                    mock.patch("hooks.check_refactor_range"), \
                    mock.patch("hooks.check_docstring_range"), \
                    mock.patch("hooks.file_checks", side_effect=HookError("invalid snapshot syntax")), \
                    mock.patch("hooks.checkpoint_checks") as wip:
                with self.assertRaisesRegex(HookError, "invalid snapshot syntax"):
                    pre_push("origin")
                wip.assert_not_called()
        finally:
            os.chdir(original)

    def test_real_merge_and_rewrite_stages(self):
        base = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "checkout", "-q", "-b", "topic")
        self.write("topic.md", "topic\n")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: add topic")
        tip = command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()
        command(self.repo, "git", "checkout", "-q", "main")
        command(self.repo, "git", "merge", "--no-ff", "--no-commit", "topic")
        command(self.repo, "git", "commit", "-s", "-m", "chore: merge topic")
        merged = self.hook("post-merge")
        self.assertEqual(merged.returncode, 0, merged.stdout + merged.stderr)
        # The fixture is no engine checkout: the real engine skips the refresh, silently.
        self.assertNotIn(b"engine install", merged.stdout + merged.stderr)
        rewritten = self.hook("post-rewrite", data=f"{base} {tip}\n".encode())
        self.assertEqual(rewritten.returncode, 0, rewritten.stdout + rewritten.stderr)
        self.assertNotEqual(self.hook("post-rewrite", data=b"bad\n").returncode, 0)


class SignaturePublished(unittest.TestCase):
    """Signature and ref regressions without installing hooks or changing process config."""

    @contextlib.contextmanager
    def signature_fixture(self, lines=b"", own=b"", email=b"local@example.test\n", total=2):
        """Stub Git's bounded range queries and capture diagnostics and exact arguments."""
        def fake_git(*args, **kwargs):
            """Answer the config, log and rev-list calls signature_log_lines makes."""
            if args[0] == "config":
                self.assertEqual(args, ("config", "--get", "user.email"))
                self.assertEqual(kwargs, {"allowed": (0, 1)})
                return email
            if args[0] == "log":
                return own if "--fixed-strings" in args else lines
            if args[0] == "rev-list":
                return f"{total}\n".encode()
            self.fail(f"unexpected git call: {args}")

        with mock.patch("hooks.run", return_value=b"true\n"), \
                mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", return_value=["published-sha"]), \
                mock.patch("hooks.git", side_effect=fake_git) as git_cmd, \
                contextlib.redirect_stdout(io.StringIO()) as out:
            yield out, git_cmd

    def test_signature_own_published_unsigned_commit_refused(self):
        """A published unsigned commit by the configured committer remains checked."""
        with self.signature_fixture(own=b"own-sha N\n"), \
                self.assertRaisesRegex(HookError, "own-sha.*no signature"):
            hooks.check_signature_range("base", "head", remote="origin")

    def test_signature_other_committer_published_unverifiable_commit_exempt(self):
        """An upstream commit absent from both queries is excluded exactly once."""
        with self.signature_fixture(total=1) as (out, git_cmd):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertIn("1 published commit(s)", out.getvalue())
        self.assertIn("0 commit(s)", out.getvalue())
        git_cmd.assert_any_call(
            "log", f"--max-count={hooks.MAX_SIGNATURE_COMMITS + 1}",
            "--fixed-strings", "--committer=<local@example.test>",
            "--format=%H %G?", "base..head", "--",
        )

    def test_signature_unset_email_preserves_exemption_and_reports_identity(self):
        """Without user.email the old exemption remains visible as an identity limitation."""
        with self.signature_fixture(email=b"", total=1) as (out, git_cmd):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertEqual(out.getvalue().count("own published commits left out with upstream ones"), 1)
        self.assertIn("1 published commit(s)", out.getvalue())
        self.assertEqual(sum(call.args[0] == "log" for call in git_cmd.call_args_list), 1)

    def test_signature_merge_deduplicates_sha_and_preserves_order(self):
        """Own commits already checked do not inflate the range or disturb log order."""
        with self.signature_fixture(lines=b"first G\nshared G\n", own=b"shared G\nlast G\n"):
            self.assertEqual(hooks.signature_log_lines("base", "head", "origin"),
                             ["first G", "shared G", "last G"])

    def test_signature_large_total_reports_at_least_true_exclusions(self):
        """A capped total never presents unchecked commits as an exact exclusion count."""
        with self.signature_fixture(lines=b"checked G\n", total=hooks.MAX_SIGNATURE_COMMITS + 1) as (out, _):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertIn(f"at least {hooks.MAX_SIGNATURE_COMMITS} published commit(s)", out.getvalue())

    def test_signature_total_at_cap_reports_exact_exclusions(self):
        """A complete count exactly at the cap still gives an exact exclusion count."""
        with self.signature_fixture(lines=b"checked G\n", total=hooks.MAX_SIGNATURE_COMMITS) as (out, _):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertIn(f"{hooks.MAX_SIGNATURE_COMMITS - 1} published commit(s)", out.getvalue())
        self.assertNotIn("at least", out.getvalue())

    def test_signature_filtered_over_cap_prints_no_exclusion_count(self):
        """An incomplete filtered log must fail the cap without claiming exclusions."""
        lines = "\n".join(f"{i:040x} G" for i in range(hooks.MAX_SIGNATURE_COMMITS + 1)).encode()
        with self.signature_fixture(lines=lines, total=hooks.MAX_SIGNATURE_COMMITS + 1) as (out, _), \
                self.assertRaisesRegex(HookError, f"exceeds {hooks.MAX_SIGNATURE_COMMITS} commits"):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertNotIn("published commit(s)", out.getvalue())

    def test_signature_merged_own_commits_enforce_cap(self):
        """The union, rather than either individual log, is bounded by the cap."""
        lines = "\n".join(f"{i:040x} G" for i in range(hooks.MAX_SIGNATURE_COMMITS)).encode()
        with self.signature_fixture(lines=lines, own=b"own N\n") as (out, _), \
                self.assertRaisesRegex(HookError, f"exceeds {hooks.MAX_SIGNATURE_COMMITS} commits"):
            hooks.check_signature_range("base", "head", remote="origin")
        self.assertNotIn("published commit(s)", out.getvalue())

    def test_signature_count_argv_is_bounded(self):
        """The count has its own max-count bound even when filtering leaves one commit."""
        with self.signature_fixture(lines=b"checked G\n") as (_, git_cmd):
            hooks.check_signature_range("base", "head", remote="origin")
        git_cmd.assert_any_call("rev-list", "--count", f"--max-count={hooks.MAX_SIGNATURE_COMMITS + 1}",
                                "base..head", "--")

    def test_stable_remote_refs_unchanged_runs_action_once(self):
        """The unchanged boundary returns the action's result after one comparison."""
        action = mock.Mock(return_value="result")
        with mock.patch("hooks.other_remote_refs", return_value=["same"]) as refs:
            self.assertEqual(hooks.with_stable_remote_refs(["upstream"], action), "result")
        action.assert_called_once_with(["same"])
        self.assertEqual(refs.call_args_list, [mock.call(["upstream"])] * 2)

    def test_stable_remote_refs_changes_once_then_retries(self):
        """A concurrent fetch discards the stale result and checks the new snapshot."""
        action = mock.Mock(side_effect=["stale", "stable"])
        with mock.patch("hooks.other_remote_refs", side_effect=[["old"], ["new"], ["new"], ["new"]]):
            self.assertEqual(hooks.with_stable_remote_refs(["upstream"], action), "stable")
        self.assertEqual(action.call_args_list, [mock.call(["old"]), mock.call(["new"])])

    def test_stable_remote_refs_continuous_fetch_fails_after_attempt_bound(self):
        """Continuous changes stop after exactly MAX_REF_SNAPSHOT_ATTEMPTS actions."""
        attempts = hooks.MAX_REF_SNAPSHOT_ATTEMPTS
        action = mock.Mock()
        snapshots = [[str(i)] for i in range(2 * attempts)]
        with mock.patch("hooks.other_remote_refs", side_effect=snapshots) as refs, \
                self.assertRaisesRegex(HookError, "concurrent fetch.*retry.*push"):
            hooks.with_stable_remote_refs(["upstream"], action)
        self.assertEqual(action.call_count, attempts)
        self.assertEqual(refs.call_count, 2 * attempts)

    def test_signature_ref_snapshot_retry_checks_newly_published_set(self):
        """A fetch during signature logging causes the signature query to run again."""
        with self.signature_fixture(email=b"", total=1) as (out, git_cmd), \
                mock.patch("hooks.other_remote_refs", side_effect=[["old"], ["new"], ["new"], ["new"]]):
            hooks.check_signature_range("base", "head", remote="origin")
        logs = [call.args for call in git_cmd.call_args_list if call.args[0] == "log"]
        self.assertEqual(len(logs), 2)
        self.assertIn("old", logs[0])
        self.assertIn("new", logs[1])
        self.assertEqual(out.getvalue().count("published commit(s)"), 1)

    def test_commit_message_ref_snapshot_retry_updates_exclusions(self):
        """Commit message checking uses the same retry with the latest object names."""
        with mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", side_effect=[["old"], ["new"], ["new"], ["new"]]), \
                mock.patch("hooks.praetorctl_path", return_value="praetorctl"), \
                mock.patch("hooks.run") as run_cmd:
            hooks.check_commit_message_range("base", "head", remote="origin")
        self.assertEqual(run_cmd.call_args_list, [
            mock.call(["praetorctl", "forge", "check-commits", "--base=base", "--head=head",
                       f"--exclude={ref}"], capture=False, timeout=180)
            for ref in ("old", "new")
        ])

    def test_commit_message_ref_snapshot_retry_discards_stale_failure(self):
        """A newly published commit may resolve the failure from the previous snapshot."""
        with mock.patch("hooks.other_push_remotes", return_value=["upstream"]), \
                mock.patch("hooks.other_remote_refs", side_effect=[["old"], ["new"], ["new"], ["new"]]), \
                mock.patch("hooks.praetorctl_path", return_value="praetorctl"), \
                mock.patch("hooks.run", side_effect=[HookError("stale failure"), b""]) as run_cmd:
            hooks.check_commit_message_range("base", "head", remote="origin")
        self.assertEqual(run_cmd.call_count, 2)

    def test_stable_remote_refs_unchanged_failure_propagates(self):
        """A failure against stable refs is preserved without unnecessary retries."""
        action = mock.Mock(side_effect=HookError("policy refused"))
        with mock.patch("hooks.other_remote_refs", return_value=["same"]), \
                self.assertRaisesRegex(HookError, "policy refused"):
            hooks.with_stable_remote_refs(["upstream"], action)
        action.assert_called_once_with(["same"])

    def test_signature_range_published_committer_identity_in_two_remote_fixture(self):
        """Real Git excludes a longer email but retains a published local committer."""
        with tempfile.TemporaryDirectory(prefix="signature-published-") as temp:
            repo = Path(temp)

            def fixture_git(*args, **kwargs):
                """Run only local Git plumbing, without hooks or process config changes."""
                return common.git(*args, cwd=repo, **kwargs)

            fixture_git("init", "-q", "-b", "main")
            fixture_git("config", "user.name", "Signature Test")
            fixture_git("config", "user.email", "local@example.test")
            fixture_git("remote", "add", "origin", str(repo / "origin.git"))
            fixture_git("remote", "add", "upstream", str(repo / "upstream.git"))
            tree = common.run(["git", "mktree"], cwd=repo, data=b"").decode().strip()
            base = fixture_git("commit-tree", tree, "--no-gpg-sign", "-m", "base").decode().strip()
            upstream = fixture_git("-c", "user.email=longerlocal@example.test", "commit-tree", tree,
                                   "-p", base, "--no-gpg-sign", "-m", "upstream").decode().strip()
            own = fixture_git("commit-tree", tree, "-p", upstream,
                              "--no-gpg-sign", "-m", "own").decode().strip()
            fixture_git("update-ref", "refs/remotes/upstream/main", own)
            with mock.patch("hooks.git", side_effect=fixture_git), \
                    mock.patch("hooks.run", return_value=b"true\n"), \
                    contextlib.redirect_stdout(io.StringIO()) as out:
                hooks.check_signature_range(base, upstream, remote="origin")
                self.assertIn("1 published commit(s)", out.getvalue())
                with self.assertRaisesRegex(HookError, f"{own[:12]}.*no signature") as error:
                    hooks.check_signature_range(base, own, remote="origin")
                self.assertNotIn(upstream[:12], str(error.exception))
                out.truncate(0)
                out.seek(0)
                with mock.patch("hooks.MAX_SIGNATURE_COMMITS", 1), \
                        self.assertRaisesRegex(HookError, f"{own[:12]}.*no signature"):
                    hooks.check_signature_range(base, own, remote="origin")
                self.assertIn("at least 1 published commit(s)", out.getvalue())


class SnapshotRefs(unittest.TestCase):
    """Commit exports restore remote refs in one bounded transaction."""

    def test_malformed_remote_ref_names_the_line_and_snapshot_bound(self):
        """Missing and extra fields raise a contextual HookError before a transaction."""
        for line in ("onlyone", "a b c", ""):
            with self.subTest(line=line):
                with self.assertRaises(HookError) as refused:
                    common._snapshot_ref_updates((line + "\n").encode())
                message = str(refused.exception)
                self.assertIn(repr(line), message)
                self.assertIn("origin", message)
                self.assertIn(f"{common.MAX_SNAPSHOT_REFS} remote-tracking refs", message)

    def test_malformed_remote_ref_diagnostic_truncates_the_line(self):
        """A long malformed ref contributes at most its first 200 characters."""
        line = "x" * 200 + "SECRET_SUFFIX"
        with self.assertRaises(HookError) as refused:
            common._snapshot_ref_updates((line + "\n").encode())
        self.assertIn(repr(line[:200]), str(refused.exception))
        self.assertNotIn("SECRET_SUFFIX", str(refused.exception))

    def test_well_formed_remote_refs_render_the_transaction(self):
        """Each two-field ref preserves its name, object ID and input order."""
        refs = b"a refs/remotes/origin/main\nb refs/remotes/origin/topic\n"
        self.assertEqual(
            common._snapshot_ref_updates(refs),
            b"update refs/remotes/origin/main a\nupdate refs/remotes/origin/topic b\n",
        )

    def test_remote_ref_transaction_accepts_exactly_the_real_bound(self):
        """Exactly MAX_SNAPSHOT_REFS lines render without truncation."""
        refs = b"".join(
            f"a refs/remotes/origin/branch-{n}\n".encode()
            for n in range(common.MAX_SNAPSHOT_REFS)
        )
        expected = b"".join(
            f"update refs/remotes/origin/branch-{n} a\n".encode()
            for n in range(common.MAX_SNAPSHOT_REFS)
        )
        self.assertEqual(common._snapshot_ref_updates(refs), expected)

    def test_remote_refs_are_batched_at_the_limit_and_empty_is_skipped(self):
        """Zero refs skip updates; the exact limit uses one stdin update."""
        for count in (0, 2):
            refs = b"".join(
                f"{'a' * 40} refs/remotes/origin/branch-{n}\n".encode()
                for n in range(count)
            )
            with (
                self.subTest(count=count),
                mock.patch.object(common, "MAX_SNAPSHOT_REFS", 2, create=True),
                mock.patch("common.git", side_effect=[b"/source\n", refs]),
                mock.patch("common.run", return_value=b"") as execute,
            ):
                common._export_commit(Path("/snapshot"), "HEAD", {})
                updates = [
                    call
                    for call in execute.call_args_list
                    if call.args[0][:2] == ["git", "update-ref"]
                ]
                self.assertEqual(len(updates), int(count > 0))
                if count:
                    self.assertEqual(
                        updates[0].args[0], ["git", "update-ref", "--stdin"]
                    )
                    expected = refs.replace(b"a" * 40 + b" ", b"update ")
                    expected = b"".join(
                        line + b" " + b"a" * 40 + b"\n"
                        for line in expected.splitlines()[:2]
                    )
                    self.assertEqual(updates[0].kwargs["data"], expected)

    def test_remote_refs_over_the_limit_fail_before_cloning(self):
        """One ref beyond the bound refuses the export before any mutation."""
        refs = b"".join(
            f"{'a' * 40} refs/remotes/origin/branch-{n}\n".encode()
            for n in range(3)
        )
        with (
            mock.patch.object(common, "MAX_SNAPSHOT_REFS", 2, create=True),
            mock.patch("common.git", side_effect=[b"/source\n", refs]),
            mock.patch("common.run", return_value=b"") as execute,
            self.assertRaisesRegex(HookError, "2 remote-tracking refs"),
        ):
            common._export_commit(Path("/snapshot"), "HEAD", {})
        self.assertEqual(execute.call_count, 1)

    def test_repository_identity_ignores_inherited_git_dir(self):
        """Inherited repository selection cannot redirect checkpoint identity reads."""
        with tempfile.TemporaryDirectory() as directory:
            root, other = (Path(directory) / name for name in ("root", "other"))
            for repo in (root, other):
                run(["git", "init", "--quiet", str(repo)], env=clean_env())
            budget = common.HookBudget(10)
            with mock.patch.dict(os.environ, {"GIT_DIR": str(other / ".git")}):
                for reader in (common._git_common_dir, common._listed_worktrees):
                    with self.subTest(reader=reader.__name__):
                        result = reader(root, budget)
                        if reader == common._git_common_dir:
                            self.assertEqual(result, (root / ".git").resolve())
                        else:
                            self.assertEqual(
                                [Path(entry["path"]).resolve() for entry in result],
                                [root.resolve()],
                            )


class SnapshotSweep(unittest.TestCase):
    """A snapshot directory a killed hook left behind (0.6-0.7 GB each, 2026-10-19) is removed by the
    next snapshot once it is older than SNAPSHOT_STALE_S; fresh ones and other names stay."""

    def test_stale_snapshot_removed_fresh_and_foreign_kept(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            stale, fresh, other = root / "praetor-hook-old", root / "praetor-hook-new", root / "other-old"
            for directory in (stale, fresh, other):
                (directory / "objects").mkdir(parents=True)
                (directory / "objects" / "pack").write_bytes(b"x")
            (stale / "objects").chmod(0o555)  # read-only like a packed object store
            now = time.time()
            for directory in (stale, other):
                os.utime(directory, (now - common.SNAPSHOT_STALE_S - 60,) * 2)
            removed = common.sweep_stale_snapshots(root, now)
            self.assertEqual(removed, 1)
            self.assertFalse(stale.exists(), "a stale snapshot must be removed, read-only parts included")
            self.assertTrue(fresh.exists() and other.exists(), "a fresh snapshot and another name stay")

    def test_sweep_of_an_empty_root_removes_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertEqual(common.sweep_stale_snapshots(tmp), 0)


class ScopeAndGuard(unittest.TestCase):
    def test_command_closes_inherited_stdin_and_preserves_payload(self):
        completed = subprocess.CompletedProcess([], 0, b"", b"")
        with mock.patch("subprocess.run", return_value=completed) as spawned:
            command(ROOT, "probe")
            self.assertEqual(spawned.call_args.kwargs["input"], b"")
            command(ROOT, "probe", data=b"payload")
            self.assertEqual(spawned.call_args.kwargs["input"], b"payload")

    def test_clean_env_preserves_ordinary_values_and_strips_git_command_config(self):
        inherited = {"PATH": os.environ.get("PATH", ""), "KEEP": "yes",
                     "GIT_CONFIG_COUNT": "2",
                     "GIT_CONFIG_KEY_0": "remote.origin.pushurl",
                     "GIT_CONFIG_VALUE_0": "ssh://example.invalid/wrong",
                     "GIT_CONFIG_KEY_1": "user.password",
                     "GIT_CONFIG_VALUE_1": "must-not-reach-children",
                     "GIT_CONFIG_KEY_2048": "remote.other.pushurl",
                     "GIT_CONFIG_VALUE_2048": "ssh://example.invalid/also-wrong",
                     "GIT_CONFIG_PARAMETERS": "'remote.origin.pushurl'='wrong'"}
        with mock.patch.dict(os.environ, inherited, clear=True):
            cleaned = clean_env()
        self.assertEqual(cleaned["KEEP"], "yes")
        self.assertEqual(cleaned["CI"], "true")
        self.assertEqual(cleaned["GOWORK"], "off")
        self.assertEqual(cleaned["GOFLAGS"], "-mod=readonly")
        self.assertFalse(any(key == "GIT_CONFIG_COUNT" or key == "GIT_CONFIG_PARAMETERS"
                             or key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"))
                             for key in cleaned), cleaned)

    def test_clean_env_keeps_fixture_pushes_inside_their_own_remote(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            repo = root / "repo"
            intended = root / "intended.git"
            sentinel = root / "sentinel.git"
            repo.mkdir()
            command(repo, "git", "init", "-q", "-b", "main")
            command(repo, "git", "config", "user.name", "Hook Test")
            command(repo, "git", "config", "user.email", "hook@example.test")
            (repo / "README.md").write_text("# Isolated\n", encoding="utf-8")
            command(repo, "git", "add", "README.md")
            command(repo, "git", "commit", "-q", "-m", "test: seed isolated remote")
            command(root, "git", "init", "--bare", "-q", str(intended))
            command(root, "git", "init", "--bare", "-q", str(sentinel))
            command(repo, "git", "remote", "add", "origin", str(intended))
            injected = dict(os.environ, GIT_CONFIG_COUNT="1",
                            GIT_CONFIG_KEY_0="remote.origin.pushurl",
                            GIT_CONFIG_VALUE_0=str(sentinel))
            with mock.patch.dict(os.environ, injected, clear=True):
                cleaned = clean_env()
            pushed = subprocess.run(["git", "push", "-q", "origin", "main"], cwd=repo,
                                    env=cleaned, capture_output=True, timeout=30, check=False)
            self.assertEqual(pushed.returncode, 0, pushed.stdout + pushed.stderr)
            self.assertTrue(command(intended, "git", "show-ref", "--verify",
                                    "refs/heads/main").stdout)
            self.assertEqual(command(sentinel, "git", "for-each-ref").stdout, b"")

    def test_full_gate_uses_the_clean_git_environment(self):
        injected = dict(os.environ, GIT_CONFIG_COUNT="1",
                        GIT_CONFIG_KEY_0="remote.origin.pushurl",
                        GIT_CONFIG_VALUE_0="ssh://example.invalid/wrong")
        with mock.patch.dict(os.environ, injected, clear=True), \
                mock.patch("checks.run", return_value=b'{"timeout_seconds": 480}\n') as process:
            self.assertTrue(run_full_gate(Path(".")))
        self.assertEqual(process.call_count, 3)
        for call in process.call_args_list:
            child_env = call.kwargs["env"]
            self.assertFalse(any(key == "GIT_CONFIG_COUNT"
                                 or key.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"))
                                 for key in child_env), child_env)

    def test_full_gate_timeout_follows_the_gate_run_deadline(self):
        # Positive: a raised stage bound reaches the hook through the gate's own resolution, so
        # the subprocess outlives the gate's deadline instead of cutting it at a fixed 600 s.
        query = ["go", "run", "./cmd/standardsctl", "gate", "deadline", "--json"]
        def process(argv, **kwargs):
            return b'{"timeout_seconds": 2100, "timeout": "35m0s"}\n' if argv == query else b""
        with mock.patch("checks.run", side_effect=process) as spawned:
            self.assertTrue(run_full_gate(Path(".")))
        calls = [(call.args[0][3:5], call.kwargs["timeout"]) for call in spawned.call_args_list]
        self.assertEqual(calls, [(["gate", "deadline"], GATE_QUERY_TIMEOUT),
                                 (["gate", "run"], 2100 + GATE_LAUNCH_MARGIN),
                                 (["gate", "verify"], 2100 + GATE_LAUNCH_MARGIN)])

    def test_full_gate_refuses_an_unusable_deadline(self):
        # Negative: without a usable deadline the gate does not run under a guessed bound.
        for report in (b"", b"not json", b"[]", b"{}", b'{"timeout_seconds": 0}',
                       b'{"timeout_seconds": -5}', b'{"timeout_seconds": "480"}',
                       b'{"timeout_seconds": true}', b'{"timeout_seconds": 1.5}'):
            with self.subTest(report=report), mock.patch("checks.run", return_value=report) as spawned:
                with self.assertRaisesRegex(HookError, "gate deadline"):
                    run_full_gate(Path("."))
                self.assertEqual(spawned.call_count, 1)

    def test_gate_timeout_adds_only_the_launch_margin(self):
        # Boundary: the smallest deadline, and the default 1200 s bound plus the 300 s allowance,
        # which gets the launch margin and nothing else.
        self.assertEqual(gate_timeout(b'{"timeout_seconds": 1}'), 1 + GATE_LAUNCH_MARGIN)
        self.assertEqual(gate_timeout(b'{"timeout_seconds": 1500}'), 1500 + GATE_LAUNCH_MARGIN)

    def test_clean_env_final_bound_is_composable_for_nested_children(self):
        """Clean environment entry bound is idempotent and nested child environments compose."""
        unmanaged = MAX_PROCESS_ENV_ENTRIES - len(MANAGED_PROCESS_ENV)
        exact = {f"SAFE_{index}": "x" for index in range(unmanaged)}
        with mock.patch.dict(os.environ, exact, clear=True):
            parent = clean_env()
        self.assertEqual(len(parent), MAX_PROCESS_ENV_ENTRIES)
        self.assertEqual(parent["SAFE_0"], "x")
        with mock.patch.dict(os.environ, parent, clear=True):
            child = clean_env()
        self.assertEqual(child, parent)

        excessive = dict(exact, ONE_TOO_MANY="x")
        with (
            mock.patch.dict(os.environ, excessive, clear=True),
            self.assertRaisesRegex(HookError, "environment exceeds"),
        ):
            clean_env()

    def semgrep_tree(self, root, *files):
        """Create both rule configs and a bounded set of source paths for command tests."""
        rules = root / ".config" / "semgrep" / "hiss-invariants.yml"
        rules.parent.mkdir(parents=True)
        rules.write_text("rules: []\n", encoding="utf-8")
        parity = ROOT / ".config/semgrep/go-permissions.yml"
        (rules.parent / parity.name).write_text(
            parity.read_text(encoding="utf-8"), encoding="utf-8",
        )
        self.assertLessEqual(len(files), 32, "fixture path bound")
        for name in files[:32]:
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("x\n", encoding="utf-8")
        return ".config/semgrep/hiss-invariants.yml"

    def test_semgrep_permissions_command_uses_baseline_and_keeps_hiss_whole_file(self):
        """A changed Go test receives a separate parity scan with the caller's base."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, "internal/a_test.go")
            commands = semgrep_commands(root, ["internal/a_test.go"], base="abc123")
        self.assertEqual(commands, [
            ["semgrep", "scan", "--error", "--config", rules, "internal/a_test.go"],
            ["semgrep", "scan", "--error", "--config", ".config/semgrep/go-permissions.yml",
             "--baseline-commit", "abc123", "internal/a_test.go"],
        ])

    def test_semgrep_permissions_requires_a_trusted_baseline(self):
        """Missing history must refuse a scan rather than silently ignore added violations."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.semgrep_tree(root, "internal/a_test.go")
            with self.assertRaisesRegex(HookError, "permission.*baseline"):
                semgrep_commands(root, ["internal/a_test.go"])

    def test_semgrep_permissions_source_checks_forwards_existing_base(self):
        """The push's already computed base reaches command construction unchanged."""
        with mock.patch("checks.tool_module_check"), \
                mock.patch("checks.go_packages", return_value=[]), \
                mock.patch("checks.governance_commands", return_value=[]), \
                mock.patch("checks.parallel"), \
                mock.patch("checks.semgrep_commands", return_value=[]) as commands:
            source_checks(ROOT, ["internal/a_test.go"], base="abc123")
        commands.assert_called_once_with(ROOT, ["internal/a_test.go"], base="abc123")

    def test_semgrep_permissions_rule_only_change_validates_configuration(self):
        """A push changing only the permission rules validates the configuration without Go files."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root)
            permissions = ".config/semgrep/go-permissions.yml"
            commands = semgrep_commands(root, [permissions])
        self.assertEqual(commands, [
            ["semgrep", "scan", "--error", "--config", rules, "--exclude", FIXTURE_DIRECTORY, "."],
            ["semgrep", "validate", permissions],
        ])

    def test_semgrep_permissions_absent_rules_file_is_the_opt_out(self):
        """A repository without the permission rules file (adoption does not emit it) gets no permission scan."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "internal").mkdir()
            (root / "internal" / "a.go").write_text("package a\n", encoding="utf-8")
            commands = semgrep_commands(root, ["internal/a.go"], base="abc123")
            self.assertFalse(any(".config/semgrep/go-permissions.yml" in command for command in commands))

    def test_semgrep_permissions_rule_file_removed_fails(self):
        """A push that removes the permission rules file is refused, not read as an opt-out."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            permissions = ".config/semgrep/go-permissions.yml"
            with self.assertRaisesRegex(HookError, "Go permission rules file removed"):
                semgrep_commands(root, [permissions])

    def test_file_checks_schedules_self_tests_on_semgrep_policy_change(self):
        """A change to .config/semgrep/ schedules harness self-tests to replay rule fixtures."""
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            cfg = root / ".config" / "semgrep"
            cfg.mkdir(parents=True)
            permissions = ".config/semgrep/go-permissions.yml"
            (root / permissions).write_text("rules: []\n", encoding="utf-8")
            with mock.patch("checks.parallel") as scheduled, \
                    mock.patch("checks.require_floors"), \
                    mock.patch("checks.self_test_commands", return_value=[["self_test_stub"]]) as self_tests:
                file_checks(root, [permissions])
            self_tests.assert_called_once_with(root)
            scheduled.assert_called_once()
            commands = scheduled.call_args.args[0]
            self.assertIn(["self_test_stub"], commands)

    def test_semgrep_permissions_rule_validation_planted_invalid_config_fails(self):
        """Execute validation and verify that planted invalid rule syntax fails."""
        if shutil.which("semgrep") is None:
            self.skipTest("semgrep absent")
        permissions = ".config/semgrep/go-permissions.yml"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cfg = root / ".config" / "semgrep"
            cfg.mkdir(parents=True)
            # Positive: real permissions config passes validation
            real_text = (ROOT / permissions).read_text(encoding="utf-8")
            (cfg / "go-permissions.yml").write_text(real_text, encoding="utf-8")
            commands = semgrep_commands(root, [permissions])
            self.assertEqual(commands, [["semgrep", "validate", permissions]])
            res = subprocess.run(commands[0], cwd=root, capture_output=True, text=True, timeout=30)
            self.assertEqual(res.returncode, 0, res.stderr)
            # Planted negative case: invalid rule structure fails semgrep validate
            (cfg / "go-permissions.yml").write_text(
                "rules:\n  - id: planted_invalid\n    bad_field: true\n", encoding="utf-8"
            )
            res_bad = subprocess.run(commands[0], cwd=root, capture_output=True, text=True, timeout=30)
            self.assertNotEqual(res_bad.returncode, 0)

    def semgrep_fixture_git(self, root, *args):
        """Use bounded local Git plumbing; fixture commits never invoke repository hooks."""
        result = subprocess.run(["git", *args], cwd=root, capture_output=True, text=True,
                                timeout=30, check=False)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def semgrep_permission_id(self, finding):
        """Recognize original ids with or without Semgrep's local-config path prefix."""
        rules = ("go.lang.correctness.permissions.file_permission.incorrect-default-permission",
                 "go_file-permissions_rule-mkdir", "go_file-permissions_rule-fileperm")
        check_id = finding["check_id"]
        return next((rule for rule in rules[:3]
                     if check_id == rule or check_id.endswith("." + rule)), check_id)

    def semgrep_permission_scan(self, source, baseline="package fixture\n"):
        """Run the hook's real parity command against a local committed baseline."""
        if shutil.which("semgrep") is None:
            self.skipTest("semgrep absent; real permission matching requires the pinned engine")
        with tempfile.TemporaryDirectory(prefix="praetor-permissions-") as directory:
            root = Path(directory)
            name = "fixture_test.go"
            self.semgrep_tree(root, name)
            fixture = root / name
            fixture.write_text(baseline, encoding="utf-8")
            self.semgrep_fixture_git(root, "init", "--quiet")
            self.semgrep_fixture_git(root, "add", ".")
            tree = self.semgrep_fixture_git(root, "write-tree")
            commit = self.semgrep_fixture_git(
                root, "-c", "user.name=Permission Fixture", "-c", "user.email=fixture@example.invalid",
                "-c", "commit.gpgsign=false", "commit-tree", tree, "-m", "baseline")
            self.semgrep_fixture_git(root, "update-ref", "HEAD", commit)
            fixture.write_text(source, encoding="utf-8")
            self.semgrep_fixture_git(root, "add", name)
            scans = semgrep_commands(root, [name], base="HEAD")
            self.assertEqual(len(scans), 2, "the hook must construct a permission scan")
            env = {**os.environ, "XDG_CONFIG_HOME": str(root)}
            result = subprocess.run([*scans[1], "--json", "--metrics=off", "--disable-version-check"],
                                    cwd=root, env=env, capture_output=True, text=True, timeout=60, check=False)
        self.assertIn(result.returncode, (0, 1), result.stderr)
        self.assertTrue(result.stdout.strip(), f"semgrep returned no JSON: {result.stderr}")
        payload = json.loads(result.stdout)
        self.assertEqual(payload.get("errors", []), [], payload)
        return result.returncode, payload.get("results", [])

    def test_semgrep_permissions_added_writefile_fails(self):
        """Plant the executable fixture mode that previously reached Codacy."""
        source = 'package fixture\nimport "os"\nfunc added() { os.WriteFile("stub", nil, 0o755) }\n'
        status, findings = self.semgrep_permission_scan(source)
        self.assertEqual(status, 1)
        self.assertEqual({self.semgrep_permission_id(item) for item in findings}, {
            "go.lang.correctness.permissions.file_permission.incorrect-default-permission",
            "go_file-permissions_rule-fileperm",
        })

    def test_semgrep_permissions_named_suppression_passes(self):
        """A justified line above the call suppresses both matching permission ids."""
        source = ('package fixture\nimport "os"\nfunc added() {\n'
                  '// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, '
                  'go_file-permissions_rule-fileperm -- fixture must execute\n'
                  'os.WriteFile("stub", nil, 0o755)\n}\n')
        self.assertEqual(self.semgrep_permission_scan(source), (0, []))

    def test_semgrep_permissions_baseline_ignores_unchanged_violation(self):
        """An unrelated addition in a touched file does not adopt its historical violation."""
        baseline = 'package fixture\nimport "os"\nfunc old() { os.WriteFile("stub", nil, 0o755) }\n'
        self.assertEqual(self.semgrep_permission_scan(baseline + "func added() {}\n", baseline), (0, []))

    def test_semgrep_permissions_thresholds_and_literal_call_forms(self):
        """Exercise each mode argument, numeric spelling, threshold and nonliteral exclusion."""
        calls = ('os.WriteFile("p", nil, MODE)', 'os.OpenFile("p", os.O_CREATE, MODE)',
                 'os.Chmod("p", MODE)', 'os.Mkdir("p", MODE)', 'os.MkdirAll("p", MODE)')
        modes = ("0o600", "0o601", "0o700", "0o750", "0o755", "0600", "0755",
                 "384", "385", "0x180", "0x181", "0b110000000", "0b110000001", "0o6_01", "mode")
        source = ['package fixture', 'import "os"', 'func added(mode os.FileMode) {']
        expected = {}
        for call in calls[:5]:
            for mode in modes[:15]:
                source.append(call.replace("MODE", mode))
                value = (int(mode, 8) if mode.startswith("0") and mode.isdecimal()
                         else int(mode, 0) if mode != "mode" else 0)
                ids = set()
                if value > 0o600:
                    ids.add("go.lang.correctness.permissions.file_permission.incorrect-default-permission")
                    if "Mkdir" not in call:
                        ids.add("go_file-permissions_rule-fileperm")
                if "Mkdir" in call and value > 0o750:
                    ids.add("go_file-permissions_rule-mkdir")
                expected[len(source)] = ids
        source.append("}")
        status, findings = self.semgrep_permission_scan("\n".join(source) + "\n")
        self.assertEqual(status, 1)
        actual = {}
        self.assertLessEqual(len(findings), 225, "fixture finding bound")
        for item in findings[:225]:
            actual.setdefault(item["start"]["line"], set()).add(self.semgrep_permission_id(item))
        self.assertEqual({line: ids for line, ids in expected.items() if ids}, actual)

    def test_semgrep_rule_change_scans_the_tree_without_the_corpus(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, "internal/a.go", ".config/hiss/testdata/HISS-08/c/positive/gets.c")
            commands = semgrep_commands(root, [rules, "internal/a.go"], base="abc123")
        self.assertEqual(commands, [["semgrep", "scan", "--error", "--config", rules,
                                     "--exclude", FIXTURE_DIRECTORY, "."],
                                   ["semgrep", "scan", "--error", "--config",
                                    ".config/semgrep/go-permissions.yml", "--baseline-commit",
                                    "abc123", "internal/a.go"]])

    def test_semgrep_file_scan_names_source_and_never_a_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, "internal/a.go", "internal/testdata/bad.go", "testdata/bad.py")
            mixed = semgrep_commands(root, ["internal/a.go", "internal/testdata/bad.go", "testdata/bad.py"],
                                     base="abc123")
            only_fixtures = semgrep_commands(root, ["internal/testdata/bad.go", "testdata/bad.py"])
        self.assertEqual(mixed, [["semgrep", "scan", "--error", "--config", rules, "internal/a.go"],
                                ["semgrep", "scan", "--error", "--config",
                                 ".config/semgrep/go-permissions.yml", "--baseline-commit",
                                 "abc123", "internal/a.go"]])
        self.assertEqual(only_fixtures, [])

    def test_semgrep_boundaries_missing_rules_and_lookalike_names(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "internal").mkdir()
            (root / "internal" / "a.py").write_text("x = 1\n", encoding="utf-8")
            self.assertEqual(semgrep_commands(root, [".config/semgrep/hiss-invariants.yml", "internal/a.py"]), [])
        self.assertTrue(is_fixture("testdata/a.go"))
        self.assertTrue(is_fixture("a\\testdata\\b.go"))
        self.assertFalse(is_fixture("internal/testdatafile.go"))
        self.assertFalse(is_fixture("mytestdata/a.go"))

    def test_gofmt_check_batches_cover_every_file(self):
        """Batched gofmt runs cover every file and report formatting errors in any batch."""
        # Boundary: gofmt runs in GOFMT_BATCH-sized argument lists, so a whole-tree check
        # stays under the Windows command-line limit; a file in the last batch is still named.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            names = [f"f{index}.go" for index in range(3)]
            for name in names[:-1]:
                (root / name).write_text("package p\n", encoding="utf-8", newline="\n")
            (root / names[-1]).write_text("package p\nfunc  f(){}\n", encoding="utf-8", newline="\n")
            with (
                mock.patch("checks.GOFMT_BATCH", 1),
                mock.patch("checks.run", wraps=run) as runner,
                self.assertRaises(HookError) as raised,
            ):
                gofmt_check(root, names)
            self.assertEqual(runner.call_count, len(names))
            self.assertIn(names[-1], str(raised.exception))
            self.assertNotIn(names[0], str(raised.exception))
            # Positive: every batch clean raises nothing.
            (root / names[-1]).write_text("package p\n", encoding="utf-8", newline="\n")
            with mock.patch("checks.GOFMT_BATCH", 1):
                gofmt_check(root, names)

    def test_semgrep_scans_every_suffix_its_rule_languages_cover(self):
        # Positive: headers, C++ and JSX/TSX sources reach semgrep; the c/cpp and
        # javascript/typescript rules target them, and the old suffix set dropped them.
        names = ["native/a.h", "native/b.hpp", "native/c.cc", "native/d.hh", "web/e.tsx",
                 "web/f.jsx", "web/g.mjs", "tools/h.pyi"]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, *names, "docs/guide.md", "notes.txt")
            commands = semgrep_commands(root, [*names, "docs/guide.md"])
            # Negative: a documentation-only change starts no semgrep scan.
            docs_only = semgrep_commands(root, ["docs/guide.md", "notes.txt"])
        self.assertEqual(commands, [["semgrep", "scan", "--error", "--config", rules, *names]])
        self.assertEqual(docs_only, [])
        # Boundary: suffixes are case-sensitive, as they are to semgrep: .C is C++, .S is not
        # a semgrep language.
        self.assertIn(".C", SEMGREP_SUFFIXES)
        self.assertNotIn(".S", SEMGREP_SUFFIXES)

    def test_semgrep_suffixes_cover_every_rule_language(self):
        # Every language the shipped rules target has its extensions in the hook's table, so a
        # rule added for a new language cannot silently skip that language's files.
        text = (ROOT / ".config/semgrep/hiss-invariants.yml").read_text(encoding="utf-8")
        declared = re.findall(r"(?m)^\s*languages:\s*(.*)$", text)
        self.assertTrue(declared)
        languages = set()
        for value in declared:
            flow = re.fullmatch(r"\[([^\]]*)\]\s*", value)
            self.assertIsNotNone(flow, f"languages must be a flow list: {value!r}")
            languages.update(item.strip() for item in flow.group(1).split(","))
        self.assertEqual(languages - set(SEMGREP_LANGUAGE_EXTENSIONS), set())

    def test_context_changed_covers_every_compile_context_path(self):
        # Positive: every file compile-context reads or writes -- AGENTS.md, .agents/ personas,
        # skills and plugin copies, the six vendor files, the vendor persona directories and the
        # client skill directories a register skill is copied to (.claude/skills, #235) --
        # triggers compile-context --verify. The real command produces the list, so a target
        # it gains fails here instead of going unverified by the pre-commit hook.
        with tempfile.TemporaryDirectory(prefix="praetor-context-") as temp:
            root = Path(temp)
            skill = "---\nname: demo\ndescription: demo\n---\n\nRun tests.\n"
            persona = "---\nname: demo\ndescription: demo\nmodel: flash\n---\n\nRun tests.\n"  # personas carry a tier
            for name, text in {"AGENTS.md": "# Demo\n\nRun tests before commit.\n",
                               ".agents/agents/demo.md": persona,
                               ".agents/skills/demo/SKILL.md": skill,
                               ".agents/skills/caveman/SKILL.md": skill,
                               ".agents/plugins/praetor/plugin.json": '{"name": "praetor"}\n'}.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text, encoding="utf-8")
            run(["go", "run", "./cmd/standardsctl", "compile-context", "--source",
                 str(root / "AGENTS.md"), "--target-dir", str(root)],
                cwd=ROOT, env=clean_env(), timeout=600)
            written = sorted(path.relative_to(root).as_posix() for path in root.rglob("*") if path.is_file())
        for vendor in (".claude/agents/demo.md", ".codex/agents/demo.md", ".gemini/agents/demo.md",
                       ".github/agents/demo.md", ".agents/plugins/praetor/skills/demo/SKILL.md",
                       ".claude/skills/caveman/SKILL.md"):
            self.assertIn(vendor, written)
        self.assertEqual([name for name in written if not context_changed([name])], [])

    def _compile_context_below_planted_git(self, plant):
        # Plants a .git entry above a temporary tree with plant(parent), then compiles the child.
        with tempfile.TemporaryDirectory(prefix="praetor-context-planted-") as temp:
            parent = Path(temp)
            plant(parent)
            root = parent / "child"
            skill = "---\nname: demo\ndescription: demo\n---\n\nRun tests.\n"
            persona = "---\nname: demo\ndescription: demo\nmodel: flash\n---\n\nRun tests.\n"  # personas carry a tier
            for name, text in {"AGENTS.md": "# Demo\n\nRun tests before commit.\n",
                               ".agents/agents/demo.md": persona,
                               ".agents/skills/demo/SKILL.md": skill,
                               ".agents/skills/caveman/SKILL.md": skill,
                               ".agents/plugins/praetor/plugin.json": '{"name": "praetor"}\n'}.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text, encoding="utf-8")
            run(["go", "run", "./cmd/standardsctl", "compile-context", "--source",
                 str(root / "AGENTS.md"), "--target-dir", str(root)],
                cwd=ROOT, env=clean_env(), timeout=600)
            written = sorted(path.relative_to(root).as_posix() for path in root.rglob("*") if path.is_file())
            self.assertIn(".claude/agents/demo.md", written)

    def test_compile_context_succeeds_with_planted_empty_git_above_temp_dir(self):
        # A planted empty .git above a temporary tree must not count as a repository (#1100).
        # Previously, GitWorktreePresent treated any .git entry as a worktree root, causing
        # compile-context to invoke git ignore and ls-files against an invalid repository.
        self._compile_context_below_planted_git(lambda parent: (parent / ".git").mkdir())

    @unittest.skipIf(os.name == "nt", "creating symlinks requires optional Windows privileges")
    def test_compile_context_succeeds_with_dangling_git_symlink_above_temp_dir(self):
        # Git skips a dangling .git symlink and keeps walking up, so it is no repository (#1100).
        self._compile_context_below_planted_git(
            lambda parent: (parent / ".git").symlink_to(parent / "nowhere"))

    def test_compile_context_succeeds_with_planted_head_only_git_above_temp_dir(self):
        # git requires objects/ and refs/ and walks past a .git holding only HEAD (#1100).
        def plant(parent):
            (parent / ".git").mkdir()
            (parent / ".git" / "HEAD").write_text("ref: refs/heads/main\n", encoding="utf-8")
        self._compile_context_below_planted_git(plant)

    def test_context_changed_ignores_paths_compile_context_does_not_touch(self):
        # Negative and boundary: documentation, vendor settings beside the persona directories,
        # workflows and lookalike prefixes do not start compile-context --verify.
        for name in ("README.md", "docs/guides/onboarding.md", ".claude/settings.json",
                     ".codex/config.toml", ".gemini/settings.json", ".github/workflows/ci.yml",
                     ".agentsrc", "docs/.agents/x.md", ".claude/agents.md", ".claude/skills.md"):
            with self.subTest(name=name):
                self.assertFalse(context_changed([name]))
        self.assertFalse(context_changed([]))

    def test_go_packages_selects_cgo_and_assembler_inputs(self):
        # Positive: every suffix go/build compiles into a package selects that package, not
        # only .go, .c, .h, .cc and .cpp.
        with tempfile.TemporaryDirectory(prefix="praetor-cgo-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/cgoscope\n\ngo 1.27\n")
            (root / "native").mkdir()
            (root / "native/native.go").write_text("package native\n")
            for name in ("native/b.hpp", "native/c.hh", "native/d.cxx", "native/e.S", "native/f.sx",
                         "native/g.m", "native/h.swig", "native/i.f90"):
                with self.subTest(name=name):
                    self.assertEqual(go_packages(root, [name]), ["example.test/cgoscope/native"])
            # Negative: a documentation file beside the package selects nothing.
            self.assertEqual(go_packages(root, ["native/README.md"]), [])

    def test_go_packages_rejects_a_package_outside_the_snapshot(self):
        # Boundary: a package `go list` reports outside the snapshot (a symlinked checkout that
        # resolves elsewhere) is a HookError naming it, as in local_package_patterns, not an
        # uncaught ValueError traceback.
        with tempfile.TemporaryDirectory(prefix="praetor-outside-") as temp, \
                tempfile.TemporaryDirectory(prefix="praetor-elsewhere-") as elsewhere:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/outside\n\ngo 1.27\n")
            listing = json.dumps({"Dir": elsewhere, "ImportPath": "example.test/outside"}).encode()
            with mock.patch("checks.run", return_value=listing), \
                    self.assertRaisesRegex(HookError, "outside the checked snapshot"):
                go_packages(root, ["a.go"])

    def chart_tree(self, root):
        """A chart, an ordinary templates/ directory and a lookalike file, as one tree.

        Both the predicate test and the wiring test read this, so the two never
        disagree about what a chart looks like.
        """
        chart = root / "deploy" / "helm" / "praetor"
        (chart / "templates" / "rbac").mkdir(parents=True)
        (chart / "Chart.yaml").write_text("name: praetor\n", encoding="utf-8")
        (chart / "values.yaml").write_text("replicaCount: 1\n", encoding="utf-8")
        (chart / "templates" / "service.yaml").write_text("{{- if true }}\n", encoding="utf-8")
        (chart / "templates" / "rbac" / "role.yaml").write_text("{{- if true }}\n", encoding="utf-8")
        (root / "deploy" / "helm" / "templates.yaml").write_text("a: b\n", encoding="utf-8")
        (root / "templates").mkdir()
        (root / "templates" / "plain.yaml").write_text("a: b\n", encoding="utf-8")

    def linted_yaml(self, root, names):
        """The YAML files file_checks actually hands yamllint for a staged set.

        The gate is the wiring, not the predicate: this calls file_checks itself and
        reads the scheduled argv, so dropping the exclusion from the comprehension
        fails here rather than passing unnoticed.
        """
        # The version probe is ToolFloors' subject; here only the scheduled argv is read, so
        # the case holds on a host without yamllint.
        with mock.patch("checks.parallel") as scheduled, mock.patch("checks.require_floors"):
            file_checks(root, names)
        self.assertEqual(scheduled.call_count, 1)
        commands, directory = scheduled.call_args.args
        self.assertEqual(directory, root)
        argv = [command for command in commands if command[0] == "yamllint"]
        if not argv:
            return []
        self.assertEqual(len(argv), 1)
        return [item for item in argv[0] if item.endswith((".yml", ".yaml"))]

    def test_staged_yaml_gate_lints_the_chart_documents_and_no_chart_template(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.chart_tree(root)
            templates = ["deploy/helm/praetor/templates/service.yaml",
                         "deploy/helm/praetor/templates/rbac/role.yaml"]
            documents = ["deploy/helm/praetor/Chart.yaml",
                         "deploy/helm/praetor/values.yaml",
                         "deploy/helm/templates.yaml",
                         "templates/plain.yaml"]
            # Positive and negative in one staged set: the documents reach yamllint
            # and the templates, at either depth, do not.
            self.assertEqual(self.linted_yaml(root, documents + templates), documents)
            # Boundary: nothing lintable staged means no yamllint command at all.
            self.assertEqual(self.linted_yaml(root, templates), [])

    def test_chart_templates_are_not_linted_as_yaml_documents(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.chart_tree(root)
            # A template beside Chart.yaml is a Go template: excluded.
            self.assertTrue(is_chart_template(root, "deploy/helm/praetor/templates/service.yaml"))
            self.assertTrue(is_chart_template(root, "deploy\\helm\\praetor\\templates\\service.yaml"))
            # Helm renders templates/ recursively, so a nested template is one too.
            self.assertTrue(is_chart_template(root, "deploy/helm/praetor/templates/rbac/role.yaml"))
            self.assertTrue(is_chart_template(root, "deploy\\helm\\praetor\\templates\\rbac\\role.yaml"))
            self.assertTrue(is_chart_template(root, "deploy/helm/praetor/templates/a/b/c/deep.yaml"))
            # The chart's own documents and a templates/ directory with no chart stay in scope.
            self.assertFalse(is_chart_template(root, "deploy/helm/praetor/values.yaml"))
            self.assertFalse(is_chart_template(root, "deploy/helm/praetor/Chart.yaml"))
            self.assertFalse(is_chart_template(root, "templates/plain.yaml"))
            self.assertFalse(is_chart_template(root, "templates/nested/plain.yaml"))
            self.assertFalse(is_chart_template(root, "deploy/helm/templates.yaml"))

    def init_governance_repo(self, root):
        command(root, "git", "init", "-q")
        command(root, "git", "config", "user.name", "Hook Test")
        command(root, "git", "config", "user.email", "hook@example.test")
        command(root, "git", "add", ".")
        command(root, "git", "commit", "-q", "-m", "chore: initialize audit fixture")

    def test_new_branch_ignores_unrelated_tracking_ref(self):
        with mock.patch("hooks.git", return_value=b"refs/remotes/origin/a unrelated\nrefs/remotes/origin/b related\n"), \
                mock.patch("hooks.run", side_effect=[b"", b"ancestor\n"]) as process:
            self.assertEqual(new_branch_base("head", "origin"), "ancestor")
            self.assertEqual(process.call_count, 2)

    def test_new_strict_branch_ignores_checkpoint_and_symbolic_head_baselines(self):
        refs = (b"refs/remotes/origin/HEAD checkpoint refs/remotes/origin/checkpoint/wip\n"
                b"refs/remotes/origin/alias checkpoint refs/remotes/origin/checkpoint/wip\n"
                b"refs/remotes/origin/checkpoint/wip checkpoint\n"
                b"refs/remotes/origin/main strict\n")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin"), "base")
            self.assertEqual(process.call_args.args[0][-1], "strict")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin", include_checkpoints=True), "base")
            self.assertEqual(process.call_args.args[0][-1], "checkpoint")

    def test_new_branch_prefers_named_default_and_preserves_ancestry_failures(self):
        refs = (b"refs/remotes/origin/HEAD current refs/remotes/origin/trunk\n"
                b"refs/remotes/origin/main old\nrefs/remotes/origin/trunk current\n")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", return_value=b"base\n") as process:
            self.assertEqual(new_branch_base("head", "origin"), "base")
            self.assertEqual(process.call_args.args[0][-1], "current")
        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", side_effect=HookError("missing ancestry")) as process:
            with self.assertRaisesRegex(HookError, "missing ancestry"):
                new_branch_base("head", "origin")
            self.assertEqual(process.call_count, 1)

    def test_new_branch_prefers_the_remote_default_over_a_stale_local_symref(self):
        """The local HEAD symref is not the source of truth for the scan scope.

        Git sets refs/remotes/<remote>/HEAD once at clone time and never updates it. In a real
        checkout it pointed at a feature branch, the base resolved six merges stale, and the
        staged scan grew from 3 files to 36 -- enough for semgrep-core to exhaust the host
        memlock limit and refuse the push with an allocation error naming nothing relevant.
        """
        refs = (b"refs/remotes/origin/HEAD stale refs/remotes/origin/feature\n"
                b"refs/remotes/origin/main current\n"
                b"refs/remotes/origin/feature stale\n")
        calls = []

        def fake_run(cmd, **kwargs):
            calls.append(cmd)
            if "ls-remote" in cmd:
                return b"ref: refs/heads/main\tHEAD\nabc123\tHEAD\n"
            return b"base\n"

        with mock.patch("hooks.git", return_value=refs), mock.patch("hooks.run", side_effect=fake_run):
            self.assertEqual(new_branch_base("head", "origin"), "base")
        merge_bases = [cmd for cmd in calls if cmd[:2] == ["git", "merge-base"]]
        self.assertTrue(merge_bases, "a merge-base must be attempted")
        self.assertEqual(merge_bases[0][-1], "current",
                         "the base must come from the remote's default branch, not the stale symref")

    def test_new_branch_falls_back_to_the_local_symref_when_the_remote_is_unreachable(self):
        """Offline must not fail the push, but the fallback has to be visible."""
        refs = (b"refs/remotes/origin/HEAD local refs/remotes/origin/trunk\n"
                b"refs/remotes/origin/trunk local\n")

        def fake_run(cmd, **kwargs):
            if "ls-remote" in cmd:
                return b""
            return b"base\n"

        with mock.patch("hooks.git", return_value=refs), \
                mock.patch("hooks.run", side_effect=fake_run), \
                mock.patch("sys.stdout", new_callable=io.StringIO) as out:
            self.assertEqual(new_branch_base("head", "origin"), "base")
        self.assertIn("remote default unavailable", out.getvalue())

    def test_remote_default_ref_reads_only_a_bounded_listing(self):
        """A remote answer is bounded input, and a malformed one yields no default at all."""
        # The bound has to be exercised, not merely present: put the only usable answer past it
        # and require that it is not found. A flood whose first line already matches would pass
        # with or without the limit and prove nothing.
        padding = b"\n".join(b"%040d\trefs/heads/pad%d" % (i, i) for i in range(hooks.MAX_LS_REMOTE_LINES + 40))
        buried = padding + b"\nref: refs/heads/main\tHEAD\n"
        with mock.patch("hooks.run", return_value=buried):
            self.assertIsNone(hooks.remote_default_ref("origin"),
                              "a default past the listing bound must not be read")

        within = b"ref: refs/heads/main\tHEAD\n" + padding
        with mock.patch("hooks.run", return_value=within):
            self.assertEqual(hooks.remote_default_ref("origin"), "refs/remotes/origin/main")
        for malformed in (b"", b"garbage\n", b"ref: refs/heads/main\tNOTHEAD\n"):
            with mock.patch("hooks.run", return_value=malformed):
                self.assertIsNone(hooks.remote_default_ref("origin"),
                                  f"malformed listing {malformed!r} must yield no default")

    def test_push_protocol_boundaries(self):
        self.assertEqual(push_updates(""), [])
        zero = "0" * 40
        self.assertEqual(push_updates(f"(delete) {zero} refs/heads/a {'a' * 40}"), [])
        self.assertEqual(len(push_updates(f"refs/heads/a {'b' * 40} refs/heads/a {zero}")), 1)
        with self.assertRaises(HookError):
            push_updates("bad")

    def test_checkpoint_destination_boundaries(self):
        for destination in ("refs/heads/checkpoint/wip", "refs/heads/checkpoint/a/b"):
            self.assertEqual(push_check_mode(destination), "checkpoint")
        for destination in ("refs/heads/main", "refs/heads/lts/1", "refs/heads/audit/wip",
                            "refs/tags/checkpoint/wip", "refs/checkpoint/wip", "checkpoint/wip",
                            "refs/heads/checkpoint", "refs/heads/checkpoint/", ""):
            with self.subTest(destination=destination):
                self.assertEqual(push_check_mode(destination), "strict")

    def test_guard_command_and_json_without_changing_live_environment(self):
        for cmd in ("git status", "git commit -s -m 'fix: valid'"):
            payload = {"hook_event_name": "PreToolUse", "tool_name": "Bash",
                       "tool_input": {"command": cmd, "workdir": str(ROOT)}}
            result = self.guard_input(json.dumps(payload).encode())
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"PRAETOR_COMMAND_POLICY_OK\n")
        # Denied commands are JSON data for the guard; they are never executed.
        for cmd in ("git commit --no-verify", "git commit -n", "LEFTHOOK=0 git commit"):
            result = self.guard_input(json.dumps({"tool_input": {"command": cmd}}).encode())
            self.assertEqual(result.returncode, 1)
            self.assertNotIn(b"PRAETOR_COMMAND_POLICY_OK", result.stdout)

    def guard_input(self, data):
        return subprocess.run([sys.executable, str(GUARD)], input=data, capture_output=True,
                              timeout=10, check=False)

    def test_guard_rejects_missing_or_wrong_json_command(self):
        payloads = [None, [], "git status", {}, {"tool_input": None},
                    {"tool_input": []}, {"tool_input": "git status"}, {"tool_input": {}}]
        payloads.extend({"tool_input": {"command": value}}
                        for value in (None, False, 1, [], {}, "", " \t\r\n"))
        for payload in payloads:
            with self.subTest(payload=payload):
                result = self.guard_input(json.dumps(payload).encode())
                self.assertEqual(result.returncode, 1)
                self.assertIn(b"Invalid hook input", result.stderr)

    def test_guard_rejects_malformed_and_oversized_json(self):
        maximum = 1 << 20
        valid = json.dumps({"tool_input": {"command": "git status"}}).encode()
        boundary = valid + b" " * (maximum - len(valid))
        result = self.guard_input(boundary)
        self.assertEqual(result.returncode, 0, result.stderr)
        for data in (b"", b"not json", b'{"tool_input":', b"\xff",
                     b"[" * 2000 + b"]" * 2000, boundary + b" "):
            with self.subTest(size=len(data), prefix=data[:20]):
                result = self.guard_input(data)
                self.assertEqual(result.returncode, 1)
                self.assertIn(b"Invalid hook input", result.stderr)
                self.assertNotIn(b"Traceback", result.stderr)

    def guard_command(self, command):
        payload = json.dumps({"tool_input": {"command": command}}).encode()
        if os.name != "nt":
            before = os.times()
            result = self.guard_input(payload)
            after = os.times()
            duration = (after.children_user - before.children_user) + (
                after.children_system - before.children_system
            )
            return result, duration
        started = time.monotonic()
        result = self.guard_input(payload)
        return result, time.monotonic() - started

    def test_guard_blocks_the_dev_root_and_names_no_organisation_folder(self):
        """Organisation folders are operator data (hooks.command_policy.deny), not guard rules."""
        cases = {
            "praetorctl adopt ~/dev": False,
            "standardsctl needs scan /home/u/dev/ --json": False,
            "PRAETORCTL Bootstrap ~/DEV": False,
            "praetorctl adopt ~/dev/acme": True,
            "praetorctl adopt ~/dev/scratch": True,
            "standardsctl needs scan /home/u/dev/acme-labs --json": True,
            # A Windows dev root ends in a backslash or in nothing (#200).
            "praetorctl adopt C:\\Users\\u\\dev\\": False,
            "praetorctl adopt C:\\Users\\u\\dev": False,
            "standardsctl needs scan C:/Users/u/dev\\ --json": False,
            "praetorctl adopt C:\\Users\\u\\dev\\acme": True,
            "praetorctl adopt C:\\Users\\u\\dev\\\\": True,
        }
        for command, allowed in cases.items():
            with self.subTest(command):
                result, _ = self.guard_command(command)
                self.assertEqual(result.returncode == 0, allowed, result.stderr)

    def guard_module(self):
        spec = importlib.util.spec_from_file_location("tested_block_evasion", GUARD)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def guard_scan_bounds(self):
        module = self.guard_module()
        return module.MAX_SCAN_CHARS, module.MAX_SCAN_LINE_CHARS

    def test_guard_refuses_commands_over_the_scan_bound_without_scanning(self):
        """re backtracks: one 16 KiB find..hooks line held the find rule for 16 s.

        That outlives a client's hook timeout, and a client that lets a timed-out hook
        through turns the stall into an evasion. The guard refuses such a command at once.
        """
        characters, per_line = self.guard_scan_bounds()
        full = ("x" * (per_line - 1) + "\n") * (characters // per_line)
        cases = {
            "pathological find line": ("find .git/hooks " * 1024, False),
            "line at the bound": ("x" * per_line, True),
            "line over the bound": ("x" * (per_line + 1), False),
            "command at the bound": (full, True),
            "command over the bound": (full + "x", False),
        }
        for name, (command, allowed) in cases.items():
            with self.subTest(name):
                result, elapsed = self.guard_command(command)
                self.assertEqual(result.returncode == 0, allowed, result.stderr)
                self.assertLess(elapsed, GUARD_TIMING_BOUND)
                if not allowed:
                    self.assertIn(b"exceeds scan bound", result.stderr)

    def test_guard_answers_the_slowest_admitted_commands_inside_the_bound(self):
        """The costliest shapes the bound admits, every line at the limit, still pass in time."""
        characters, per_line = self.guard_scan_bounds()
        for unit in ("find .git/hooks ", "sed -i ", " -git", " --a=git", " -C /git", "git -C\r-C\r"):
            with self.subTest(unit=unit):
                line = (unit * per_line)[:per_line - 1] + "\n"
                result, elapsed = self.guard_command(line * (characters // per_line))
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertLess(elapsed, GUARD_TIMING_BOUND)

    def test_guard_privilege_prefilter_skips_regex_without_a_literal(self):
        """Guard prefilter avoids regex execution when privilege literals are absent."""
        guard = self.guard_module()
        pattern = guard.BLOCKED_PATTERNS[-1]
        with mock.patch.object(guard.re, "search", side_effect=AssertionError("regex ran")):
            self.assertFalse(guard.search(pattern, "x" * guard.MAX_SCAN_CHARS))
        for name in ("sudo", "doas", "pkexec", "run0", "su"):
            with (
                self.subTest(name=name),
                mock.patch.object(guard.re, "search", return_value=True) as search,
            ):
                self.assertTrue(guard.search(pattern, "echo " + name))
                search.assert_called_once_with(pattern, "echo " + name)
        with mock.patch.object(guard.re, "search", return_value=True) as search:
            self.assertTrue(guard.search("custom", "x"))
            search.assert_called_once_with("custom", "x")

    def test_guard_search_answers_like_re_search(self):
        """search() answers re.search for every rule, the option-run rule walked once (#1283)."""
        guard = self.guard_module()
        tokens = ["git", " ", "\t", "\n", "\r", "-", "-C", "-c", "-Cx", "-n", "-an", "-mn",
                  "-3n", "--x", "--no-pager", "=", "/", "'", '"', "x", "commit", "am", " -git",
                  "dir/git", "'a git b'"]
        options = [" -C dir", " -C 'a git b'", ' -C "x"', " -c a=b", " --no-pager", " -git",
                   " --a=git", " -C /git", " -Cx", "\t-p", " -C", " -c", " -C 'x git commit -n'",
                   ' -C "d git am -3n"']
        tails = [" -n", " -an", " -mn", " -3n", " -m x", " --x", " -v", " -sn", " x"]
        rng = random.Random(1283)
        option_run_rule = guard.BLOCKED_PATTERNS[1]
        matched = 0

        def shaped():
            # A git call with an option chain before commit or am, then flags: the rule's own
            # shape, with a random token dropped in at any place.
            parts = [rng.choice(["", "x ", "cd d && ", "-"]), "git"]
            parts += [rng.choice(options) for _ in range(rng.randint(0, 8))]
            parts += [rng.choice([" ", "\n", "\t", " \r"]), rng.choice(["commit", "am", "status"])]
            parts += [rng.choice(tails) for _ in range(rng.randint(0, 4))]
            parts.insert(rng.randint(0, len(parts)), rng.choice(tokens))
            return "".join(parts)

        # A git inside a quoted argument starts in the middle of a run, not at a boundary of it:
        # only that git matches here, so search must not skip it.
        edges = {"git -C 'x git commit -n'": True, 'git -C "d git am -3n" status': True,
                 "git -git commit -n": True, "git --a=git commit -an": True,
                 "git -C dir/git commit -n": True, "git -C 'x git status' commit -m n": False,
                 "git -git -git": False, "x-git commit -n": True}
        for text, expected in edges.items():
            self.assertEqual(re.search(option_run_rule, text) is not None, expected, text)
            self.assertEqual(guard.search(option_run_rule, text), expected, text)
        for case in range(20000):
            if case % 2:
                text = shaped()
            else:
                text = "".join(rng.choice(tokens) for _ in range(rng.randint(1, 30)))
            patterns = guard.BLOCKED_PATTERNS if case < 2000 else [option_run_rule]
            for pattern in patterns:
                expected = re.search(pattern, text) is not None
                self.assertEqual(guard.search(pattern, text), expected, (pattern, text))
                matched += expected and pattern == option_run_rule
        self.assertGreater(matched, 2000, "the comparison must hold matching texts too")

    def test_guard_option_run_costs_about_what_a_benign_command_costs(self):
        """A git word in every option of a chain cost 30 benign commands before #1283."""
        guard = self.guard_module()
        characters, per_line = guard.MAX_SCAN_CHARS, guard.MAX_SCAN_LINE_CHARS

        def cost(unit):
            line = (unit * per_line)[:per_line - 1] + "\n"
            command = line * (characters // per_line)
            readings = []
            for _ in range(3):
                started = time.perf_counter()
                self.assertTrue(guard.audit_command(command))
                readings.append(time.perf_counter() - started)
            return min(readings)

        benign = cost("x")
        for unit in (" -git", " --a=git", " -C /git"):
            with self.subTest(unit=unit):
                self.assertLess(cost(unit), 10 * benign)

    def test_guard_preserves_argv_and_environment_entry_points(self):
        for args in (("git", "status"), ("--environment",)):
            result = subprocess.run([sys.executable, str(GUARD), *args], input=b"",
                                    capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, b"")

    def load_codex_adapter(self):
        path = GUARD.with_name("codex_pre_tool.py")
        spec = importlib.util.spec_from_file_location("tested_codex_pre_tool", path)
        module = importlib.util.module_from_spec(spec)
        with mock.patch.object(sys, "path", [str(GUARD.parent), *sys.path]):
            spec.loader.exec_module(module)
        return module

    def test_codex_adapter_fails_closed_when_guard_is_unavailable(self):
        adapter = self.load_codex_adapter()
        payload = b'{"tool_input":{"command":"git status"}}'
        for failure in (FileNotFoundError("missing fixture executable"),
                        subprocess.TimeoutExpired([sys.executable, "block_evasion.py"], 10)):
            with self.subTest(failure=type(failure).__name__), \
                    mock.patch.object(adapter.subprocess, "run", side_effect=failure), \
                    contextlib.redirect_stderr(io.StringIO()) as diagnostic:
                self.assertEqual(adapter.check(payload), 2)
                self.assertIn("unavailable", diagnostic.getvalue())

    def test_codex_adapter_rejects_success_without_guard_marker(self):
        adapter = self.load_codex_adapter()
        payload = b'{"tool_input":{"command":"git status"}}'
        def skipped_guard(*_args, **kwargs):
            kwargs["stdout"].write(b"guard process completed without checking policy\n")
            return subprocess.CompletedProcess([sys.executable, "block_evasion.py"], 0)
        with mock.patch.object(adapter.subprocess, "run", side_effect=skipped_guard), \
                contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(adapter.check(payload), 2)

    def test_command_guard_runs_the_shared_job_in_the_payload_session_checkout(self):
        self.load_codex_adapter()
        guard = sys.modules["command_guard"]
        self.assertEqual(guard.payload_cwd(b'{"cwd":"/session/tree"}'), "/session/tree")
        self.assertEqual(guard.payload_cwd(b'{"cwd":7}'), 7)
        for raw in (b'{"tool_input":{}}', b"[]", b"null", b"{", b"\xff", b"[" * 100000):
            with self.subTest(raw=raw[:8]):
                self.assertIsNone(guard.payload_cwd(raw))
        seen = []
        def shared_job(*_args, **kwargs):
            seen.append(kwargs["cwd"])
            kwargs["stdout"].write(b"MARKER\n")
            return subprocess.CompletedProcess(["lefthook"], 0)
        with mock.patch.object(guard, "session_root", return_value=Path("/session/tree")) as select, \
                mock.patch.object(guard.subprocess, "run", side_effect=shared_job):
            self.assertEqual(guard.check_job(b'{"cwd":"/session/tree/sub"}', "job", b"MARKER"), 0)
            self.assertEqual(guard.check_job(b"{", "job", b"MARKER"), 0)
        self.assertEqual(select.call_args_list, [mock.call(guard.ROOT, "/session/tree/sub", mock.ANY),
                                                 mock.call(guard.ROOT, None, mock.ANY)])
        self.assertEqual(seen, [Path("/session/tree")] * 2)

    def test_command_guard_draws_every_step_from_one_budget_and_refuses_once_it_is_spent(self):
        """The checkout lookup and the shared job share one budget below the client timeout.

        A client lets a call through once the guard outlives its registered timeout, so a
        lookup that cannot answer, or a budget spent before the job starts, must deny.
        """
        self.load_codex_adapter()
        guard = sys.modules["command_guard"]
        timeouts = []
        def shared_job(*_args, **kwargs):
            timeouts.append(kwargs["timeout"])
            kwargs["stdout"].write(b"MARKER\n")
            return subprocess.CompletedProcess(["lefthook"], 0)
        with mock.patch.object(guard, "session_root", return_value=Path("/session/tree")), \
                mock.patch.object(guard.subprocess, "run", side_effect=shared_job):
            self.assertEqual(guard.check_job(b"{}", "job", b"MARKER"), 0)
        self.assertEqual(len(timeouts), 1)
        self.assertTrue(0 < timeouts[0] <= guard.GUARD_JOB_TIMEOUT)
        failures = {"git timed out": {"session_root": mock.Mock(side_effect=HookError("git timed out"))},
                    "budget spent": {"GUARD_BUDGET": 0}}
        for reason, patches in failures.items():
            with self.subTest(reason=reason), mock.patch.multiple(guard, **patches), \
                    mock.patch.object(guard.subprocess, "run", side_effect=shared_job) as job, \
                    contextlib.redirect_stderr(io.StringIO()) as diagnostic:
                self.assertEqual(guard.check_job(b"{}", "job", b"MARKER"), 2)
                self.assertIn("policy unavailable", diagnostic.getvalue())
                self.assertIn(reason, diagnostic.getvalue())
                job.assert_not_called()

    def test_command_guard_refuses_when_it_is_still_blocked_at_its_limit(self):
        """A wait no process bound reaches still ends in a denial before the client gives up."""
        hooks = GUARD.parent
        script = ("import sys, time\n"
                  f"sys.path.insert(0, {str(hooks)!r})\n"
                  "import command_guard as guard\n"
                  "guard.GUARD_LIMIT = 0.3\n"
                  "guard.session_root = lambda *_args: time.sleep(30)\n"
                  "sys.exit(guard.main())\n")
        started = time.monotonic()
        result = subprocess.run([sys.executable, "-B", "-c", script], input=b'{"cwd":"/"}',
                                capture_output=True, timeout=20, check=False)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertIn(b"no answer within 0.3 s; call refused", result.stderr)
        self.assertLess(time.monotonic() - started, 10)

    def test_reverse_dependencies_embed_testdata_module_and_docs_scope(self):
        with tempfile.TemporaryDirectory(prefix="praetor-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scope\n\ngo 1.27\n")
            (root / "a").mkdir()
            (root / "b").mkdir()
            (root / "c").mkdir()
            (root / "a/a.go").write_text('package a\nimport _ "embed"\n//go:embed data.txt guide.md\nvar Data string\n')
            (root / "a/data.txt").write_text("embedded")
            (root / "a/guide.md").write_text("embedded markdown")
            (root / "a/assets").mkdir()
            (root / "a/assets/remaining.json").write_text("{}")
            with (root / "a/a.go").open("a") as stream:
                stream.write('//go:embed assets/*\nvar Assets string\n')
            (root / "b/b.go").write_text('package b\nimport "example.test/scope/a"\nvar Value = a.Data\n')
            (root / "c/c.go").write_text("package c\n")
            expected = ["example.test/scope/a", "example.test/scope/b"]
            for names in (["a/a.go"], ["a/data.txt"], ["a/testdata/input.json"], ["a/guide.md"]):
                self.assertEqual(go_packages(root, names, reverse=True), expected)
            self.assertEqual(go_packages(root, ["README.md"], reverse=True), [])
            self.assertEqual(len(go_packages(root, ["go.mod"], reverse=True)), 3)
            self.assertEqual(go_packages(root, ["removed/old.go"], reverse=True), [])
            self.assertEqual(go_packages(root, ["a/assets/deleted.json"], reverse=True), expected)

    def test_governance_selection_without_go_changes(self):
        with tempfile.TemporaryDirectory(prefix="praetor-governance-") as temp:
            root = Path(temp)
            (root / ".standards.yaml").write_text("repository: {}\n")
            (root / ".workingdir").mkdir()
            self.init_governance_repo(root)
            for name in (".standards.yaml", ".standards.lock", ".standards-baseline.json", ".agents/persona.md"):
                commands = governance_commands(root, [name], False)
                self.assertEqual(commands[0][3:], ["audit", "--touched=.standards.yaml"])
                self.assertEqual(commands[1][3:], ["flavor", "audit", "."])
            self.assertEqual(len(governance_commands(root, ["README.md"], False)), 2)
            self.assertEqual(len(governance_commands(root, ["a.go"], True)), 2)
            self.assertEqual(governance_commands(root, [".workingdir/OPEN.md"], False)[0][3:], ["state", "audit", "."])
            for name in (".gemini/GEMINI.md", ".codex/rules.md", ".cursor/rules/hiss-invariants.mdc"):
                self.assertTrue(context_changed([name]))
            with mock.patch("checks.run", return_value=b""), \
                    mock.patch("checks.parallel", side_effect=HookError("governance rejected")) as gate:
                with self.assertRaisesRegex(HookError, "governance rejected"):
                    source_checks(root, [".standards.yaml"])
                self.assertEqual(len(gate.call_args.args[0]), 2)

    def test_governance_only_push_retains_gate_and_pinned_receipt_verification(self):
        """Governance-only push executes gate verification and checks pinned exit receipt."""
        with tempfile.TemporaryDirectory(prefix="praetor-receipt-") as temp:
            root = Path(temp)
            (root / ".standards.yaml").write_text("repository: {}\n")
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()
            calls = []
            def process(argv, **kwargs):
                if argv[0] == "git":
                    return run(argv, **kwargs)
                calls.append(argv)
                return b'{"timeout_seconds": 480}' if argv[3:5] == ["gate", "deadline"] else b""
            with mock.patch("checks.parallel"), mock.patch("checks.run", side_effect=process):
                self.assertTrue(source_checks(root, [".standards.yaml"], base=base))
                self.assertEqual(calls, [[toolchain.make_program(), "--no-print-directory", "state-audit"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "deadline", "--json"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "run", "--path=.", "--race-packages=none"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "verify", "--path=."]])
            def reject_pin(argv, **kwargs):
                if argv[:5] == ["go", "run", "./cmd/standardsctl", "gate", "verify"]:
                    raise HookError("pin mismatch")
                return process(argv, **kwargs)
            with (
                mock.patch("checks.parallel"),
                mock.patch("checks.run", side_effect=reject_pin),
                self.assertRaisesRegex(HookError, "pin mismatch"),
            ):
                source_checks(root, [".standards.yaml"], base=base)

    def test_gate_timeout_reads_the_real_gate_deadline(self):
        # The hook's bound comes from the CLI it runs, so replay the contract through the hook's
        # own invocation: unset, an unusable value, the exact ceiling and one step past it.
        query = ["go", "run", "./cmd/standardsctl", "gate", "deadline", "--json"]
        # PRAETOR_GATE_DEVCONTAINER=off keeps the table independent of the host: where a container
        # runtime exists, the deadline also reserves the devcontainer image build bound.
        expected = {
            "": ("20m0s", ""),
            "soon": ("20m0s", "not a duration"),
            "30m": ("30m0s", "raised"),
            "31m": ("30m0s", "clamped"),
        }
        for value, (stage_bound, note) in expected.items():
            with self.subTest(value=value):
                environment = dict(
                    clean_env(), PRAETOR_TEST_STAGE_TIMEOUT=value, PRAETOR_GATE_DEVCONTAINER="off"
                )
                report = run(query, cwd=ROOT, env=environment, timeout=GATE_QUERY_TIMEOUT)
                resolved = json.loads(report)
                self.assertEqual(resolved["stage_bound"], stage_bound)
                self.assertIn(note, resolved.get("note", ""))
                self.assertEqual(gate_timeout(report), resolved["timeout_seconds"] + GATE_LAUNCH_MARGIN)
        # With the default (auto) the hook still reads the CLI's figure: the plain deadline, or the
        # plain deadline plus the 15-minute image build bound when this host can build the image.
        report = run(query, cwd=ROOT, env=dict(clean_env(), PRAETOR_TEST_STAGE_TIMEOUT=""),
                     timeout=GATE_QUERY_TIMEOUT)
        resolved = json.loads(report)
        self.assertEqual(gate_timeout(report), resolved["timeout_seconds"] + GATE_LAUNCH_MARGIN)

    def test_audit_range_uses_exact_oid_and_touched_paths(self):
        with tempfile.TemporaryDirectory(prefix="praetor-audit-range-") as temp:
            root = Path(temp)
            (root / "legacy.go").write_text("package legacy\n")
            (root / ".standards-baseline.json").write_text('{"total_infractions": 0}\n')
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()
            supplied = ["legacy.go", ".standards-baseline.json"]
            self.assertEqual(audit_scope(root, supplied, base),
                             ["--base=" + base, "--touched=" + ",".join(supplied)])
            # Missing explicit refs cannot silently drop historical comparison.
            for invalid in ("f" * 40, "--help", "missing-branch"):
                with self.assertRaises(HookError):
                    audit_scope(root, supplied, invalid)
            # Absent remote history makes every tracked file touched, even when
            # callers request a narrower path list.
            self.assertEqual(audit_scope(root, ["README.md"], None),
                             ["--touched=.standards-baseline.json,legacy.go"])
            for unrepresentable in (["comma,name.go"], ["line\nname.go"], [" leading.go"], ["a.go"] * 10001):
                with self.assertRaisesRegex(HookError, "lossless CSV"):
                    audit_scope(root, unrepresentable, base)

    def test_audit_scope_stays_within_argument_limit(self):
        with tempfile.TemporaryDirectory(prefix="praetor-audit-limit-") as temp:
            root = Path(temp)
            (root / "legacy.go").write_text("package legacy\n")
            (root / ".standards-baseline.json").write_text('{"total_infractions": 0}\n')
            (root / ".standards.yaml").write_text("repository: {}\n")
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()
            # 3000 paths of 50 chars each -> CSV with prefix is ~153 KB (> 120 KB limit).
            prefix_len = len("pkg0000/")
            pad_len = 50 - prefix_len - 3
            paths_3000 = [f"pkg{i:04d}/" + "x" * pad_len + ".go" for i in range(3000)]
            self.assertEqual(len(paths_3000[0]), 50)
            self.assertEqual(len(paths_3000), 3000)
            with io.StringIO() as stderr_buf, contextlib.redirect_stderr(stderr_buf):
                args = audit_scope(root, paths_3000, base)
                self.assertEqual(args, ["--base=" + base])
                self.assertIn(
                    "3000 touched paths exceed argument limit; "
                    "audit derives touched files from base.",
                    stderr_buf.getvalue(),
                )
            with io.StringIO(), contextlib.redirect_stderr(io.StringIO()):
                commands = governance_commands(root, paths_3000, True, base=base)
                self.assertEqual(commands[0][3:], ["audit", "--base=" + base])

    def test_audit_scope_argument_limit_boundaries_and_negative(self):
        with tempfile.TemporaryDirectory(prefix="praetor-audit-bound-") as temp:
            root = Path(temp)
            (root / "legacy.go").write_text("package legacy\n")
            (root / ".standards-baseline.json").write_text('{"total_infractions": 0}\n')
            self.init_governance_repo(root)
            base = command(root, "git", "rev-parse", "HEAD").stdout.decode().strip()

            # Boundary: CSV exactly at limit (120,000 bytes). Prefix "--touched=" is 10 bytes.
            p1 = [f"d/{i:04d}/" + "a" * (49 - len(f"d/{i:04d}/")) for i in range(2399)]
            exact_paths = p1 + ["last/" + "b" * (40 - len("last/"))]
            exact_csv = "--touched=" + ",".join(exact_paths)
            self.assertEqual(len(exact_csv.encode("utf-8")), 120_000)
            self.assertEqual(audit_scope(root, exact_paths, base), ["--base=" + base, exact_csv])

            # One byte over limit -> base only.
            over_paths = p1 + ["last/" + "b" * (41 - len("last/"))]
            over_csv = "--touched=" + ",".join(over_paths)
            self.assertEqual(len(over_csv.encode("utf-8")), 120_001)
            with io.StringIO() as stderr_buf, contextlib.redirect_stderr(stderr_buf):
                self.assertEqual(audit_scope(root, over_paths, base), ["--base=" + base])
                self.assertIn(
                    "2400 touched paths exceed argument limit; "
                    "audit derives touched files from base.",
                    stderr_buf.getvalue(),
                )

            # Negative: small scope stays byte-identical to base.
            self.assertEqual(
                audit_scope(root, ["legacy.go"], base),
                ["--base=" + base, "--touched=legacy.go"],
            )

        # Negative: no base + over limit -> HookError.
        with tempfile.TemporaryDirectory(prefix="praetor-audit-nobase-") as temp_nobase:
            nobase = Path(temp_nobase)
            for i in range(2400):
                target = nobase / f"pkg{i:04d}" / ("z" * 41)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text("ok\n")
            self.init_governance_repo(nobase)
            with self.assertRaisesRegex(
                HookError, r"Audit touched paths \(2400\) exceed single-argument limit"
            ):
                audit_scope(nobase, ["ignored"], None)

    def test_real_scoped_race_test_propagates_failure(self):
        skip_without_race_detector(self)
        with tempfile.TemporaryDirectory(prefix="praetor-race-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/race\n\ngo 1.27\n")
            (root / "a.go").write_text("package a\n")
            test = root / "a_test.go"
            test.write_text('package a\nimport "testing"\nfunc TestA(t *testing.T) {}\n')
            source_checks(root, ["a_test.go"], "test")
            test.write_text('package a\nimport "testing"\nfunc TestA(t *testing.T) { t.Fatal("negative control") }\n')
            with self.assertRaisesRegex(HookError, "negative control"):
                source_checks(root, ["a_test.go"], "test")

    def test_real_scoped_lint_scans_reverse_dependencies(self):
        with tempfile.TemporaryDirectory(prefix="praetor-lint-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / ".golangci.yml").write_text(
                'version: "2"\nlinters:\n  default: none\n  enable: [errcheck]\n')
            (root / "tools/lint").mkdir(parents=True)
            shutil.copyfile(ROOT / "tools/lint/go.mod", root / "tools/lint/go.mod")
            shutil.copyfile(ROOT / "tools/lint/go.sum", root / "tools/lint/go.sum")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/scopes/core"\n'
                                'func Value() int { return core.Value() }\n')
            # Use the actual from-source invocation: an installed binary may have been
            # built with an older Go version than the checked module requires.
            source_checks(root, ["core/core.go"], "lint")
            consumer.write_text('package consumer\nimport ("example.test/scopes/core"; "os")\n'
                                'func Value() int { os.Chdir("."); return core.Value() }\n')
            with self.assertRaisesRegex(HookError, "errcheck"):
                source_checks(root, ["core/core.go"], "lint")

    def test_checkpoint_test_only_packages_still_run_and_propagate_failure(self):
        skip_without_race_detector(self)
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-test-only-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/onlytests\n\ngo 1.27\n")
            test = root / "only_test.go"
            test.write_text('package onlytests\nimport "testing"\n'
                            'func TestOnly(t *testing.T) {}\n')
            checkpoint_checks(root, ["only_test.go"])
            test.write_text('package onlytests\nimport "testing"\n'
                            'func TestOnly(t *testing.T) { t.Fatal("test-only failure") }\n')
            with self.assertRaisesRegex(HookError, "test-only failure"):
                checkpoint_checks(root, ["only_test.go"])

    def test_checkpoint_real_build_rejects_broken_reverse_dependency(self):
        skip_without_race_detector(self)
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-build-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/checkpoint\n\ngo 1.27\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/checkpoint/core"\n'
                                'func Value() int { return core.Value() }\n')
            checkpoint_checks(root, ["core/core.go"])
            consumer.write_text('package consumer\nimport "example.test/checkpoint/core"\n'
                                'func Value() int { return core.Missing() }\n')
            with self.assertRaisesRegex(HookError, "go build.*|undefined: core.Missing") as failure:
                checkpoint_checks(root, ["core/core.go"])
            self.assertIn("undefined: core.Missing", str(failure.exception))

    def test_checkpoint_real_race_detector_rejects_consumer_test_race(self):
        skip_without_race_detector(self)
        with tempfile.TemporaryDirectory(prefix="praetor-checkpoint-race-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/checkpoint\n\ngo 1.27\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            (root / "consumer.go").write_text("package consumer\n")
            test = root / "consumer_test.go"
            test.write_text('package consumer\nimport ("testing"; "example.test/checkpoint/core")\n'
                            'func TestConsumer(t *testing.T) { if core.Value() != 1 { t.Fatal("value") } }\n')
            checkpoint_checks(root, ["core/core.go"])
            test.write_text('package consumer\nimport ("testing"; "example.test/checkpoint/core")\n'
                            'func TestConsumer(t *testing.T) {\n'
                            'var value int; done := make(chan bool)\n'
                            'go func() { value = core.Value(); done <- true }()\n'
                            'value = 2; <-done; _ = value\n}\n')
            with self.assertRaisesRegex(HookError, "DATA RACE"):
                checkpoint_checks(root, ["core/core.go"])

    def test_real_scoped_gosec_cannot_succeed_without_scanning(self):
        with tempfile.TemporaryDirectory(prefix="praetor-gosec-scope-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / ".gosec.json").write_text("{}\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            consumer = root / "consumer.go"
            consumer.write_text('package consumer\nimport "example.test/scopes/core"\n'
                                'func Value() int { return core.Value() }\n')
            source_checks(root, ["core/core.go"], "sec")
            consumer.write_text('package consumer\nimport ("example.test/scopes/core"; "crypto/md5")\n'
                                'func Sum() [md5.Size]byte { return md5.Sum([]byte{byte(core.Value())}) }\n')
            with self.assertRaisesRegex(HookError, "G401|G501"):
                source_checks(root, ["core/core.go"], "sec")

    def test_scoped_vuln_runs_the_go_vulnerability_gate(self):
        """The vuln job runs praetorctl security govuln, the gate's one implementation (HISS-19).

        Plain govulncheck over the affected packages failed only on a called symbol and judged
        no OpenVEX statement, a second vulnerability check that could drift from the gate.
        """
        root = Path(tempfile.mkdtemp(prefix="praetor-vuln-gate-test-"))
        self.addCleanup(shutil.rmtree, root)
        (root / "tools/lint").mkdir(parents=True)
        (root / "tools/lint/go.mod").write_text("module lint\n")
        gate = ["go", "run", "./cmd/standardsctl", "security", "govuln", "--path=.", "--",
                "govulncheck"]
        for gate_name, governance in (("vuln", []), ("all", [["go", "run", "./cmd/standardsctl",
                                                                "flavor", "audit", "."]])):
            with self.subTest(gate=gate_name), \
                    mock.patch("checks.go_packages", return_value=["example.test/core"]), \
                    mock.patch("checks.governance_commands", return_value=governance), \
                    mock.patch("checks.semgrep_commands", return_value=[]), \
                    mock.patch("checks.local_package_patterns", return_value=["./core"]), \
                    mock.patch("checks.run", return_value=(
                        b"github.com/golangci/golangci-lint/v2/cmd/golangci-lint\n")), \
                    mock.patch("checks.parallel") as jobs:
                self.assertFalse(source_checks(root, ["core/core.go"], gate_name))
                commands = jobs.call_args_list[-1].args[0]
                self.assertIn(gate, commands)
                self.assertFalse([command for command in commands if command[0] == "govulncheck"])

    def test_tool_module_dirs_finds_nested_go_modules(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        with tempfile.TemporaryDirectory(prefix="praetor-tool-dirs-") as temp:
            root = Path(temp)
            (root / "tools/lefthook").mkdir(parents=True)
            (root / "tools/lefthook/go.mod").write_text("module example.test/lh\n")
            (root / "tools/lefthook/go.sum").write_text("")
            (root / "tools/lint").mkdir(parents=True)
            (root / "tools/lint/go.mod").write_text("module example.test/lint\n")
            (root / "tools/apicompat/testdata/alias/base").mkdir(parents=True)
            (root / "tools/apicompat/testdata/alias/base/go.mod").write_text("module alias\n")
            (root / "core").mkdir()
            (root / "core/go.mod").write_text("module core\n")

            dirs = checks.tool_module_dirs(
                root,
                [
                    "tools/lefthook/go.mod",
                    "tools/lefthook/go.sum",
                    "tools/lint/go.mod",
                    "tools/missing/go.mod",
                    "tools/apicompat/testdata/alias/base/go.mod",
                    "core/go.mod",
                    "README.md",
                ],
            )
            self.assertEqual(dirs, ["tools/lefthook", "tools/lint"])

    def test_trivy_tool_command_matches_ci_specification(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        cmd = checks.trivy_tool_command("tools/lefthook")
        self.assertEqual(
            cmd,
            [
                "trivy",
                "fs",
                "--scanners",
                "vuln",
                "--severity",
                "HIGH,CRITICAL",
                "--exit-code",
                "1",
                "--skip-version-check",
                "tools/lefthook",
            ],
        )

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_prepush_two_tool_modules_scan_one_target_each(self):
        # Order: engine-1629-landing-failed-20261027-0914. Real trivy fs takes one target; the stub
        # refuses more than one the same way, and a finding in either module refuses the push.
        with tempfile.TemporaryDirectory(prefix="praetor-trivy-two-") as temp:
            root = Path(temp)
            for name in ("lefthook", "lint"):
                (root / "tools" / name).mkdir(parents=True)
                (root / "tools" / name / "go.mod").write_text(f"module example.test/{name}\n")
            bin_dir = root / "bin"
            calls = root / "trivy_calls.txt"
            stand_in(
                bin_dir,
                "trivy",
                f'printf "%s\\n" "$*" >> "{calls}"\n'
                'last=""; n=0; for a in "$@"; do case "$a" in tools/*) n=$((n+1)); last="$a";; esac; done\n'
                'if [ "$n" -ne 1 ]; then echo "FATAL multiple targets cannot be specified" >&2; exit 1; fi\n'
                'if [ "$last" = tools/lint ]; then echo "CVE-2026-78669 HIGH in golang.org/x/net" >&2; exit 1; fi\n'
                'exit 0\n',
            )
            shadowed = os.pathsep.join([str(bin_dir), os.environ.get("PATH", "")])
            with mock.patch.dict(os.environ, {"PATH": shadowed}):
                with self.assertRaises(HookError) as cm:
                    source_checks(root, ["tools/lefthook/go.mod", "tools/lint/go.mod"])
            error_msg = str(cm.exception)
            self.assertIn("tools/lint", error_msg)
            self.assertIn("CVE-2026-78669", error_msg)
            self.assertNotIn("multiple targets", error_msg)
            self.assertEqual(len(calls.read_text().splitlines()), 2)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_prepush_tool_module_trivy_finding_fails(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        with tempfile.TemporaryDirectory(prefix="praetor-trivy-fail-") as temp:
            root = Path(temp)
            (root / "tools/lefthook").mkdir(parents=True)
            (root / "tools/lefthook/go.mod").write_text("module example.test/lh\n")
            bin_dir = root / "bin"
            stand_in(
                bin_dir,
                "trivy",
                'echo "CVE-2026-78669 HIGH in golang.org/x/net" >&2\nexit 1\n',
            )
            shadowed = os.pathsep.join([str(bin_dir), os.environ.get("PATH", "")])
            with mock.patch.dict(os.environ, {"PATH": shadowed}):
                with self.assertRaises(HookError) as cm:
                    source_checks(root, ["tools/lefthook/go.mod"])
            error_msg = str(cm.exception)
            self.assertIn("tools/lefthook", error_msg)
            self.assertIn("CVE-2026-78669", error_msg)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_prepush_tool_module_trivy_clean_passes(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        with tempfile.TemporaryDirectory(prefix="praetor-trivy-pass-") as temp:
            root = Path(temp)
            (root / "tools/lefthook").mkdir(parents=True)
            (root / "tools/lefthook/go.mod").write_text("module example.test/lh\n")
            bin_dir = root / "bin"
            args_file = root / "trivy_args.txt"
            stand_in(
                bin_dir,
                "trivy",
                f'printf "%s\\n" "$@" > "{args_file}"\nexit 0\n',
            )
            shadowed = os.pathsep.join([str(bin_dir), os.environ.get("PATH", "")])
            with mock.patch.dict(os.environ, {"PATH": shadowed}):
                source_checks(root, ["tools/lefthook/go.mod"])
            self.assertTrue(args_file.is_file(), "stub trivy was not invoked")
            argv = args_file.read_text().splitlines()
            self.assertIn("--severity", argv)
            self.assertIn("HIGH,CRITICAL", argv)
            self.assertIn("--exit-code", argv)
            self.assertIn("1", argv)
            self.assertIn("--scanners", argv)
            self.assertIn("vuln", argv)
            self.assertIn("tools/lefthook", argv)

    def test_prepush_tool_module_trivy_absent_skips_with_notice(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        with tempfile.TemporaryDirectory(prefix="praetor-trivy-absent-") as temp:
            root = Path(temp)
            (root / "tools/lefthook").mkdir(parents=True)
            (root / "tools/lefthook/go.mod").write_text("module example.test/lh\n")
            orig_which = shutil.which
            with mock.patch("shutil.which", side_effect=lambda cmd, *a, **kw: None if cmd == "trivy" else orig_which(cmd, *a, **kw)), \
                    io.StringIO() as out, \
                    contextlib.redirect_stdout(out):
                source_checks(root, ["tools/lefthook/go.mod"])
                output = out.getvalue()
            expected = "Trivy not installed: tool-module vulnerability scan skipped for tools/lefthook; scan runs only in CI security.yml"
            self.assertIn(expected, output)

    def test_prepush_without_tool_module_skips_trivy(self):
        # Order: engine-prepush-trivy-tool-modules-20261027.
        with tempfile.TemporaryDirectory(prefix="praetor-trivy-unused-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/main\n\ngo 1.27\n")
            (root / "core").mkdir()
            (root / "core/core.go").write_text("package core\nfunc Value() int { return 1 }\n")
            bin_dir = root / "bin"
            invoked_file = root / "trivy_invoked"
            stand_in(
                bin_dir,
                "trivy",
                f'touch "{invoked_file}"\nexit 1\n',
            )
            shadowed = os.pathsep.join([str(bin_dir), os.environ.get("PATH", "")])
            with mock.patch.dict(os.environ, {"PATH": shadowed}), \
                    mock.patch("checks.go_packages", return_value=[]), \
                    mock.patch("checks.governance_commands", return_value=[]), \
                    mock.patch("checks.semgrep_commands", return_value=[]):
                source_checks(root, ["core/core.go"])
            self.assertFalse(invoked_file.exists(), "trivy must not be invoked when no tool module changed")

    def test_prepush_hook_sources_changed_identifies_hook_paths(self):
        self.assertTrue(hook_sources_changed([".config/lefthook/scripts/checkpoint.py"]))
        self.assertTrue(hook_sources_changed([".config/agent/hooks/block_evasion.py"]))
        self.assertTrue(hook_sources_changed([HOOK_LINT_SUITE]))
        self.assertFalse(hook_sources_changed([".config/lefthook/README.md"]))
        self.assertFalse(hook_sources_changed([".config/lefthook/praetor.yml"]))
        self.assertFalse(hook_sources_changed(["cmd/praetorctl/main.go"]))
        self.assertFalse(hook_sources_changed(["docs/guides/git-hooks.md"]))

    def test_prepush_require_hook_lint_tools_fails_closed_when_mismatched_or_absent(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            # When suite is absent (e.g. adopted repository), require_hook_lint_tools returns None
            self.assertIsNone(require_hook_lint_tools(root))

        # Test tool missing / version mismatch fails closed with install command (HISS-19, HISS-21)
        mock_module = mock.Mock()
        mock_module.TOOLS = ("black", "flake8", "yamllint", "ruff")
        mock_module.GO_PINNED_TOOLS = ("gofumpt", "shfmt")
        mock_module.toolchain_pins.return_value = {"black": "26.10.1", "flake8": "7.4.1"}
        mock_module.resolve_tool.side_effect = lambda tool, pins, environ=None: (
            (None, f"{tool} 7.4.1 is pinned but /bin/{tool} reports 7.5.0; install it with: "
                   "python3 -m pip install --require-hashes -r .config/hook-lint/requirements.txt",
             False) if tool == "flake8" else (f"/bin/{tool}", None, False)
        )
        with mock.patch("checks.load_hook_lint", return_value=mock_module):
            with self.assertRaises(HookError) as cm:
                require_hook_lint_tools(Path.cwd())
            err = str(cm.exception)
            self.assertIn("flake8 7.4.1 is pinned", err)
            self.assertIn(
                "python3 -m pip install --require-hashes -r .config/hook-lint/requirements.txt",
                err,
            )

    def test_prepush_file_checks_schedules_hook_lint_on_hook_path(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "scripts").mkdir(parents=True)
            (root / HOOK_LINT_SUITE).write_text("# suite\n", encoding="utf-8")
            (root / ".config/lefthook/scripts").mkdir(parents=True)
            hook_script = root / ".config/lefthook/scripts/checkpoint.py"
            hook_script.write_text("# script\n", encoding="utf-8")
            # LF on every platform: text mode writes CRLF on Windows, and gofmt -l lists such a file.
            (root / "main.go").write_text("package main\n", encoding="utf-8", newline="\n")

            stub_bin = root / "cached" / "bin"
            stub_bin.mkdir(parents=True)

            # A push touching a hook path schedules the hook lint suite with toolchain env
            with mock.patch("checks.parallel") as scheduled, \
                    mock.patch("checks.require_floors"), \
                    mock.patch("checks.provision_hook_lint", return_value=stub_bin), \
                    mock.patch("checks.require_hook_lint_tools") as require_lint:
                file_checks(root, [".config/lefthook/scripts/checkpoint.py"])
            require_lint.assert_called_once_with(root, environ={HOOK_LINT_BIN_ENV: str(stub_bin)})
            scheduled.assert_called_once()
            commands = scheduled.call_args.args[0]
            self.assertIn([sys.executable, "-B", HOOK_LINT_SUITE], commands)
            suite_cmd = next(c for c in commands if c == [sys.executable, "-B", HOOK_LINT_SUITE])
            self.assertEqual(suite_cmd.env, {HOOK_LINT_BIN_ENV: str(stub_bin)})

            # A push touching no hook path does not run or schedule the stage
            with mock.patch("checks.parallel") as scheduled, \
                    mock.patch("checks.require_floors"), \
                    mock.patch("checks.provision_hook_lint") as prov_lint, \
                    mock.patch("checks.require_hook_lint_tools") as require_lint:
                file_checks(root, ["main.go"])
            prov_lint.assert_not_called()
            require_lint.assert_not_called()
            scheduled.assert_called_once()
            commands = scheduled.call_args.args[0]
            self.assertNotIn([sys.executable, "-B", HOOK_LINT_SUITE], commands)

    def hook_lint_repo(self, temp):
        root = Path(temp) / "repo"
        (root / ".config/hook-lint").mkdir(parents=True)
        (root / "tools/go").mkdir(parents=True)
        (root / ".config/hook-lint/requirements.txt").write_bytes(b"black==26.10.1\n")
        (root / "tools/go/go.mod").write_bytes(b"module tools\n")
        return root, Path(temp) / "cache"

    @staticmethod
    def fake_toolchain_runner(fail=None, calls=None):
        """A provisioning runner that builds a venv directory and fails the named step."""
        def runner(cmd, cwd=None, env=None, timeout=None):
            if calls is not None:
                calls.append(cmd)
            step = "venv" if "venv" in cmd else "pip" if "pip" in cmd else "go"
            if step == fail:
                raise HookError(f"{step} execution error")
            if step == "venv":
                venv_bin(Path(cmd[-1])).mkdir(parents=True)
            return b""
        return runner

    def test_prepush_hook_lint_provisioning_builds_once_and_reuses(self):
        with tempfile.TemporaryDirectory() as temp:
            root, cache_home = self.hook_lint_repo(temp)
            calls = []
            runner = self.fake_toolchain_runner(calls=calls)
            with mock.patch("shutil.which", return_value="/usr/bin/go"):
                first = provision_hook_lint(root, cache_home=cache_home, runner=runner)
                self.assertEqual(len(calls), 3)
                self.assertEqual(provision_hook_lint(root, cache_home=cache_home, runner=runner), first)
            self.assertEqual(len(calls), 3, "the second call runs no installer")
            self.assertTrue(first.is_dir())
            self.assertEqual(first.parent.parent, hook_lint_cache_root(cache_home))
            # The venv is used where it was built: no rename, so its scripts keep their path.
            self.assertIn(str(first.parent), " ".join(calls[0]))

    def test_prepush_hook_lint_rebuilds_when_the_build_is_gone_or_python_changed(self):
        with tempfile.TemporaryDirectory() as temp:
            root, cache_home = self.hook_lint_repo(temp)
            calls = []
            runner = self.fake_toolchain_runner(calls=calls)
            with mock.patch("shutil.which", return_value="/usr/bin/go"):
                first = provision_hook_lint(root, cache_home=cache_home, runner=runner)
                shutil.rmtree(first.parent)
                second = provision_hook_lint(root, cache_home=cache_home, runner=runner)
                self.assertEqual(len(calls), 6, "a pointer to a removed build builds again")
                self.assertTrue(second.is_dir())
                digest = hook_lint_digest(root)
                with mock.patch("checks.sys.version", "another interpreter"):
                    self.assertNotEqual(hook_lint_digest(root), digest)

    def test_prepush_hook_lint_provisioning_failure_leaves_nothing_and_names_step(self):
        cases = (("venv", "/usr/bin/go", "python3 -m venv"),
                 ("pip", "/usr/bin/go", "pip install --require-hashes"),
                 (None, None, "go install -modfile"),
                 ("go", "/usr/bin/go", "go install -modfile"))
        for fail, go_path, command in cases:
            with self.subTest(fail=fail, go=go_path), tempfile.TemporaryDirectory() as temp:
                root, cache_home = self.hook_lint_repo(temp)
                with mock.patch("shutil.which", return_value=go_path), self.assertRaises(HookError) as cm:
                    provision_hook_lint(root, cache_home=cache_home, runner=self.fake_toolchain_runner(fail))
                self.assertIn("hook lint toolchain provisioning failed", str(cm.exception))
                self.assertIn(command, str(cm.exception))
                self.assertEqual(list(hook_lint_cache_root(cache_home).iterdir()), [],
                                 "neither a build folder nor a pointer stays behind")

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_prepush_hook_lint_planted_stub_toolchain_end_to_end(self):
        """Planted: the real suite passes on stub tools at the pins and fails on a rejecting black."""
        with tempfile.TemporaryDirectory() as temp:
            fake_bin = Path(temp) / "bin"
            fake_bin.mkdir()
            module = load_hook_lint(ROOT)
            pins = module.toolchain_pins()

            for tool in (*module.TOOLS, *module.GO_PINNED_TOOLS):
                version = pins[tool]
                stub = fake_bin / tool
                stub.write_text(f"""#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "{tool} {version}"
    exit 0
fi
exit 0
""")
                stub.chmod(0o755)

            # Passing case: command carrying PRAETOR_HOOK_LINT_BIN=fake_bin passes parallel file check
            cmd_pass = Command([sys.executable, "-B", "scripts/test_emitted_hook_lint.py",
                                "EmittedSourcesTest.test_black_accepts_hook_python"],
                               env={HOOK_LINT_BIN_ENV: str(fake_bin)})
            parallel([cmd_pass], ROOT)

            # Planted rejecting black: stub black exits 1
            stub_black = fake_bin / "black"
            stub_black.write_text(f"""#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "black {pins['black']}"
    exit 0
fi
echo "planted black formatting refusal" >&2
exit 1
""")
            stub_black.chmod(0o755)

            cmd_fail = Command([sys.executable, "-B", "scripts/test_emitted_hook_lint.py",
                                "EmittedSourcesTest.test_black_accepts_hook_python"],
                               env={HOOK_LINT_BIN_ENV: str(fake_bin)})
            with self.assertRaises(HookError) as cm:
                parallel([cmd_fail], ROOT)
            self.assertIn("exited 1", str(cm.exception))
            self.assertIn("planted black formatting refusal", str(cm.exception))

    def test_local_package_patterns_confine_actual_go_metadata(self):
        with tempfile.TemporaryDirectory(prefix="praetor-package-dirs-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/scopes\n\ngo 1.27\n")
            (root / "a.go").write_text("package scopes\n")
            (root / "nested").mkdir()
            (root / "nested/a.go").write_text("package nested\n")
            selected = ["example.test/scopes", "example.test/scopes/nested"]
            self.assertEqual(local_package_patterns(root, selected), [".", "./nested"])
            with self.assertRaisesRegex(HookError, "outside the checked snapshot"):
                local_package_patterns(root, ["fmt"])
            with self.assertRaises(HookError):
                local_package_patterns(root, ["example.test/scopes/missing"])

    def test_local_package_patterns_resolves_a_symlinked_root(self):
        """A symlinked snapshot root must not read as outside the checked snapshot.

        `local_package_patterns` already resolved both operands before comparing; this locks
        that behavior in through the shared `common.resolved_relative_to` helper introduced for
        `go_packages` below, so the two call sites cannot drift back to resolving one side and
        not the other (HISS-19). See `test_go_packages_resolves_a_symlinked_directory` for the
        call site that actually raised on a symlinked ancestor.
        """
        with tempfile.TemporaryDirectory(prefix="praetor-package-real-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/symlinked\n\ngo 1.27\n")
            (root / "a.go").write_text("package symlinked\n")
            alias = root.parent / (root.name + "-alias")
            alias.symlink_to(root)
            try:
                self.assertEqual(local_package_patterns(alias, ["example.test/symlinked"]), ["."])
            finally:
                alias.unlink()

    def test_go_packages_resolves_a_symlinked_directory(self):
        """go_packages must match a package's Dir even when `directory` is reached via a symlink.

        `go list`, run the way `run()` invokes it (subprocess.Popen with no $PWD override), loses
        its caller's spelling and reports each package's Dir through the syscall-resolved real
        path once cwd is a symlink -- exactly what common.snapshot()'s
        tempfile.TemporaryDirectory(prefix="praetor-hook-") sits under on macOS's own
        /var -> /private/var. `Path(pkg["Dir"]).relative_to(directory)` then raised for every
        touched package inside a pre-commit/pre-push snapshot (#135 second pass; #282 collapsed
        the Go-side equivalents onto internal/util.ResolveExistingPath).
        """
        with tempfile.TemporaryDirectory(prefix="praetor-package-real-") as temp:
            root = Path(temp)
            (root / "go.mod").write_text("module example.test/hookscope\n\ngo 1.27\n")
            (root / "a.go").write_text("package hookscope\n")
            alias = root.parent / (root.name + "-alias")
            alias.symlink_to(root)
            try:
                self.assertEqual(go_packages(alias, ["a.go"]), ["example.test/hookscope"])
            finally:
                alias.unlink()

    def test_unresolved_relative_to_matches_root_by_identity_and_keeps_the_rest_as_spelled(self):
        """The root is found by identity above it; nothing below it is resolved.

        The alias stands in for macOS's /var -> /private/var and Windows's 8.3 short names: a
        spelling of the root's ancestors that differs from the resolved root while naming the
        same directory, reproducible on every host.
        """
        with tempfile.TemporaryDirectory(prefix="praetor-unresolved-real-") as temp:
            root = Path(temp).resolve()
            (root / "sub").mkdir()
            (root / "sub/link").symlink_to(root)
            alias = root.parent / (root.name + "-alias")
            alias.symlink_to(root)
            self.addCleanup(alias.unlink)
            relative = common.unresolved_relative_to
            self.assertEqual(relative(alias / "sub/file.go", root), Path("sub/file.go"))
            self.assertEqual(relative(root / "sub/file.go", alias), Path("sub/file.go"))
            # A symlink below the root stays visible instead of collapsing onto the root.
            self.assertEqual(relative(alias / "sub/link/file.go", root), Path("sub/link/file.go"))
            self.assertEqual(relative(alias, root), Path("."))
            for outside in (root.parent / "file.go", Path("sub/file.go"), alias / "sub/../file.go"):
                with self.subTest(path=str(outside)), self.assertRaises(ValueError):
                    relative(outside, root)
            with self.assertRaises(ValueError):
                relative(root / "file.go", root / "missing")

    def test_unresolved_relative_to_walks_past_a_component_it_cannot_inspect(self):
        """A component whose stat fails is not the root, and the walk goes on with the next.

        Positive: with an ancestor above the root refusing inspection, the root below it is
        still found. Boundary: a path not written yet is found the same way, its missing
        components never inspected before the root matches. Negative: when the one component
        that names the root is the one refusing, the path is not below it, and the refusal
        is the function's ValueError, never the OSError of the stat (#1219).
        """
        with tempfile.TemporaryDirectory(prefix="praetor-unresolved-denied-") as temp:
            root = Path(temp).resolve() / "repository"
            (root / "sub").mkdir(parents=True)
            alias = root.parent / "alias"
            alias.symlink_to(root)
            real_stat = os.stat

            def refusing(refused):
                def stat(path, *args, **kwargs):
                    if Path(path) == refused:
                        raise PermissionError(13, "planted refusal", os.fspath(path))
                    return real_stat(path, *args, **kwargs)
                return mock.patch.object(common.os, "stat", stat)

            relative = common.unresolved_relative_to
            with refusing(root.parent):
                self.assertEqual(relative(root / "sub/file.go", alias), Path("sub/file.go"))
                self.assertEqual(relative(root / "new/dir/file.go", alias), Path("new/dir/file.go"))
            with refusing(root), self.assertRaisesRegex(ValueError, "is not below"):
                relative(root / "sub/file.go", alias)

    def test_process_failure_and_timeout_are_not_swallowed(self):
        with self.assertRaises(HookError):
            run([sys.executable, "-c", "raise SystemExit(7)"])
        with self.assertRaises(HookError):
            run([sys.executable, "-c", "import time; time.sleep(10)"], timeout=0.01)

    def test_timeout_kills_child_process_group(self):
        """The timeout stops the command and the child it started.

        The parent records when it started the child, before the timeout fires, so the test
        proves the group stop and not a race with interpreter start-up: with a 0.05 s timeout
        the stop signal could reach the group before the child existed, and the child then
        wrote its marker inside the stop grace (#668).
        """
        with tempfile.TemporaryDirectory(prefix="praetor-child-") as temp:
            spawned = Path(temp) / "child-spawned"
            marker = Path(temp) / "child-survived"
            child = f"import time; from pathlib import Path; time.sleep(5); Path({str(marker)!r}).touch()"
            parent = (
                "import subprocess, sys, time; from pathlib import Path; "
                f"subprocess.Popen([sys.executable, '-c', {child!r}]); "
                f"Path({str(spawned)!r}).write_text(repr(time.time())); time.sleep(30)"
            )
            with self.assertRaises(HookError):
                run([sys.executable, "-c", parent], timeout=3)
            self.assertTrue(spawned.exists(), "the parent did not start its child within the timeout")
            started = float(spawned.read_text())
            time.sleep(max(0.0, started + 5.5 - time.time()))
            self.assertFalse(marker.exists())

    def test_timeout_reaps_the_tree_where_no_process_group_exists(self):
        """Windows has no process group, and killing the child alone left its children running.

        The case above proves the POSIX path; this one proves the other branch is reached and
        asks the platform for the whole tree, without needing a Windows host to observe it.
        ``taskkill`` is replaced so the assertion is about what was requested; the direct kill
        underneath it still reaps the real child this test started.
        """
        with (
            mock.patch("common.os", _WithoutProcessGroup()),
            mock.patch("common.subprocess.run") as tree,
            self.assertRaises(HookError),
        ):
            run([sys.executable, "-c", "import time; time.sleep(10)"], timeout=0.05)
        self.assertEqual(tree.call_args.args[0][:3], ["taskkill", "/F", "/T"])

    @unittest.skipUnless(hasattr(os, "killpg"), "Windows has no process group; _kill_bounded "
                         "asks taskkill for the tree there, as the case above proves")
    def test_stop_lets_the_cli_stop_the_commands_in_their_own_groups(self):
        """SIGKILL on the CLI's group ended the CLI and missed its commands.

        The praetor CLI runs each git or go command in a process group of its own, so the old
        SIGKILL left the command running with its lock held. The catchable signal first lets the
        CLI forward it: the command removes its lock, and nothing of it is left running.
        """
        with tempfile.TemporaryDirectory(prefix="praetor-stop-") as temp:
            root = Path(temp)
            process = start_cli(self, root, FORWARDING_CLI, LOCK_HOLDING_COMMAND)
            try:
                common._kill_bounded(process)
                process.wait(timeout=5)
                self.assertTrue((root / "cleaned").exists(), "the command never got the signal")
                self.assertFalse((root / "lock").exists())
            finally:
                self.assertFalse(reap_command_group(root), "the command outlived its CLI")
            self.assertEqual(process.returncode, 128 + signal.SIGTERM)

    @unittest.skipUnless(hasattr(os, "killpg"), "Windows has no process group to signal")
    def test_stop_kills_a_group_that_outlasts_the_grace(self):
        with tempfile.TemporaryDirectory(prefix="praetor-stop-") as temp, \
                mock.patch.object(common, "STOP_GRACE", 0.3):
            process = start_cli(self, Path(temp), STUBBORN_CLI)
            started = time.monotonic()
            common._kill_bounded(process)
            self.assertEqual(process.wait(timeout=5), -signal.SIGKILL)
            self.assertGreaterEqual(time.monotonic() - started, 0.3)

    @unittest.skipUnless(hasattr(os, "killpg"), "Windows has no process group to signal")
    def test_stop_boundaries(self):
        """Process group stop enforces graceful termination, interrupt handling, and SIGKILL."""
        with tempfile.TemporaryDirectory(prefix="praetor-stop-") as temp:
            # A second Ctrl-C during the grace kills the group at once and is not swallowed.
            process = start_cli(self, Path(temp), STUBBORN_CLI)
            with (
                mock.patch.object(common.time, "sleep", side_effect=KeyboardInterrupt),
                self.assertRaises(KeyboardInterrupt),
            ):
                stop_process_group(process, signal.SIGINT)
            self.assertEqual(process.wait(timeout=5), -signal.SIGKILL)
            # A group already gone is not an error.
            stop_process_group(process)
        # Ctrl-C reaches the child as SIGINT, a timeout as SIGTERM.
        with mock.patch("common._kill_bounded", wraps=common._kill_bounded) as stop:
            with self.assertRaises(HookError):
                run([sys.executable, "-c", "import time; time.sleep(10)"], timeout=0.05)
            self.assertEqual(stop.call_args.args[1], signal.SIGTERM)
            real = subprocess.Popen.communicate
            calls = []
            def interrupted(process, *args, **kwargs):
                calls.append(process)
                if len(calls) == 1:
                    raise KeyboardInterrupt
                return real(process, *args, **kwargs)
            with (
                mock.patch.object(subprocess.Popen, "communicate", interrupted),
                self.assertRaises(KeyboardInterrupt),
            ):
                run([sys.executable, "-c", "import time; time.sleep(10)"])
            self.assertEqual(stop.call_args.args[1], signal.SIGINT)

    def test_hook_budget_hands_out_what_is_left_and_refuses_once_spent(self):
        with mock.patch.object(common.time, "monotonic", side_effect=[100.0, 101.0, 109.5, 110.0]):
            budget = common.HookBudget(10)
            self.assertEqual(budget.timeout(2), 2)
            self.assertEqual(budget.timeout(2), 0.5)
            with self.assertRaisesRegex(HookError, "budget spent"):
                budget.timeout(2)

    def test_bounded_stop_takes_at_most_twice_its_grace(self):
        """A native hook reserves 2 * grace for stopping the step that overran its timeout.

        The default grace is STOP_GRACE (10 s) for the group plus 5 s to reap, which alone
        outlasts a 15 s client timeout.
        """
        with tempfile.TemporaryDirectory(prefix="praetor-grace-") as temp:
            started = time.monotonic()
            with self.assertRaisesRegex(HookError, "timed out"):
                common.run_bounded([sys.executable, "-c", STUBBORN_CLI, temp],
                                   timeout=0.5, grace=0.3)
            self.assertLess(time.monotonic() - started, 0.5 + 2 * 0.3 + 1.5)
        with (
            mock.patch("common.os", _WithoutProcessGroup()),
            mock.patch("common.subprocess.run") as tree,
            self.assertRaises(HookError),
        ):
            common.run_bounded([sys.executable, "-c", "import time; time.sleep(10)"],
                               timeout=0.05, grace=0.3)
        self.assertEqual(tree.call_args.kwargs["timeout"], 0.3)

    def test_refuse_after_answers_for_a_blocked_hook_unless_cancelled_or_answered(self):
        scripts = Path(common.__file__).parent
        cases = (("2 if True else None", "time.sleep(30)", 2),
                 ("None", "time.sleep(0.6)", 0),
                 ("2", "timer.cancel(); time.sleep(0.6)", 0))
        for refuse, rest, expected in cases:
            with self.subTest(refuse=refuse, rest=rest):
                script = ("import sys, time\n"
                          f"sys.path.insert(0, {str(scripts)!r})\n"
                          "import common\n"
                          f"timer = common.refuse_after(0.3, lambda: {refuse})\n"
                          f"{rest}\n")
                started = time.monotonic()
                result = subprocess.run([sys.executable, "-B", "-c", script],
                                        capture_output=True, timeout=20, check=False)
                self.assertEqual(result.returncode, expected, result.stderr)
                self.assertLess(time.monotonic() - started, 10)

    def test_sandbox_failure_removes_only_owned_container(self):
        """Sandbox container failures clean up only the owned container instance."""
        calls = []
        def docker(argv, **kwargs):
            calls.append(argv)
            if argv[:2] == ["docker", "run"]:
                raise HookError("simulated container timeout")
            return b"owned-container\n" if argv[:2] == ["docker", "ps"] else b""
        with (
            tempfile.TemporaryDirectory(prefix="praetor-docker-") as temp,
            mock.patch("sandbox.git", return_value=b"a" * 40 + b"\n"),
            mock.patch("sandbox.snapshot", return_value=contextlib.nullcontext(Path(temp))),
            mock.patch("sandbox.run", side_effect=docker),
            self.assertRaisesRegex(HookError, "simulated container timeout"),
        ):
            sandbox.main([])
        start = next(cmd for cmd in calls if cmd[:2] == ["docker", "run"])
        name = start[start.index("--name") + 1]
        self.assertTrue(name.startswith("praetor-gate-"))
        self.assertEqual(calls[-1], ["docker", "container", "rm", "--force", name])
        expected = sandbox.user_arguments()
        after_rm = start[start.index("--rm") + 1:]
        self.assertEqual(after_rm[:len(expected)], expected)

    def test_sandbox_user_mapping_follows_the_host(self):
        posix = mock.Mock(getuid=mock.Mock(return_value=1000), getgid=mock.Mock(return_value=100))
        self.assertEqual(sandbox.user_arguments(posix), ["--user", "1000:100"])
        root = mock.Mock(getuid=mock.Mock(return_value=0), getgid=mock.Mock(return_value=0))
        self.assertEqual(sandbox.user_arguments(root), ["--user", "0:0"])
        self.assertEqual(sandbox.user_arguments(object()), [])


class InstallRefresh(unittest.TestCase):
    """post-merge refreshes a lagging engine install through the engine's own decision (BUG-1004)."""

    def refresh(self, answer, head=b"8d2d46183\n"):
        out = io.StringIO()
        with mock.patch("hooks.run", side_effect=[b"", answer]) as process, \
                mock.patch("hooks.git", return_value=head), \
                contextlib.redirect_stdout(out):
            hooks.refresh_install()
        return process, out.getvalue()

    def test_rebuilt_install_prints_one_line(self):
        process, out = self.refresh(b'{"refreshed":true,"reason":"install was 2 commits behind the checkout"}')
        self.assertEqual(out, "engine install: refreshed; install was 2 commits behind the checkout.\n")
        engine = process.call_args_list[1]
        self.assertEqual(engine.args[0][1:], ["workstation", "install", "--source", os.getcwd(), "--if-stale"])
        self.assertEqual(engine.kwargs["timeout"], hooks.WORKSTATION_REFRESH_TIMEOUT)

    def test_invalid_engine_answer_is_an_error(self):
        for answer in (b"[]", b'{"refreshed":"yes"}', b"{}"):
            with self.subTest(answer=answer), self.assertRaisesRegex(HookError, "invalid result"):
                self.refresh(answer)
        with self.assertRaises(ValueError):
            self.refresh(b"not json")

    def test_skip_prints_nothing(self):
        for answer in (
            b'{"refreshed":false,"reason":"no install manifest at /x; nothing installed to refresh"}',
            b'{"refreshed":false,"reason":"installed commit is not behind the checkout HEAD","commits_behind":0}',
        ):
            with self.subTest(answer=answer):
                _, out = self.refresh(answer)
                self.assertEqual(out, "")

    def test_refused_while_behind_prints_warning(self):
        _, out = self.refresh(
            b'{"refreshed":false,"reason":"checkout has modified tracked files","commits_behind":7}',
            head=b"8d2d46183\n",
        )
        self.assertEqual(
            out,
            "engine install: NOT refreshed, 7 commits behind 8d2d46183: checkout has modified tracked files; "
            "next: clean checkout, run make dev-install\n",
        )

    def test_post_merge_refreshes_context_then_install(self):
        order = []
        with mock.patch("hooks.changed", return_value=["go.mod"]), \
                mock.patch("hooks.refresh", side_effect=lambda names: order.append(("context", names))), \
                mock.patch("hooks.refresh_install", side_effect=lambda: order.append(("install",))):
            hooks.post_stage("post-merge", [])
        self.assertEqual(order, [("context", ["go.mod"]), ("install",)])
        with mock.patch("hooks.changed", return_value=[]), mock.patch("hooks.refresh"), \
                mock.patch("hooks.refresh_install") as install:
            hooks.post_stage("post-checkout", ["a" * 40, "b" * 40, "1"])
            install.assert_not_called()


# What each floored linter prints for --version, verbatim from a release build.
VERSION_REPORTS = {
    "actionlint": b"v1.7.12\ninstalled by building from source\n"
                  b"built with go1.26.4 compiler for linux/amd64\n",
    "shellcheck": b"ShellCheck - shell script analysis tool\nversion: 0.11.0\n"
                  b"license: GNU General Public License, version 3\n",
    "yamllint": b"yamllint 1.38.0\n",
}
# One staged path per declared linter, in the order lint_commands schedules them.
LINTED_PATHS = {"shellcheck": "run.sh", "actionlint": ".github/workflows/ci.yml",
                "hadolint": "Dockerfile", "yamllint": "config.yaml"}
# The linter declared without a floor: no file in the repository names a version of it.
UNFLOORED = "hadolint"


def installed(tool):
    """A PATH lookup that finds every program."""
    return "/usr/bin/" + tool


class ToolFloors(unittest.TestCase):
    """The hook linters are held to the floors declared in tool-floors.txt (#343)."""

    def reports(self, **overrides):
        """A stand-in for toolchain.run that answers --version from VERSION_REPORTS."""
        answers = {**VERSION_REPORTS, **overrides}

        def process(argv, **settings):
            self.assertEqual(argv[1:], ["--version"])
            self.assertEqual(settings["timeout"], toolchain.VERSION_TIMEOUT)
            answer = answers[argv[0]]
            if isinstance(answer, Exception):
                raise answer
            return answer
        return mock.patch("toolchain.run", side_effect=process)

    def staged(self, root, tools):
        for tool in tools:
            path = root / LINTED_PATHS[tool]
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("a: b\n", encoding="utf-8")
        return [LINTED_PATHS[tool] for tool in tools]

    def test_declared_linters_are_exactly_the_ones_the_hook_runs(self):
        floors = toolchain.tool_floors()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            commands = lint_commands(root, self.staged(root, LINTED_PATHS))
        scheduled = [command[0] for command in commands]
        self.assertEqual(scheduled, list(LINTED_PATHS))
        self.assertEqual(sorted(floors), sorted(scheduled))
        floored = {tool: floor for tool, floor in floors.items() if floor is not None}
        # A version reader exists for each floored linter and for no other tool.
        self.assertEqual(sorted(toolchain.VERSION_OUTPUT), sorted(floored))
        self.assertEqual([tool for tool in floors if tool not in floored], [UNFLOORED])
        for tool, floor in floored.items():
            self.assertRegex(floor, r"^\d+\.\d+\.\d+$", tool)

    def test_golangci_lint_uses_lint_module_pin_first(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "tools/lint").mkdir(parents=True)
            (root / "tools/lint/go.mod").write_text("module lint\n")
            (root / "tools/go").mkdir(parents=True)
            (root / "tools/go/go.mod").write_text("module tools\n")
            with mock.patch("checks.run", return_value=b"github.com/golangci/golangci-lint/v2/cmd/golangci-lint\n") as run:
                command = golangci_lint_command(root, ["./..."])
        self.assertEqual(command, ["go", "tool", "-modfile=tools/lint/go.mod",
                                   "golangci-lint", "run", "--allow-serial-runners", "./..."])
        self.assertEqual(run.call_args.args[0], ["go", "list", "-modfile=tools/lint/go.mod", "tool"])

    def test_golangci_lint_uses_go_module_pin_when_lint_module_lacks_tool(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "tools/lint").mkdir(parents=True)
            (root / "tools/lint/go.mod").write_text("module lint\n")
            (root / "tools/go").mkdir(parents=True)
            (root / "tools/go/go.mod").write_text("module tools\n")
            answers = [b"unrelated/tool\n", b"github.com/golangci/golangci-lint/v2/cmd/golangci-lint\n"]
            with mock.patch("checks.run", side_effect=answers):
                command = golangci_lint_command(root, ["./pkg"])
        self.assertEqual(command, ["go", "tool", "-modfile=tools/go/go.mod",
                                   "golangci-lint", "run", "--allow-serial-runners", "./pkg"])

    def test_golangci_lint_without_pin_fails_with_remedy(self):
        """Running golangci-lint without a pinned tool module fails with remedy instructions."""
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with (
                mock.patch("checks.run") as run,
                self.assertRaisesRegex(HookError, r"golangci-lint.*tools/lint/go.mod"),
            ):
                golangci_lint_command(root, ["./..."])
            run.assert_not_called()

    def test_floor_lines_parse_with_comments_and_blanks(self):
        text = "# header\n\nshellcheck>=0.11.0  # trailing\n  yamllint>=1.38\r\nhadolint\n"
        self.assertEqual(toolchain.parse_floors(text),
                         {"shellcheck": "0.11.0", "yamllint": "1.38", "hadolint": None})
        self.assertEqual(toolchain.parse_floors(""), {})
        boundary = "\n" * (toolchain.MAX_FLOOR_LINES - 1) + "shellcheck>=0.11.0"
        self.assertEqual(toolchain.parse_floors(boundary), {"shellcheck": "0.11.0"})

    def test_malformed_floor_lines_are_refused_with_their_line(self):
        cases = {"shellcheck==0.11.0\n": ":1:", "\nshell check\n": ":2:",
                 "shellcheck>=0.11.0\nshellcheck>=0.12.0\n": ":2:",
                 "hadolint\nhadolint>=2.14.0\n": ":2:",
                 "shellcheck >= 0.11.0\n": ":1:", "shellcheck>=v0.11.0\n": ":1:",
                 "shellcheck>=0.11.0rc1\n": ":1:", "shellcheck>=\n": ":1:",
                 "shellcheck>=0.11.0 ; marker\n": ":1:", "shellcheck>=0.11.0 \\\n": ":1:"}
        for text, where in cases.items():
            with self.subTest(text=text), self.assertRaises(HookError) as refused:
                toolchain.parse_floors(text)
            self.assertIn(toolchain.FLOORS_NAME + where, str(refused.exception))
        with self.assertRaisesRegex(HookError, "more than"):
            toolchain.parse_floors("\n" * (toolchain.MAX_FLOOR_LINES + 1))

    def test_one_reader_serves_every_requirement_line(self):
        # The floors file and the hash-locked lint lock are read through the same function.
        lines = {"yamllint>=1.38.0  # pin": ("yamllint", ">=", "1.38.0"),
                 "black==26.5.1 \\": ("black", "==", "26.5.1"),
                 "tomli==2.4.1 ; python_full_version < '3.11' \\": ("tomli", "==", "2.4.1"),
                 "hadolint": ("hadolint", None, None)}
        for line, parsed in lines.items():
            with self.subTest(line=line):
                self.assertEqual(toolchain.requirement(line), parsed)
        for line in ("", "# comment", "    --hash=sha256:0e48b87e", "-r other.txt", "a b",
                     "tool>=", "tool<2.0"):
            with self.subTest(line=line):
                self.assertIsNone(toolchain.requirement(line))

    def test_unreadable_floors_fail_the_hook(self):
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch("toolchain.FLOORS", Path(directory) / "absent.txt"), \
                self.assertRaisesRegex(HookError, "cannot read the tool floors"):
            toolchain.require_floors(["shellcheck"])

    def test_versions_compare_as_numbers(self):
        self.assertFalse(toolchain.below("0.11.0", "0.11.0"))
        self.assertFalse(toolchain.below("1.7", "1.7.0"))
        self.assertFalse(toolchain.below("0.11.0", "0.9.0"))
        self.assertTrue(toolchain.below("0.9.0", "0.11.0"))
        self.assertTrue(toolchain.below("1.7.11", "1.7.12"))
        self.assertTrue(toolchain.below("1.7", "1.7.1"))
        self.assertFalse(toolchain.below("2.0", "1.38.0"))

    def test_each_floored_linter_states_the_version_the_policy_reads(self):
        with self.reports():
            for tool in toolchain.VERSION_OUTPUT:
                with self.subTest(tool=tool):
                    self.assertEqual(toolchain.installed_version(tool),
                                     toolchain.tool_floors()[tool])
        for tool in ("gofmt", UNFLOORED):
            with self.subTest(tool=tool), self.assertRaisesRegex(HookError, "no --version reader"):
                toolchain.installed_version(tool)

    def test_a_build_that_states_no_version_is_refused(self):
        # An untagged actionlint build prints "(devel)" and names the Go release it was
        # built with; reading that as its own version would pass any floor.
        devel = b"(devel)\ninstalled by building from source\nbuilt with go1.26.4 compiler\n"
        with self.reports(actionlint=devel, yamllint=b"yamllint UNKNOWN\n"):
            for tool in ("actionlint", "yamllint"):
                with self.subTest(tool=tool), \
                        self.assertRaisesRegex(HookError, "states no version"):
                    toolchain.installed_version(tool)

    def test_a_linter_below_its_floor_fails_with_the_required_version(self):
        old = {"actionlint": b"v1.7.11\n", "shellcheck": b"version: 0.9.0\n"}
        with self.reports(**old), self.assertRaises(HookError) as refused:
            toolchain.require_floors(list(LINTED_PATHS), which=installed)
        message = str(refused.exception)
        self.assertIn("actionlint >= 1.7.12 is required (.config/lefthook/tool-floors.txt): "
                      "the actionlint on PATH is 1.7.11", message)
        self.assertIn("shellcheck >= 0.11.0 is required", message)
        self.assertIn("the shellcheck on PATH is 0.9.0", message)
        self.assertNotIn("hadolint", message)
        self.assertNotIn("yamllint", message)

    def test_a_linter_at_or_above_its_floor_passes(self):
        newer = {"actionlint": b"v1.8.0\n", "yamllint": b"yamllint 2.0\n"}
        with self.reports(**newer) as process:
            toolchain.require_floors(list(LINTED_PATHS), which=installed)
        # One probe per floored linter; the linter without a floor is not asked its version.
        self.assertEqual(process.call_count, len(VERSION_REPORTS))
        with self.reports() as process:
            toolchain.require_floors([])
        process.assert_not_called()

    def test_a_missing_linter_fails_with_the_required_version(self):
        missing = HookError("yamllint: [Errno 2] No such file or directory: 'yamllint'")
        with self.reports(yamllint=missing), self.assertRaises(HookError) as refused:
            toolchain.require_floors(["yamllint"])
        self.assertIn("yamllint >= 1.38.0 is required", str(refused.exception))
        self.assertIn("No such file or directory", str(refused.exception))
        with self.assertRaisesRegex(HookError, "gofmt: not declared in"):
            toolchain.require_floors(["gofmt"])

    def test_a_linter_without_a_floor_only_has_to_be_installed(self):
        floors = toolchain.tool_floors()
        self.assertIsNone(floors[UNFLOORED])
        with self.reports() as process:
            self.assertIsNone(toolchain.checked_version(UNFLOORED, floors, which=installed))
            toolchain.require_floors([UNFLOORED], which=installed)
            with self.assertRaisesRegex(
                    HookError, r"hadolint is required \(\.config/lefthook/tool-floors\.txt, "
                               r"any version\): it is not on PATH"):
                toolchain.require_floors([UNFLOORED], which=lambda _tool: None)
        # Present is accepted: no version is read, so none can refuse a contributor.
        process.assert_not_called()

    def test_file_checks_settles_the_floor_before_any_linter_runs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            names = self.staged(root, ["shellcheck", "yamllint"])
            with self.reports(shellcheck=b"version: 0.10.0\n") as process, \
                    mock.patch("checks.parallel") as scheduled, \
                    self.assertRaisesRegex(HookError, "shellcheck >= 0.11.0 is required"):
                file_checks(root, names)
            scheduled.assert_not_called()
            self.assertEqual([call.args[0][0] for call in process.call_args_list],
                             ["shellcheck", "yamllint"])
            with self.reports(), mock.patch("checks.parallel") as scheduled:
                file_checks(root, names)
            self.assertEqual([command[0] for command in scheduled.call_args.args[0]],
                             ["shellcheck", "yamllint"])
            # Boundary: no file of a linter's type staged, no probe of that linter.
            (root / "notes.txt").write_text("plain\n", encoding="utf-8")
            with self.reports() as process, mock.patch("checks.parallel"):
                file_checks(root, ["notes.txt"])
            process.assert_not_called()

    def test_installed_linters_state_a_readable_version(self):
        """Replay the readers against the real tools on this host; an absent one is named."""
        absent = []
        for tool in toolchain.VERSION_OUTPUT:
            if shutil.which(tool) is None:
                absent.append(tool)
                continue
            with self.subTest(tool=tool):
                self.assertRegex(toolchain.installed_version(tool), r"^\d+(\.\d+)+$")
        if len(absent) == len(toolchain.VERSION_OUTPUT):
            self.skipTest("none of " + ", ".join(absent) + " is on PATH; the readers are "
                          "replayed against recorded --version output instead")


class HookToolRestores(unittest.TestCase):
    """Missing and outdated linters name a usable restore source, including non-Go tools."""

    def test_restore_missing_module_before_the_pinned_install(self):
        """Absent Go module files need restoration before their pinned install commands."""
        for tool, module, install in (
            ("lefthook", "tools/lefthook/go.mod", "install github.com/evilmartians/lefthook/v2"),
            ("gosec", "tools/go/go.mod", "install -modfile=tools/go/go.mod github.com/securego/gosec/v2/cmd/gosec"),
            ("govulncheck", "tools/go/go.mod", "install -modfile=tools/go/go.mod golang.org/x/vuln/cmd/govulncheck"),
        ):
            with self.subTest(tool=tool), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                message = common.missing_tool_line(tool, root)
                self.assertIn(f"restore {module} from version control (git restore -- {module}) and run:", message)
                self.assertIn(install, message)
                self.assertLess(message.index("git restore --"), message.index(install))
                self.assertIn(f"or install {tool} at the floor in .config/lefthook/tool-floors.txt", message)
                self.assertNotIn("praetorctl adopt", message)
                self.assertNotIn(f"read {root}", message)

    def test_present_modules_keep_the_pinned_install_hints(self):
        """Existing modules retain the direct restore command without an adoption step."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for module in ("tools/lefthook", "tools/go"):
                path = root / module / "go.mod"
                path.parent.mkdir(parents=True)
                path.write_text("module fixture\n", encoding="utf-8")
            quoted_root = common._shell_quote_path(root)
            quoted_module = common._shell_quote_path(root / "tools/lefthook")
            for tool, command in (
                ("lefthook", f"go -C {quoted_module} install github.com/evilmartians/lefthook/v2"),
                ("gosec", f"go -C {quoted_root} install -modfile=tools/go/go.mod github.com/securego/gosec/v2/cmd/gosec"),
                ("govulncheck", f"go -C {quoted_root} install -modfile=tools/go/go.mod golang.org/x/vuln/cmd/govulncheck"),
            ):
                with self.subTest(tool=tool):
                    message = common.missing_tool_line(tool, root)
                    restore = message.split("Restore: ")[1].split("; read-back:")[0]
                    self.assertEqual(restore, command)

    def test_restore_paths_use_the_platform_shell_quotes(self):
        """Windows prints double quotes; POSIX preserves spaces and apostrophes."""
        import shlex

        with tempfile.TemporaryDirectory(prefix="restore path's ") as directory:
            root = Path(directory)
            for module in ("tools/lefthook", "tools/go"):
                path = root / module / "go.mod"
                path.parent.mkdir(parents=True)
                path.write_text("module fixture\n", encoding="utf-8")
            for platform in ("nt", "posix"):
                for tool in ("lefthook", "gosec", "govulncheck", "yamllint"):
                    with (
                        self.subTest(platform=platform, tool=tool),
                        mock.patch("common.os", wraps=os) as platform_os,
                    ):
                        platform_os.name = platform
                        message = common.missing_tool_line(tool, root)
                    restore = message.split("Restore: ")[1].split("; read-back:")[0]
                    path = (
                        sys.executable
                        if tool == "yamllint"
                        else str(root / "tools/lefthook" if tool == "lefthook" else root)
                    )
                    if platform == "nt":
                        self.assertIn('"' + path + '"', restore)
                        self.assertNotIn("'\"'\"'", restore)
                    else:
                        self.assertIn(path, shlex.split(restore))

    def _run_tool_command(self, script, args, env, stdin=None):
        """Execute script with given arguments, environment, and optional stdin input."""
        return subprocess.run(
            [sys.executable, "-B", str(script), *args],
            input=stdin, env=env, capture_output=True, text=True, timeout=15, check=False)

    def test_empty_path_names_every_floor_restore_and_the_lefthook_guard(self):
        """Empty PATH reports missing floor tools with restore remedies and guards execution."""
        floors = toolchain.tool_floors()
        with tempfile.TemporaryDirectory() as directory:
            env = {**os.environ, "PATH": directory}
            result = self._run_tool_command(
                POLICY_SCRIPTS / "toolchain.py", ["--require", ",".join(floors)], env)
            guard = self._run_tool_command(
                ROOT / ".config/agent/hooks/command_guard.py", [], env, stdin="{}")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        restores = {
            "actionlint": f"go install github.com/rhysd/actionlint/cmd/actionlint@v{floors['actionlint']}",
            "yamllint": "-m pip install --require-hashes -r .config/hook-lint/requirements.txt",
            "shellcheck": f"shellcheck v{floors['shellcheck']}",
            "hadolint": "https://github.com/hadolint/hadolint/releases",
        }
        self.assertEqual(set(restores), set(floors))
        for tool, restore in restores.items():
            with self.subTest(tool=tool):
                line = next(line for line in result.stdout.splitlines()
                            if line.startswith(f"{tool}: UNUSABLE:"))
                self.assertIn(
                    f"{tool} is required" if floors[tool] is None
                    else f"{tool} >= {floors[tool]} is required", line)
                self.assertIn("Restore: ", line)
                self.assertIn(restore, line)
                if tool == "shellcheck":
                    self.assertIn("https://github.com/koalaman/shellcheck/releases", line)
        self.assertEqual(guard.returncode, 2, guard.stdout + guard.stderr)
        self.assertIn("lefthook not found on PATH", guard.stderr)
        self.assertIn("Restore: go -C", guard.stderr)
        self.assertIn("install github.com/evilmartians/lefthook/v2", guard.stderr)

    @unittest.skipIf(os.name == "nt", "POSIX executable stub; Windows has no shebang execution")
    def test_actionlint_below_and_at_the_floor_on_a_planted_path(self):
        """Sub-floor actionlint is rejected with a restore command; floor version passes."""
        floor = toolchain.tool_floors()["actionlint"]
        parts = floor.split(".")
        older = ".".join([*parts[:-1], str(int(parts[-1]) - 1)])
        with tempfile.TemporaryDirectory() as directory:
            stub = Path(directory) / "actionlint"
            for version, status in ((older, 1), (floor, 0)):
                with self.subTest(version=version):
                    stub.write_text(f"#!/bin/sh\nprintf 'v{version}\\n'\n", encoding="utf-8")
                    stub.chmod(0o755)
                    result = subprocess.run(
                        [sys.executable, "-B", str(POLICY_SCRIPTS / "toolchain.py"),
                         "--require", "actionlint"],
                        env={**os.environ, "PATH": directory},
                        capture_output=True, text=True, timeout=15, check=False)
                    self.assertEqual(result.returncode, status, result.stdout + result.stderr)
                    line = next(line for line in result.stdout.splitlines()
                                if line.startswith("actionlint:"))
                    if status:
                        self.assertIn(f"the actionlint on PATH is {older}", line)
                        self.assertIn(
                            "Restore: go install github.com/rhysd/actionlint/"
                            f"cmd/actionlint@v{floor}", line)
                    else:
                        self.assertNotIn("Restore:", line)

    def test_restore_versions_follow_the_floor_file(self):
        """Restore commands incorporate exact versions declared in the tool floors file."""
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "tool-floors.txt"
            path.write_text("actionlint>=9.8.7\nshellcheck>=6.5.4\n", encoding="utf-8")
            with (
                mock.patch("toolchain.FLOORS", path),
                mock.patch.dict(os.environ, {"PATH": directory}),
                self.assertRaises(HookError) as refused,
            ):
                toolchain.require_floors(["actionlint", "shellcheck"])
            message = str(refused.exception)
            self.assertIn("Restore: go install github.com/rhysd/actionlint/cmd/actionlint@v9.8.7", message)
            self.assertIn("shellcheck v6.5.4", message)
            self.assertNotIn("@v1.7.12", message)
            self.assertNotIn("shellcheck v0.11.0", message)

    def test_unknown_tool_has_a_named_generic_restore(self):
        """Unknown tool missing on PATH produces a generic restore remedy naming floors file."""
        message = common.missing_tool_line("new-linter")
        self.assertIn("new-linter not found on PATH", message)
        restore = message.split("Restore: ")[1].split("; read-back:")[0]
        self.assertIn("new-linter", restore)
        self.assertIn(toolchain.FLOORS_NAME, restore)
        self.assertNotIn("tools/go/go.mod", restore)

    def test_restore_uses_the_running_python_and_handles_a_new_declared_tool(self):
        """Python restore uses running executable and new declared tools report restore advice."""
        import shlex

        message = common.missing_tool_line("yamllint")
        restore = message.split("Restore: ")[1].split("; read-back:")[0]
        self.assertEqual(shlex.split(restore), [
            sys.executable, "-m", "pip", "install", "--require-hashes",
            "-r", ".config/hook-lint/requirements.txt"])
        with self.assertRaises(HookError) as refused:
            toolchain.checked_version("new-linter", {"new-linter": None}, which=lambda _: None)
        self.assertIn("new-linter is required", str(refused.exception))
        self.assertIn("Restore: read .config/lefthook/tool-floors.txt", str(refused.exception))


@unittest.skipIf(os.name == "nt", POSIX_STUB)
class LauncherWorktree(unittest.TestCase):
    """Exercise virtualenv selection in real linked Git worktrees."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="praetor-launcher-worktree-")
        self.addCleanup(self.temp.cleanup)
        base = Path(self.temp.name)
        self.main = base / "main"
        self.main.mkdir()
        command(self.main, "git", "init", "-q", "-b", "main")
        command(self.main, "git", "config", "user.name", "Fixture")
        command(self.main, "git", "config", "user.email", "fixture@example.test")
        command(self.main, "git", "commit", "-q", "--allow-empty", "-m", "initial")
        self.worktree = base / "linked"
        command(self.main, "git", "worktree", "add", "-q", "-b", "linked", str(self.worktree))
        self.bin = base / "bin"
        self.bin.mkdir()
        (self.bin / "git").symlink_to(shutil.which("git"))

    def launch(self):
        env = dict(os.environ, PATH=str(self.bin))
        return subprocess.run([shutil.which("sh") or "sh", str(ROOT / LAUNCHER), "hook.py"],
                              cwd=self.worktree, env=env, capture_output=True, timeout=60,
                              check=False)

    def venv(self, root):
        return interpreter_stand_in(root / ".venv/bin", "python3", "3.13\\n")

    def test_launcher_uses_main_worktree_venv(self):
        candidate = self.venv(self.main)
        result = self.launch()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.decode().strip(), f"stub:{candidate}:1:hook.py")

    def test_launcher_refuses_tracked_main_worktree_venv(self):
        self.venv(self.main)
        command(self.main, "git", "add", "-f", ".venv/bin/python3")
        command(self.main, "git", "commit", "-q", "-m", "track interpreter")
        candidate = interpreter_stand_in(self.bin, "python3", "3.13\\n")
        result = self.launch()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.decode().strip(), f"stub:{candidate}:1:hook.py")

    def test_launcher_reports_missing_venv_candidates(self):
        result = self.launch()
        self.assertEqual(result.returncode, 127, result.stderr)
        for candidate in (self.worktree / ".venv/bin/python3", self.main / ".venv/bin/python3"):
            self.assertIn(str(candidate).encode(), result.stderr)
        self.assertIn(b"python3, python, py -3", result.stderr)

    def test_launcher_prefers_checkout_venv(self):
        self.venv(self.main)
        candidate = self.venv(self.worktree)
        result = self.launch()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.decode().strip(), f"stub:{candidate}:1:hook.py")


class HookInterpreter(unittest.TestCase):
    """Every hook starts through one launcher, which proves its interpreter first (#339)."""

    def launch(self, *args, path=None, data=b"", **variables):
        env = dict(os.environ, **variables)
        if path is not None:
            env["PATH"] = path
        # The path is relative and the directory is the repository root, as for every hook.
        return subprocess.run([shutil.which("sh") or "sh", LAUNCHER, *args], cwd=ROOT, env=env,
                              input=data, capture_output=True, timeout=60, check=False)

    def test_the_proven_interpreter_runs_the_hook_and_returns_its_status(self):
        result = self.launch("-c", "print(40+2)")
        self.assertEqual((result.returncode, result.stdout.strip()), (0, b"42"), result.stderr)
        # Boundary: the statuses a shell gives for a program it cannot start are a verdict
        # when a hook returns them. The interpreter was proven before the hook ran.
        for status in (1, 7, 49, 126, 127):
            with self.subTest(status=status):
                failed = self.launch("-c", f"raise SystemExit({status})")
                self.assertEqual(failed.returncode, status, failed.stderr)
                self.assertNotIn(b"missing dependency", failed.stderr)

    def test_the_probe_leaves_standard_input_to_the_hook(self):
        # The command guard and the post-rewrite hook read their input from stdin.
        result = self.launch("-c", "import sys; print(sys.stdin.read().upper())", data=b"payload")
        self.assertEqual(result.stdout.strip(), b"PAYLOAD", result.stderr)

    def test_no_variable_selects_the_interpreter(self):
        for variable in ("PRAETOR_PYTHON", "PYTHON"):
            with self.subTest(variable=variable):
                result = self.launch("-c", "raise SystemExit(7)", **{variable: "true"})
                self.assertEqual(result.returncode, 7, result.stdout + result.stderr)
        # The launcher expands its arguments and its own lower-case variables, nothing else.
        launcher = (ROOT / LAUNCHER).read_text(encoding="utf-8")
        self.assertEqual(re.findall(r"\$\{?[A-Z_][A-Z0-9_]*", launcher), [])
        self.assertIsNotNone(re.search(r"\$\{?[A-Z_][A-Z0-9_]*", '"${PRAETOR_PYTHON:-python3}"'))

    def test_git_repository_selection_is_dropped_by_default(self):
        variables = {
            "GIT_DIR": "/fake/git/dir",
            "GIT_WORK_TREE": "/fake/work/tree",
            "GIT_INDEX_FILE": "fake/index/file",
            "GIT_COMMON_DIR": "/fake/common/dir",
            "GIT_OBJECT_DIRECTORY": "/fake/objects",
            "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/fake/alt/objects",
        }
        probe = (
            "import os, json; "
            "print(json.dumps([os.environ.get(k) for k in "
            "('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_COMMON_DIR', "
            "'GIT_OBJECT_DIRECTORY', 'GIT_ALTERNATE_OBJECT_DIRECTORIES')]))"
        )
        result = self.launch("-c", probe, **variables)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), [None, None, None, None, None, None])

    def test_keep_index_preserves_git_index_file_only(self):
        variables = {
            "GIT_DIR": "/fake/git/dir",
            "GIT_WORK_TREE": "/fake/work/tree",
            "GIT_INDEX_FILE": "fake/index/file",
            "GIT_COMMON_DIR": "/fake/common/dir",
            "GIT_OBJECT_DIRECTORY": "/fake/objects",
            "GIT_ALTERNATE_OBJECT_DIRECTORIES": "/fake/alt/objects",
        }
        probe = (
            "import os, json; "
            "print(json.dumps([os.environ.get(k) for k in "
            "('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_COMMON_DIR', "
            "'GIT_OBJECT_DIRECTORY', 'GIT_ALTERNATE_OBJECT_DIRECTORIES')]))"
        )
        result = self.launch("--keep-index", "-c", probe, **variables)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout), [None, None, "fake/index/file", None, None, None])

    def refused(self, result):
        self.assertEqual(result.returncode, 127, result.stdout + result.stderr)
        self.assertEqual(result.stdout, b"")
        self.assertIn(b"praetor hooks: missing dependency: no Python 3.10 or newer interpreter.",
                      result.stderr)
        self.assertIn(b"Tried: python3, python, py -3; none answered the version probe.",
                      result.stderr)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_a_stand_in_that_exits_zero_is_no_interpreter(self):
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temp:
            directory = Path(temp) / "bin"
            for candidate in toolchain.PYTHON_CANDIDATES:
                stand_in(directory, candidate[0], "exit 0\n")
            self.refused(self.launch("-c", "raise SystemExit(0)", path=str(directory)))
            with self.assertRaisesRegex(
                    HookError, r"missing dependency: a Python 3\.10 or newer interpreter\. Tried: "
                               r"python3 \(does not answer the version probe as Python\); "
                               r"python \(does not answer .*\); py -3 \(does not answer"):
                toolchain.python_program(which=only(directory))

    def test_no_candidate_on_path_is_a_missing_dependency(self):
        # Boundary, on every platform: nothing under any candidate name. The launcher needs
        # nothing but its shell to say so.
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as empty:
            self.refused(self.launch("-B", "hook.py", path=empty))
            with self.assertRaisesRegex(HookError, r"python3 \(not on PATH\); python \(not on "
                                                   r"PATH\); py -3 \(not on PATH\)"):
                toolchain.python_program(which=only(empty))

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_the_store_alias_is_skipped_for_the_next_candidate(self):
        # What Windows puts on PATH as python3.exe and python.exe: found by every lookup,
        # and started with arguments it prints its own message and exits nonzero.
        alias = ('echo "Python was not found; run without arguments to install from the '
                 'Microsoft Store" >&2\nexit 49\n')
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temp:
            directory = Path(temp) / "bin"
            for name in ("python3", "python"):
                stand_in(directory, name, alias)
            self.refused(self.launch("-B", "hook.py", path=str(directory)))
            with self.assertRaisesRegex(HookError, r"python3 \(.* exited 49 Python was not found"):
                toolchain.python_program(which=only(directory))
            launcher = interpreter_stand_in(directory, "py", "3.13\\n")
            result = self.launch("-B", "hook.py", path=str(directory))
            self.assertEqual(result.stdout.decode().strip(), f"stub:{launcher}:3:-3 -B hook.py",
                             result.stderr)
            self.assertEqual(toolchain.python_program(which=only(directory)),
                             ([str(launcher), "-3"], "3.13"))

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_candidates_are_tried_in_order_and_held_to_the_floor(self):
        # What python3 and python answer the probe with, and the candidate that then runs
        # the hook. The launcher and toolchain.python_program must agree on every row.
        cases = (("3.14\\n", "3.13\\n", "python3"),
                 ("3.9\\n", "3.10\\n", "python"),
                 ("2.7\\n", "3.12\\r\\n", "python"),
                 ("3.x\\n", "3.\\n", None),
                 ("Python 3.13.1\\n", "4.0\\n", None),
                 ("3.10.1\\n", "\\n", None))
        for first, second, expected in cases:
            with self.subTest(python3=first, python=second), \
                    tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temp:
                directory = Path(temp)
                interpreter_stand_in(directory, "python3", first)
                interpreter_stand_in(directory, "python", second)
                result = self.launch("-B", "hook.py", path=temp)
                if expected is None:
                    self.refused(result)
                    with self.assertRaisesRegex(HookError, "missing dependency"):
                        toolchain.python_program(which=only(directory))
                    continue
                chosen = str(directory / expected)
                self.assertEqual(result.stdout.decode().strip(), f"stub:{chosen}:2:-B hook.py",
                                 result.stderr)
                self.assertEqual(toolchain.python_program(which=only(directory))[0], [chosen])

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_a_path_and_arguments_with_spaces_stay_whole(self):
        with tempfile.TemporaryDirectory(prefix="praetor-interpreter-") as temp:
            program = interpreter_stand_in(Path(temp) / "program files", "python3", "3.13\\n")
            result = self.launch("a b", "c", path=str(program.parent))
            self.assertEqual(result.stdout.decode().strip(), f"stub:{program}:2:a b c",
                             result.stderr)

    def test_the_policy_names_no_interpreter_but_the_launcher(self):
        policy = (ROOT / ".config/lefthook/praetor.yml").read_text(encoding="utf-8")
        script = (ROOT / ".config/lefthook/pre-push/pushed-checks.sh").read_text(encoding="utf-8")
        for name, text in (("praetor.yml", policy), ("pushed-checks.sh", script)):
            lines = [line for line in text.splitlines() if not line.lstrip().startswith("#")]
            body = "\n".join(lines)
            with self.subTest(file=name):
                self.assertIsNone(re.search(r"\bpython(?!\.sh\b)", body), body)
                # One launcher call for each hook script a job or the push script starts.
                self.assertEqual(body.count(LAUNCHER), len(re.findall(r"\.py\b", body)))
                self.assertGreater(body.count(LAUNCHER), 0)
        self.assertEqual(toolchain.LAUNCHER, LAUNCHER)

    def test_the_launcher_and_the_policy_declare_one_candidate_list(self):
        """Launcher script and toolchain policy agree on ordered Python candidates."""
        launcher = (ROOT / LAUNCHER).read_text(encoding="utf-8")
        # The guards sit in the column shfmt -i 2 -ci aligns them to, one space or more away.
        lines = re.findall(r'^if proven (.+); then exec (.+) "\$@"; fi +#$', launcher, re.MULTILINE)
        self.assertEqual([tuple(proved.split()) for proved, _ in lines],
                         list(toolchain.PYTHON_CANDIDATES))
        # A candidate is started exactly as it was proven, and nothing else is started.
        self.assertEqual([proved for proved, _ in lines], [started for _, started in lines])
        self.assertEqual(len(re.findall(r"\bexec\b", launcher)), len(lines) + 1)
        self.assertIn('exec "$venv" "$@"', launcher)
        quoted_probe = re.escape("'" + toolchain.PYTHON_PROBE + "'")
        self.assertRegex(launcher, rf"\nprobe={quoted_probe} +#\n")
        major, minor = toolchain.PYTHON_FLOOR.split(".")
        self.assertEqual(major, "3")
        self.assertRegex(launcher, rf"\nfloor={minor} +#\n")

    def test_a_crlf_checkout_of_the_launcher_runs_the_hook(self):
        # Adoption writes the launcher into repositories whose checkout may convert it
        # (core.autocrlf), and a shell takes a carriage return as part of a line's last word.
        launcher = (ROOT / LAUNCHER).read_text(encoding="utf-8")
        code = [line for line in launcher.splitlines()[1:] if not line.startswith("#")]
        self.assertGreater(len(code), 20)
        self.assertEqual([line for line in code if not line.endswith(" #")], [])
        self.assertNotIn("\r", launcher)
        with tempfile.TemporaryDirectory(prefix="praetor-launcher-") as temp:
            converted = Path(temp) / "python.sh"
            converted.write_bytes(launcher.replace("\n", "\r\n").encode())
            shell = shutil.which("sh") or "sh"
            for status in (0, 7):
                with self.subTest(status=status):
                    result = subprocess.run(
                        [shell, str(converted), "-c", f"print(40+2); raise SystemExit({status})"],
                        input=b"", capture_output=True, timeout=60, check=False)
                    self.assertEqual((result.returncode, result.stdout.strip()), (status, b"42"),
                                     result.stderr)
            if os.name == "nt":
                # A Windows shell may be configured to drop carriage returns itself, so
                # what the layout prevents is shown on the platforms where no shell does.
                return
            # Negative: the same conversion of a launcher without the comment signs stops
            # at its first line.
            bare = Path(temp) / "bare.sh"
            bare.write_bytes(b"#!/bin/sh\r\nset -eu\r\nexit 0\r\n")
            broken = subprocess.run([shell, str(bare)], input=b"", capture_output=True,
                                    timeout=60, check=False)
            self.assertNotEqual(broken.returncode, 0, broken.stdout + broken.stderr)

    def test_self_tests_run_under_the_interpreter_running_the_hook(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(self_test_commands(root),
                             [["lefthook", "validate"],
                              *([sys.executable, "-B", suite] for suite in checks.SELF_TESTS)])
            (root / "scripts").mkdir()
            (root / "scripts/test_praetor_hook.py").write_text("", encoding="utf-8")
            self.assertEqual(self_test_commands(root)[-1],
                             [sys.executable, "-B", "scripts/test_praetor_hook.py"])
            self.assertEqual(len(self_test_commands(root)), len(checks.SELF_TESTS) + 2)

    def test_spelling_self_test_is_gated_on_adopter_codespell_configuration(self):
        """Self-test commands must gate test_spelling.py on adopter's .codespellrc."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            commands = self_test_commands(root)
            self.assertNotIn([sys.executable, "-B", ".config/lefthook/scripts/test_spelling.py"],
                             commands)
            (root / ".codespellrc").write_text("[codespell]\n", encoding="utf-8")
            script = root / ".config/lefthook/scripts/test_spelling.py"
            script.parent.mkdir(parents=True, exist_ok=True)
            script.write_text("", encoding="utf-8")
            self.assertIn([sys.executable, "-B", ".config/lefthook/scripts/test_spelling.py"],
                          self_test_commands(root))

    def test_the_probe_answer_decides_not_the_name_or_the_status(self):
        for output, version in ((b"3.14\n", "3.14"), (b"3.10\r\n", "3.10"), (b"3.100\n", "3.100")):
            with self.subTest(output=output), \
                    mock.patch("toolchain.run", return_value=output) as process:
                self.assertEqual(toolchain.python_proof(["py", "-3"]), version)
                self.assertEqual(process.call_args.args[0],
                                 ["py", "-3", "-c", toolchain.PYTHON_PROBE])
                self.assertEqual(process.call_args.kwargs["timeout"], toolchain.VERSION_TIMEOUT)
        # A zero status with any other answer proves nothing: no output, the answer of -V,
        # a release below the floor, Python 2, and a major version that is not 3.
        refused = {b"": "does not answer the version probe as Python",
                   b"Python 3.13.1\n": "does not answer the version probe as Python",
                   b"3.10.1\n": "does not answer the version probe as Python",
                   b"3.9\n": "is Python 3.9; Python 3.10 or newer is required",
                   b"2.7\n": "is Python 2.7; Python 3.10 or newer is required",
                   b"4.0\n": "is Python 4.0; Python 3.10 or newer is required"}
        for output, reason in refused.items():
            with self.subTest(output=output), mock.patch("toolchain.run", return_value=output), \
                    self.assertRaisesRegex(HookError, re.escape(reason)):
                toolchain.python_proof(["python3"])

    def test_the_interpreter_on_this_host_proves_itself(self):
        """Host Python interpreter resolves successfully and validates version requirements."""
        argv, version = toolchain.python_program()
        self.assertTrue(Path(argv[0]).is_file(), argv)
        self.assertRegex(version, r"^3\.\d+$")
        self.assertEqual(toolchain.python_proof([sys.executable]), f"{sys.version_info[0]}.{sys.version_info[1]}")
        with self.assertRaisesRegex(HookError, "praetor-no-such-interpreter"):
            toolchain.python_proof(["praetor-no-such-interpreter"])

    def check(self, required, states):
        """Run toolchain.check with describe answering from states; return (failed, lines)."""
        def describe(tool, _floors):
            if isinstance(states.get(tool), Exception):
                raise states[tool]
            return states.get(tool, "present")
        out = io.StringIO()
        with mock.patch("toolchain.describe", side_effect=describe), \
                contextlib.redirect_stdout(out):
            failed = toolchain.check(required)
        return failed, out.getvalue().splitlines()

    def test_check_fails_only_the_required_tools_and_reports_every_declared_one(self):
        declared = [*toolchain.RESOLVED, *toolchain.BY_NAME, *toolchain.tool_floors()]
        failed, lines = self.check(["python", "yamllint"], {})
        self.assertEqual(failed, [])
        self.assertEqual(lines, [f"{tool}: present" for tool in declared])
        gone = {"hadolint": HookError("hadolint is required"),
                "yamllint": HookError("yamllint >= 1.38.0 is required")}
        failed, lines = self.check(["python", "yamllint"], gone)
        self.assertEqual(failed, ["yamllint"])
        self.assertIn("hadolint: not asserted: hadolint is required", lines)
        self.assertIn("yamllint: UNUSABLE: yamllint >= 1.38.0 is required", lines)
        # Boundary: nothing required, so nothing fails, and every tool is still reported.
        failed, lines = self.check([], gone)
        self.assertEqual((failed, len(lines)), ([], len(declared)))
        with self.assertRaisesRegex(HookError, "not declared hook tools: docker"):
            self.check(["docker"], {})

    def test_a_program_started_by_name_is_reported_where_path_resolves_it(self):
        self.assertEqual(toolchain.describe("gofmt", {}), shutil.which("gofmt"))
        self.assertEqual(toolchain.on_path("sh", which=installed), "/usr/bin/sh")
        with self.assertRaisesRegex(HookError, "^sh is not on PATH$"):
            toolchain.on_path("sh", which=lambda _program: None)

    def test_codespell_is_described_with_floor_and_refuses_outdated_versions(self):
        """Tool describe reports codespell floor and rejects versions below CODESPELL_FLOOR."""
        with (
            mock.patch("toolchain.on_path", return_value="/usr/bin/codespell"),
            mock.patch("toolchain.stated_version", return_value="2.4.3"),
        ):
            self.assertEqual(toolchain.describe("codespell", {}), "2.4.3 (floor 2.4.3)")
        with (
            mock.patch("toolchain.on_path", return_value="/usr/bin/codespell"),
            mock.patch("toolchain.stated_version", return_value="2.4.1"),
            self.assertRaisesRegex(HookError, "codespell >= 2.4.3 is required"),
        ):
            toolchain.describe("codespell", {})

    def test_check_exits_nonzero_for_a_required_tool_that_is_unusable(self):
        runner = [sys.executable, "-B", str(ROOT / ".config/lefthook/scripts/toolchain.py")]
        with tempfile.TemporaryDirectory(prefix="praetor-toolchain-") as empty:
            env = dict(os.environ, PATH=empty)
            refused = subprocess.run([*runner, "--require", "python,sh"], env=env, cwd=empty,
                                     capture_output=True, timeout=120, check=False)
            self.assertEqual(refused.returncode, 1, refused.stdout + refused.stderr)
            self.assertIn(b"python: UNUSABLE: missing dependency: a Python 3.10 or newer "
                          b"interpreter. Tried: python3 (not on PATH); python (not on PATH); "
                          b"py -3 (not on PATH)", refused.stdout)
            self.assertIn(b"sh: UNUSABLE: sh is not on PATH", refused.stdout)
            self.assertIn(b"required hook tools unusable: python, sh", refused.stderr)
            reported = subprocess.run(runner, env=env, cwd=empty, capture_output=True,
                                      timeout=120, check=False)
            self.assertEqual(reported.returncode, 0, reported.stdout + reported.stderr)
            self.assertIn(b"python: not asserted: missing dependency", reported.stdout)
            self.assertIn(b"make: not asserted: missing dependency: GNU Make", reported.stdout)


class HookMake(unittest.TestCase):
    """The hooks take GNU Make from fixed candidates, each proven by what it states (#341)."""

    GNU = b"GNU Make 4.4.1\nBuilt for x86_64-pc-linux-gnu\n"

    def resolve(self, answers):
        """Resolve make on a PATH holding the candidates of answers, by their --version output."""
        def process(argv, **settings):
            self.assertEqual(argv[1:], ["--version"])
            self.assertEqual(settings["timeout"], toolchain.VERSION_TIMEOUT)
            answer = answers[argv[0].rsplit("/", 1)[1]]
            if isinstance(answer, Exception):
                raise answer
            return answer

        def which(program):
            return "/opt/bin/" + program if program in answers else None
        with mock.patch("toolchain.run", side_effect=process):
            return toolchain.make_program(which=which)

    def test_the_first_candidate_that_states_gnu_make_is_the_make_the_hooks_run(self):
        self.assertEqual([candidate[0] for candidate in toolchain.MAKE_CANDIDATES],
                         ["make", "gmake", "mingw32-make"])
        self.assertEqual(self.resolve({"make": self.GNU}), "/opt/bin/make")
        old = b"GNU Make 3.81\nCopyright (C) 2006  Free Software Foundation, Inc.\n"
        self.assertEqual(self.resolve({"make": old, "gmake": self.GNU}), "/opt/bin/make")
        # Where make is another make, GNU Make is installed as gmake.
        other = HookError("/opt/bin/make --version exited 2\nusage: make [-BeikNnqrSstWwX]")
        self.assertEqual(self.resolve({"make": other, "gmake": self.GNU}), "/opt/bin/gmake")
        self.assertEqual(self.resolve({"mingw32-make": self.GNU}), "/opt/bin/mingw32-make")

    def test_a_program_that_is_no_gnu_make_is_a_missing_dependency(self):
        gone = HookError("/opt/bin/make --version exited 49\nnot found")
        cases = (({}, "make (not on PATH); gmake (not on PATH); mingw32-make (not on PATH)"),
                 ({"make": b""}, "make (does not state a GNU Make version); gmake (not on PATH)"),
                 ({"make": b"bmake 20240108\n", "gmake": b"usage: gmake\n"},
                  "make (does not state a GNU Make version); gmake (does not state"),
                 ({"make": gone}, "make (/opt/bin/make --version exited 49 not found); gmake"))
        for answers, tried in cases:
            with self.subTest(answers=answers), self.assertRaises(HookError) as refused:
                self.resolve(answers)
            message = str(refused.exception)
            self.assertIn("missing dependency: GNU Make", message)
            self.assertIn("hook-cli", message)
            self.assertIn("Tried: " + tried, message)

    @unittest.skipIf(os.name == "nt", POSIX_STUB)
    def test_a_stand_in_on_path_that_exits_zero_is_not_make(self):
        with tempfile.TemporaryDirectory(prefix="praetor-make-") as temp:
            directory = Path(temp)
            stand_in(directory, "make", "exit 0\n")
            with self.assertRaisesRegex(HookError, r"Tried: make \(does not state a GNU Make "
                                                   r"version\); gmake \(not on PATH\)"):
                toolchain.make_program(which=only(directory))
            real = stand_in(directory, "gmake", 'echo "GNU Make 4.4.1"\n')
            self.assertEqual(toolchain.make_program(which=only(directory)), str(real))

    def test_no_variable_selects_make(self):
        resolved = toolchain.make_program()
        with mock.patch.dict(os.environ, {"PRAETOR_MAKE": "true", "MAKE": "true"}):
            self.assertEqual(toolchain.make_program(), resolved)
        # The module reads no environment variable at all.
        source = (POLICY_SCRIPTS / "toolchain.py").read_text(encoding="utf-8")
        for reader in ("import os", "os.environ", "getenv"):
            self.assertNotIn(reader, source)

    def test_every_make_call_of_the_hooks_runs_the_resolved_program(self):
        with mock.patch("hooks.make_program", return_value="proven-make") as resolved, \
                mock.patch("hooks.run", return_value=b'{"refreshed":false}') as process:
            hooks.cli(["state", "audit", "."])
            hooks.refresh(["main.go"])
            hooks.refresh_install()
        built = [call.args[0] for call in process.call_args_list if "hook-cli" in call.args[0]]
        self.assertEqual([argv[0] for argv in built], ["proven-make"] * 3)
        self.assertEqual(resolved.call_count, 3)
        self.assertEqual(built[0][1:], ["--always-make", "--no-print-directory", "-s", "hook-cli"])
        missing = HookError("missing dependency: GNU Make")
        with mock.patch("hooks.make_program", side_effect=missing), \
                mock.patch("hooks.run") as process, \
                self.assertRaisesRegex(HookError, "missing dependency: GNU Make"):
            hooks.cli(["state", "audit", "."])
        process.assert_not_called()

    def test_the_make_on_this_host_is_reported(self):
        argv, version = toolchain.resolved_make()
        self.assertTrue(Path(argv[0]).is_file(), argv)
        self.assertRegex(version, r"^\d+(\.\d+)+$")
        self.assertEqual(toolchain.make_program(), argv[0])
        self.assertEqual(toolchain.describe("make", {}), f"{version} ({argv[0]})")


POLICY_SCRIPTS = ROOT / ".config/lefthook/scripts"
PROGRAM_NAME = re.compile(r"[a-z][a-z0-9_-]*")


def started_programs(source, kinds=(ast.List,)):
    """Return the bare program names source starts: the first element of each command.

    A command is a list literal that opens with a string spelled like a program name. A list
    handed to cli() holds arguments of the repository's own CLI and a list compared with
    another value is data; neither starts a program.
    """
    tree = ast.parse(source)
    data = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Call) and getattr(node.func, "id", "") == "cli":
            data.update(id(argument) for argument in node.args)
        elif isinstance(node, ast.Compare):
            data.update(id(operand) for operand in node.comparators)
    return {node.elts[0].value for node in ast.walk(tree)
            if isinstance(node, kinds) and id(node) not in data and node.elts
            and isinstance(node.elts[0], ast.Constant) and isinstance(node.elts[0].value, str)
            and PROGRAM_NAME.fullmatch(node.elts[0].value)}


class DeclaredPrograms(unittest.TestCase):
    """Every program the command tables start by name is declared in toolchain.py (#341)."""

    def scripts(self):
        return sorted(path for path in POLICY_SCRIPTS.glob("*.py")
                      if not path.name.startswith("test_"))

    def test_the_declared_programs_are_what_the_command_tables_start(self):
        started = set()
        for name in ("checks.py", "hooks.py"):
            started |= started_programs((POLICY_SCRIPTS / name).read_text(encoding="utf-8"))
        linters = set(toolchain.tool_floors())
        # sh starts the jobs themselves, not a command of these two files.
        self.assertEqual(started - linters, set(toolchain.BY_NAME) - {"sh"})
        self.assertLessEqual(linters, started)
        self.assertEqual(len(toolchain.BY_NAME), len(set(toolchain.BY_NAME)))
        self.assertEqual(list(toolchain.RESOLVED), ["python", "make"])
        self.assertEqual(set(toolchain.RESOLVED) & (started | set(toolchain.BY_NAME)), set())

    def test_sh_is_the_one_program_the_jobs_start(self):
        """Lefthook job configurations invoke sh as their execution runner."""
        policy = (ROOT / ".config/lefthook/praetor.yml").read_text(encoding="utf-8")
        runs = re.findall(r"^ +run: (?:>-\n +)?(\S+)", policy, re.MULTILINE)
        runners = re.findall(r"^ +runner: (\S+)$", policy, re.MULTILINE)
        self.assertGreater(len(runs), 10)
        self.assertEqual(len(runners), 1)
        self.assertEqual(set(runs) | set(runners), {"sh"})
        self.assertIn("sh", toolchain.BY_NAME)
        script = (ROOT / ".config/lefthook/pre-push/pushed-checks.sh").read_text(encoding="utf-8")
        self.assertIn(f"\nexec sh {LAUNCHER} ", script)

    def test_a_new_program_is_found_and_data_is_not(self):
        source = ('run(["newtool", "--flag"])\n'
                  'groups = [(["linter"], None)]\n'
                  'cli(["state", "sync"])\n'
                  'full = command[3:4] == ["audit"]\n'
                  'options = ["--exclude", "x"]\n'
                  'names = ["go.mod", "Makefile"]\n'
                  'pair = ("git", "x")\n')
        self.assertEqual(started_programs(source), {"newtool", "linter"})
        self.assertEqual(started_programs(source, (ast.List, ast.Tuple)),
                         {"newtool", "linter", "git"})

    def test_no_hook_script_starts_an_interpreter_or_make_by_name(self):
        resolved = {candidate[0] for candidates in toolchain.RESOLVED.values()
                    for candidate in candidates}
        self.assertEqual(resolved, {"python3", "python", "py", "make", "gmake", "mingw32-make"})
        scripts = self.scripts()
        self.assertGreater(len(scripts), 5)
        for path in scripts:
            source = path.read_text(encoding="utf-8")
            with self.subTest(script=path.name):
                # A command is a list; the candidates are declared once, as tuples, in
                # toolchain.py, and no other script holds one in either form.
                self.assertEqual(started_programs(source) & resolved, set())
                declared = resolved if path.name == "toolchain.py" else set()
                self.assertEqual(started_programs(source, (ast.List, ast.Tuple)) & resolved,
                                 declared)
        calls = sum(path.read_text(encoding="utf-8").count("[make_program(), ")
                    for path in scripts)
        self.assertEqual(calls, 4)


class CommitRange(unittest.TestCase):
    """hooks.py commits, CI's re-run of the commit checks over every pull request commit (#61).

    The fixture installs no hook, as a commit made with its hooks skipped had none run.
    """

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="praetor-commit-range-")
        self.addCleanup(temp.cleanup)
        self.repo = Path(temp.name) / "repo"
        self.repo.mkdir()
        command(self.repo, "git", "init", "-q", "-b", "main")
        command(self.repo, "git", "config", "user.name", "Range Test")
        command(self.repo, "git", "config", "user.email", "range@example.test")
        self.base = self.commit("README.md", "# Fixture\n", "chore: initialize fixture")

    def commit(self, name, data, message):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data.encode())
        command(self.repo, "git", "add", "-f", "--", name)
        command(self.repo, "git", "commit", "-q", "-s", "-m", message)
        return command(self.repo, "git", "rev-parse", "HEAD").stdout.decode().strip()

    def check(self, *args):
        original = Path.cwd()
        try:
            os.chdir(self.repo)
            with contextlib.redirect_stdout(io.StringIO()) as output:
                hooks.main(["commits", *args])
            return output.getvalue()
        finally:
            os.chdir(original)

    def test_clean_commits_pass(self):
        self.commit("docs/guide.md", "# Guide\n", "docs: add a guide")
        self.commit("data.json", '{"ok": true}\n', "feat: add data")
        self.assertIn("carry no private state, whitespace error or conflict marker",
                      self.check(self.base))

    def test_every_commit_is_checked_not_only_the_head(self):
        # The head tree is clean: only the commit in the middle introduced each defect, so a check
        # of the pull request's result alone would pass.
        for name, data, want in (("notes.md", "trailing  \n", "trailing whitespace"),
                                 ("merge.txt", "<<<<<<< HEAD\nours\n", "leftover conflict marker")):
            with self.subTest(defect=want):
                bad = self.commit(name, data, "docs: introduce a defect")
                self.commit(name, "fixed\n", "docs: fix the defect")
                with self.assertRaisesRegex(HookError, f"(?s)commit {bad}.*{want}"):
                    self.check(self.base)
                self.base = self.rev("HEAD")

    def test_private_state_in_any_commit_fails(self):
        self.commit(".workingdir/notes.md", "private\n", "docs: leak private state")
        command(self.repo, "git", "rm", "-q", "-r", "--cached", ".workingdir")
        command(self.repo, "git", "commit", "-q", "-s", "-m", "docs: untrack private state")
        with self.assertRaisesRegex(HookError, "Private .workingdir content must stay untracked"):
            self.check(self.base)

    def test_range_boundaries(self):
        # An empty range passes; a defect in the base is outside the range; the private-state
        # bound refuses a longer range before anything is read; the stage takes one base.
        self.commit("old.md", "trailing  \n", "docs: defect before the range")
        self.base = self.rev("HEAD")
        self.check(self.base)
        self.commit("new.md", "clean\n", "docs: inside the range")
        self.check(self.base)
        self.commit("more.md", "clean\n", "docs: past the bound")
        with mock.patch("privacy.MAX_PRIVATE_COMMITS", 1), \
                self.assertRaisesRegex(HookError, "exceeds 1 commits"):
            self.check(self.base)
        for args in ((), (self.base, "HEAD")):
            with self.subTest(args=args), self.assertRaises(ValueError):
                self.check(*args)
        with self.assertRaises(HookError):
            self.check("f" * 40)

    def rev(self, ref):
        return command(self.repo, "git", "rev-parse", ref).stdout.decode().strip()


if __name__ == "__main__":
    unittest.main(verbosity=2)
