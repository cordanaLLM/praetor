#!/usr/bin/env python3
"""Behavioral gates in disposable Git repositories; never disable real hooks."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

import common
from common import (HookError, MANAGED_PROCESS_ENV, MAX_PROCESS_ENV_ENTRIES,
                    clean_env, run, snapshot, stop_process_group)
from checks import (go_packages, source_checks, governance_commands, context_changed,
                    audit_scope, local_package_patterns, checkpoint_checks,
                    semgrep_commands, is_fixture, is_chart_template, file_checks,
                    run_full_gate, gate_timeout, FIXTURE_DIRECTORY, GATE_LAUNCH_MARGIN,
                    GATE_QUERY_TIMEOUT, SEMGREP_LANGUAGE_EXTENSIONS, SEMGREP_SUFFIXES)
import hooks
from hooks import push_updates, new_branch_base, pre_push, push_check_mode, prepare_message
from privacy import check_private_history, check_private_index
import sandbox

ROOT = Path(__file__).resolve().parents[3]
RUNNER = Path(".config/lefthook/scripts/hooks.py")
GUARD = ROOT / ".config/agent/hooks/block_evasion.py"
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
        os.killpg(int(raw), signal.SIGKILL)
    except ProcessLookupError:
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
# tracked registration resolves the Git root with $( ), which cmd.exe does not have, so on Windows
# the registration itself cannot run: a stated gap, never a Git Bash sh standing in for cmd.exe.
CODEX_WINDOWS_GAP = ("Codex runs hooks through cmd.exe on Windows, which has no $( ) for the "
                     "registration's Git-root lookup; see docs/guides/agent-hooks.md")
# The policy prints job output and failures only, so a passing job is evidenced by what it
# printed, never by Lefthook's success line naming it. commit-msg and pre-push print this after
# verifying the live ledger.
STATE_VERIFIED = b'PRAETOR_STATE_RESULT={"schema_version":1,"verified":true}'


def cli_path(repo):
    """The fixture CLI as an absolute path.

    CreateProcess resolves a relative program path against the parent's working directory, not
    the cwd it is given, so "bin/praetorctl" ran whatever binary sat under the directory the suite
    was started from -- on CI the real repository's, and inside a pre-commit snapshot none at all.
    """
    return str(repo / PRAETORCTL)


def command(repo, *args, data=None, ok=True, maintain_state=True):
    # Most fixtures model an agent obeying the sync obligation. Negative state
    # tests opt out and execute the same real hooks against stale/missing state.
    if (maintain_state and args[:2] in (("git", "commit"), ("git", "push"))
            and (repo / PRAETORCTL).is_file()):
        command(repo, cli_path(repo), "state", "sync", ".", ok=ok)
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
        self.repo.mkdir()
        command(self.repo, "git", "init", "-q", "-b", "main")
        command(self.repo, "git", "config", "user.name", "Hook Test")
        command(self.repo, "git", "config", "user.email", "hook@example.test")
        for path in (".config/lefthook", ".config/agent/hooks"):
            shutil.copytree(ROOT / path, self.repo / path)
        shutil.copy(ROOT / "lefthook.yml", self.repo / "lefthook.yml")
        # Fixture policy self-tests are data, not another recursive full suite.
        (self.repo / ".config/lefthook/scripts/test_hooks.py").write_text(
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
        files = {".standards.yaml": "repository: {}\n", ".standards.lock": "{}\n",
                 "go.mod": "module fixture\n\ngo 1.25\n", "internal/doc.go": "package internal\n",
                 ".golangci.yml": "version: '2'\n", ".github/workflows/ci.yml": "name: fixture\n",
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
            def check_gate(root):
                run([str(self.binary), "state", "audit", "."], cwd=root)
                return True
            with mock.patch("checks.parallel", side_effect=check_flavor), \
                    mock.patch("checks.run_full_gate", side_effect=check_gate) as gated:
                self.assertTrue(source_checks(directory, [".standards.yaml"], base="HEAD"))
                gated.assert_called_once_with(directory)
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
        self.assertIn(b"checking the full tree", result.stdout + result.stderr)
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
            with mock.patch("privacy.subprocess.run", return_value=subprocess.CompletedProcess([], 2)):
                with self.assertRaisesRegex(HookError, "inspection failed"):
                    check_private_index()
            with mock.patch("privacy.subprocess.run", side_effect=subprocess.TimeoutExpired("git", 60)):
                with self.assertRaisesRegex(HookError, "inspection failed"):
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
                result = command(self.repo, "python3", str(adapter), data=payload, ok=False)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertNotIn(b"policy-test\n", result.stdout)

    def test_codex_adapter_missing_guard_blocks(self):
        adapter = self.repo / ".config/agent/hooks/codex_pre_tool.py"
        (self.repo / ".config/agent/hooks/block_evasion.py").unlink()
        result = command(self.repo, "python3", str(adapter),
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
        if os.name == "nt":
            self.skipTest(CODEX_WINDOWS_GAP)
        nested = self.repo / "nested directory"
        nested.mkdir()
        for command_text, expected in (("git status", 0), ("git commit --no-verify", 2)):
            payload = json.dumps({"hook_event_name": "PreToolUse", "tool_name": "Bash",
                                  "tool_input": {"command": command_text}}).encode()
            result = command(nested, "/bin/sh", "-c", action["command"],
                             data=payload, ok=False)
            self.assertEqual(result.returncode, expected, result.stdout + result.stderr)

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
        self.assertIn(b"checking the full tree", result.stdout + result.stderr)
        self.assertNotEqual(self.hook("pre-push", "origin", data=b"broken\n").returncode, 0)

    def test_push_passes_actual_base_to_snapshot_audit(self):
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
                    mock.patch("hooks.source_checks", return_value=False) as check:
                pre_push("origin")
                self.assertEqual(check.call_args.kwargs, {"base": base})
                self.assertEqual(check.call_args.args[1], ["README.md"])
            with mock.patch.dict(os.environ, AUTOCRLF_ENV), \
                    mock.patch("hooks.sys.stdin", io.StringIO(protocol.replace(base, "f" * 40))), \
                    mock.patch("hooks.source_checks", return_value=False) as check:
                pre_push("origin")
                self.assertIsNone(check.call_args.kwargs["base"])
                self.assertIn("lefthook.yml", check.call_args.args[1])
        finally:
            os.chdir(original)

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
                        mock.patch("hooks.source_checks", side_effect=HookError("strict release rejected")) as full:
                    with self.assertRaisesRegex(HookError, "strict release rejected"):
                        pre_push("origin")
                    full.assert_called_once()
            # File checks are mandatory before either mode's source checks.
            with mock.patch("hooks.sys.stdin", io.StringIO(checkpoint)), \
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
        rewritten = self.hook("post-rewrite", data=f"{base} {tip}\n".encode())
        self.assertEqual(rewritten.returncode, 0, rewritten.stdout + rewritten.stderr)
        self.assertNotEqual(self.hook("post-rewrite", data=b"bad\n").returncode, 0)


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
        # Boundary: the smallest deadline, and the default 180 s bound plus the 300 s allowance,
        # which keeps the hook at exactly the 600 s it used before the deadline was derived.
        self.assertEqual(gate_timeout(b'{"timeout_seconds": 1}'), 1 + GATE_LAUNCH_MARGIN)
        self.assertEqual(gate_timeout(b'{"timeout_seconds": 480}'), 600)

    def test_clean_env_final_bound_is_composable_for_nested_children(self):
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
        with mock.patch.dict(os.environ, excessive, clear=True):
            with self.assertRaisesRegex(HookError, "environment exceeds"):
                clean_env()

    def semgrep_tree(self, root, *files):
        rules = root / ".config" / "semgrep" / "hiss-invariants.yml"
        rules.parent.mkdir(parents=True)
        rules.write_text("rules: []\n", encoding="utf-8")
        for name in files:
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("x\n", encoding="utf-8")
        return ".config/semgrep/hiss-invariants.yml"

    def test_semgrep_rule_change_scans_the_tree_without_the_corpus(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, "internal/a.go", ".config/hiss/testdata/HISS-08/c/positive/gets.c")
            commands = semgrep_commands(root, [rules, "internal/a.go"])
        self.assertEqual(commands, [["semgrep", "scan", "--error", "--config", rules,
                                     "--exclude", FIXTURE_DIRECTORY, "."]])

    def test_semgrep_file_scan_names_source_and_never_a_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            rules = self.semgrep_tree(root, "internal/a.go", "internal/testdata/bad.go", "testdata/bad.py")
            mixed = semgrep_commands(root, ["internal/a.go", "internal/testdata/bad.go", "testdata/bad.py"])
            only_fixtures = semgrep_commands(root, ["internal/testdata/bad.go", "testdata/bad.py"])
        self.assertEqual(mixed, [["semgrep", "scan", "--error", "--config", rules, "internal/a.go"]])
        self.assertEqual(only_fixtures, [])

    def test_semgrep_boundaries_missing_rules_and_lookalike_names(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "internal").mkdir()
            (root / "internal" / "a.go").write_text("x\n", encoding="utf-8")
            self.assertEqual(semgrep_commands(root, [".config/semgrep/hiss-invariants.yml", "internal/a.go"]), [])
        self.assertTrue(is_fixture("testdata/a.go"))
        self.assertTrue(is_fixture("a\\testdata\\b.go"))
        self.assertFalse(is_fixture("internal/testdatafile.go"))
        self.assertFalse(is_fixture("mytestdata/a.go"))

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
        # skills and plugin copies, the six vendor files and the vendor persona directories --
        # triggers compile-context --verify. The real command produces the list, so a target
        # it gains fails here instead of going unverified by the pre-commit hook.
        with tempfile.TemporaryDirectory(prefix="praetor-context-") as temp:
            root = Path(temp)
            persona = "---\nname: demo\ndescription: demo\n---\n\nRun tests.\n"
            for name, text in {"AGENTS.md": "# Demo\n\nRun tests before commit.\n",
                               ".agents/agents/demo.md": persona,
                               ".agents/skills/demo/SKILL.md": persona,
                               ".agents/plugins/praetor/plugin.json": '{"name": "praetor"}\n'}.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text, encoding="utf-8")
            run(["go", "run", "./cmd/standardsctl", "compile-context", "--source",
                 str(root / "AGENTS.md"), "--target-dir", str(root)],
                cwd=ROOT, env=clean_env(), timeout=600)
            written = sorted(path.relative_to(root).as_posix() for path in root.rglob("*") if path.is_file())
        for vendor in (".claude/agents/demo.md", ".codex/agents/demo.md", ".gemini/agents/demo.md",
                       ".github/agents/demo.md", ".agents/plugins/praetor/skills/demo/SKILL.md"):
            self.assertIn(vendor, written)
        self.assertEqual([name for name in written if not context_changed([name])], [])

    def test_context_changed_ignores_paths_compile_context_does_not_touch(self):
        # Negative and boundary: documentation, vendor settings beside the persona directories,
        # workflows and lookalike prefixes do not start compile-context --verify.
        for name in ("README.md", "docs/guides/onboarding.md", ".claude/settings.json",
                     ".codex/config.toml", ".gemini/settings.json", ".github/workflows/ci.yml",
                     ".agentsrc", "docs/.agents/x.md", ".claude/agents.md"):
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
        with mock.patch("checks.parallel") as scheduled:
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
        return subprocess.run(["python3", str(GUARD)], input=data, capture_output=True,
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

    def test_guard_preserves_argv_and_environment_entry_points(self):
        for args in (("git", "status"), ("--environment",)):
            result = subprocess.run(["python3", str(GUARD), *args], input=b"",
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
                        subprocess.TimeoutExpired(["python3", "block_evasion.py"], 10)):
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
            return subprocess.CompletedProcess(["python3", "block_evasion.py"], 0)
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
        self.assertEqual(select.call_args_list, [mock.call(guard.ROOT, "/session/tree/sub"),
                                                 mock.call(guard.ROOT, None)])
        self.assertEqual(seen, [Path("/session/tree")] * 2)

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
                self.assertEqual(calls, [["make", "--no-print-directory", "state-audit"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "deadline", "--json"],
                                        ["go", "run", "./cmd/standardsctl", "gate", "run", "--path=."],
                                        ["go", "run", "./cmd/standardsctl", "gate", "verify", "--path=."]])
            def reject_pin(argv, **kwargs):
                if argv[:5] == ["go", "run", "./cmd/standardsctl", "gate", "verify"]:
                    raise HookError("pin mismatch")
                return process(argv, **kwargs)
            with mock.patch("checks.parallel"), mock.patch("checks.run", side_effect=reject_pin):
                with self.assertRaisesRegex(HookError, "pin mismatch"):
                    source_checks(root, [".standards.yaml"], base=base)

    def test_gate_timeout_reads_the_real_gate_deadline(self):
        # The hook's bound comes from the CLI it runs, so replay the contract through the hook's
        # own invocation: unset, an unusable value, the exact ceiling and one step past it.
        query = ["go", "run", "./cmd/standardsctl", "gate", "deadline", "--json"]
        expected = {"": 480, "soon": 480, "30m": 2100, "31m": 2100}
        for value, seconds in expected.items():
            with self.subTest(value=value):
                environment = dict(clean_env(), PRAETOR_TEST_STAGE_TIMEOUT=value)
                report = run(query, cwd=ROOT, env=environment, timeout=GATE_QUERY_TIMEOUT)
                self.assertEqual(gate_timeout(report), seconds + GATE_LAUNCH_MARGIN)

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

    def test_process_failure_and_timeout_are_not_swallowed(self):
        with self.assertRaises(HookError):
            run(["python3", "-c", "raise SystemExit(7)"])
        with self.assertRaises(HookError):
            run(["python3", "-c", "import time; time.sleep(10)"], timeout=0.01)

    def test_timeout_kills_child_process_group(self):
        with tempfile.TemporaryDirectory(prefix="praetor-child-") as temp:
            marker = Path(temp) / "child-survived"
            child = f"import time; from pathlib import Path; time.sleep(0.2); Path({str(marker)!r}).touch()"
            parent = f"import subprocess,time; subprocess.Popen(['python3','-c',{child!r}]); time.sleep(10)"
            with self.assertRaises(HookError):
                run(["python3", "-c", parent], timeout=0.05)
            time.sleep(0.3)
            self.assertFalse(marker.exists())

    def test_timeout_reaps_the_tree_where_no_process_group_exists(self):
        """Windows has no process group, and killing the child alone left its children running.

        The case above proves the POSIX path; this one proves the other branch is reached and
        asks the platform for the whole tree, without needing a Windows host to observe it.
        ``taskkill`` is replaced so the assertion is about what was requested; the direct kill
        underneath it still reaps the real child this test started.
        """
        with mock.patch("common.os", _WithoutProcessGroup()), \
                mock.patch("common.subprocess.run") as tree:
            with self.assertRaises(HookError):
                run(["python3", "-c", "import time; time.sleep(10)"], timeout=0.05)
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
        with tempfile.TemporaryDirectory(prefix="praetor-stop-") as temp:
            # A second Ctrl-C during the grace kills the group at once and is not swallowed.
            process = start_cli(self, Path(temp), STUBBORN_CLI)
            with mock.patch.object(common.time, "sleep", side_effect=KeyboardInterrupt):
                with self.assertRaises(KeyboardInterrupt):
                    stop_process_group(process, signal.SIGINT)
            self.assertEqual(process.wait(timeout=5), -signal.SIGKILL)
            # A group already gone is not an error.
            stop_process_group(process)
        # Ctrl-C reaches the child as SIGINT, a timeout as SIGTERM.
        with mock.patch("common._kill_bounded", wraps=common._kill_bounded) as stop:
            with self.assertRaises(HookError):
                run(["python3", "-c", "import time; time.sleep(10)"], timeout=0.05)
            self.assertEqual(stop.call_args.args[1], signal.SIGTERM)
            real = subprocess.Popen.communicate
            calls = []
            def interrupted(process, *args, **kwargs):
                calls.append(process)
                if len(calls) == 1:
                    raise KeyboardInterrupt
                return real(process, *args, **kwargs)
            with mock.patch.object(subprocess.Popen, "communicate", interrupted):
                with self.assertRaises(KeyboardInterrupt):
                    run(["python3", "-c", "import time; time.sleep(10)"])
            self.assertEqual(stop.call_args.args[1], signal.SIGINT)

    def test_sandbox_failure_removes_only_owned_container(self):
        calls = []
        def docker(argv, **kwargs):
            calls.append(argv)
            if argv[:2] == ["docker", "run"]:
                raise HookError("simulated container timeout")
            return b"owned-container\n" if argv[:2] == ["docker", "ps"] else b""
        with tempfile.TemporaryDirectory(prefix="praetor-docker-") as temp, \
                mock.patch("sandbox.git", return_value=b"a" * 40 + b"\n"), \
                mock.patch("sandbox.snapshot", return_value=contextlib.nullcontext(Path(temp))), \
                mock.patch("sandbox.run", side_effect=docker):
            with self.assertRaisesRegex(HookError, "simulated container timeout"):
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


if __name__ == "__main__":
    unittest.main(verbosity=2)
