#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Praetor-owned hook sources, generated hook files and documentation gate YAML pass downstream
lint policies.

Adoption copies the checkpoint evaluator and its shared module into an adopted repository
(internal/adopt/checkpoint.go). .config/lefthook/praetor.yml is the vendorable canonical
policy: adoption does not write it, but an adopter can vendor it with those scripts and
extend it from their own lefthook.yml (.config/lefthook/README.md), a setup
internal/adopt/lefthook_identity.go recognises. Adoption also renders two hook files from
templates in internal/adopt/hooks.go: the root lefthook.yml and
.config/agent/hooks/block_evasion.py. Their renderings are committed under
internal/adopt/testdata/emitted at the paths adoption writes them, and
TestEmittedHookFixturesMatchTheRendering keeps those fixtures equal to what adoption writes.
A repository whose own hooks run black, flake8 or yamllint over its whole tree used to fail
on all five files (BUG-782). The paths are read from the Go constants, so a moved file is
followed without editing this list.

The documentation gate's YAML is part of the same set: the hosted workflow text adoption
writes to .github/workflows/praetor-docs.yml (the Workflow constant) and every YAML asset the
gate embeds, read through the constants and go:embed line of tools/markdownlint/assets.go.
Audit locks an adopter's copies to these bytes, so an adopter whose yamllint covers the tree
cannot fix a finding in them.

The policy is the one such a repository gets without configuring anything: black and
yamllint (in strict mode, so warnings fail too) with their built-in defaults, and flake8
with a 100-column limit. Each file is linted from a copy in an empty temporary directory
and each tool is told to ignore configuration files, so no project or user configuration
can loosen the check.

The tools come from a hash-locked lock:

    python3 -m pip install --require-hashes -r .config/hook-lint/requirements.txt

A test skips, naming the reason, when its tool is missing or is not the pinned version,
because another version formats differently. With PRAETOR_HOOK_LINT_BIN set to the
directory holding the pinned tools (CI sets it after installing the lock), the tools are
taken only from there and a missing or mismatched one fails the test: a gate that cannot
run is not a passing gate (HISS-21).
"""

import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / ".config" / "hook-lint" / "requirements.txt"
TOOL_DIR_ENV = "PRAETOR_HOOK_LINT_BIN"
TOOLS = ("black", "flake8", "yamllint")
# One lint run over a handful of small files; a hung tool must fail, not hang the gate.
LINT_TIMEOUT_SECONDS = 120
FLAKE8_MAX_LINE = 100
YAMLLINT_MAX_LINE = 80
# Where the renderings of the generated hook files are committed, at their adopted paths.
RENDERED = "internal/adopt/testdata/emitted"
# The Go constant naming each hook file linted here, the file declaring it, and the directory
# its content is read from: the repository root for a canonical source, RENDERED for a file
# adoption renders from a template.
EMITTED_CONSTANTS = (
    ("internal/adopt/checkpoint.go", "checkpointScript", ""),
    ("internal/adopt/checkpoint.go", "checkpointCommon", ""),
    ("internal/adopt/lefthook_identity.go", "canonicalLefthookPolicy", ""),
    ("internal/adopt/hooks.go", "lefthookFile", RENDERED),
    ("internal/adopt/hooks.go", "evasionHookFile", RENDERED),
)
# The Go file declaring the documentation gate's workflow, its directory and its embedded assets.
DOCUMENTATION_GATE_SOURCE = "tools/markdownlint/assets.go"
VERSION = re.compile(r"(\d+(?:\.\d+)+)")


def go_constant(relative, name):
    """Return the string value of the Go constant `name` declared in `relative`, inside a
    const block or on its own `const` line."""
    source = (ROOT / relative).read_text(encoding="utf-8")
    match = re.search(rf'^\s*(?:const\s+)?{name}\s*=\s*"([^"]+)"', source, re.M)
    if match is None:
        raise AssertionError(f"{relative} no longer declares the string constant {name}")
    return match.group(1)


def emitted_sources():
    """Return {adopted path: repository-relative file holding its content} for each file."""
    sources = {}
    for relative, name, base in EMITTED_CONSTANTS:
        path = go_constant(relative, name)
        sources[path] = f"{base}/{path}" if base else path
    return sources


def documentation_gate_yaml():
    """Return {repository path: emitted text} for the documentation gate's YAML.

    The workflow text is the Workflow raw string constant, the bytes adoption writes; each YAML
    asset named on the go:embed line is read from the gate's directory, which is its source.
    """
    source = (ROOT / DOCUMENTATION_GATE_SOURCE).read_text(encoding="utf-8")
    workflow = re.search(r"^const Workflow = `([^`]*)`$", source, re.M)
    embed = re.search(r"^//go:embed (.+)$", source, re.M)
    if workflow is None or embed is None:
        raise AssertionError(
            f"{DOCUMENTATION_GATE_SOURCE} no longer declares Workflow and a go:embed line")
    directory = go_constant(DOCUMENTATION_GATE_SOURCE, "Directory")
    files = {go_constant(DOCUMENTATION_GATE_SOURCE, "WorkflowFile"): workflow.group(1)}
    for name in embed.group(1).split():
        if name.endswith((".yml", ".yaml")):
            files[f"{directory}/{name}"] = (ROOT / directory / name).read_text(encoding="utf-8")
    return files


def pinned_versions(text):
    """Return {tool: version} for each tool pinned with == in the lock text."""
    pins = {}
    for line in text.splitlines()[:2000]:
        match = re.match(r"^([A-Za-z0-9_.-]+)==([^\s;\\]+)", line)
        if match and match.group(1).lower() in TOOLS:
            pins[match.group(1).lower()] = match.group(2)
    return pins


def probe_version(executable):
    """Return the first dotted version number `executable --version` prints, or None."""
    try:
        result = subprocess.run([executable, "--version"], capture_output=True, text=True,
                                timeout=LINT_TIMEOUT_SECONDS, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return None
    match = VERSION.search(result.stdout + result.stderr)
    return match.group(1) if match else None


def resolve_tool(name, pins, environ=None, which=shutil.which, probe=probe_version):
    """Return (executable, problem, required) for one pinned tool.

    `problem` is None when the executable is the pinned version, else the reason it is not
    usable. `required` is true when PRAETOR_HOOK_LINT_BIN names the tool directory, which
    turns an unusable tool into a failure instead of a skip.
    """
    environ = os.environ if environ is None else environ
    tool_dir = environ.get(TOOL_DIR_ENV, "")
    required = bool(tool_dir)
    executable = which(name, path=tool_dir) if required else which(name)
    install = f"python3 -m pip install --require-hashes -r {LOCK.relative_to(ROOT).as_posix()}"
    if executable is None:
        where = f"in {TOOL_DIR_ENV}={tool_dir}" if required else "on PATH"
        return None, f"{name} not found {where}; install it with: {install}", required
    installed = probe(executable)
    if installed != pins.get(name):
        return None, (f"{name} {pins.get(name)} is pinned but {executable} reports "
                      f"{installed or 'no version'}; install it with: {install}"), required
    return executable, None, required


def run_tool(command, cwd):
    """Run one lint command in `cwd`; return (exit code, combined output)."""
    result = subprocess.run(command, cwd=cwd, capture_output=True, text=True,
                            timeout=LINT_TIMEOUT_SECONDS, check=False)
    return result.returncode, result.stdout + result.stderr


class LintCase(unittest.TestCase):
    """Shared tool resolution and hermetic lint invocations."""

    pins = pinned_versions(LOCK.read_text(encoding="utf-8"))

    def tool(self, name):
        executable, problem, required = resolve_tool(name, self.pins)
        if problem is None:
            return executable
        if required:
            self.fail(problem)
        raise unittest.SkipTest(problem)

    def lint(self, name, files):
        """Lint {relative path: text} from an empty directory; return (code, output)."""
        executable = self.tool(name)
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            for relative, text in files.items():
                target = work / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(text, encoding="utf-8")
            empty_config = work / "empty-black-config.toml"
            empty_config.write_text("", encoding="utf-8")
            paths = list(files)
            commands = {
                "black": [executable, "--check", "--config", str(empty_config), *paths],
                "flake8": [executable, "--isolated", f"--max-line-length={FLAKE8_MAX_LINE}",
                           *paths],
                "yamllint": [executable, "--strict", "-d", "default", *paths],
            }
            return run_tool(commands[name], work)

    def assertLintPasses(self, name, files):
        code, output = self.lint(name, files)
        self.assertEqual(code, 0, f"{name} rejected {sorted(files)}:\n{output}")

    def assertLintFails(self, name, files, expected):
        code, output = self.lint(name, files)
        self.assertNotEqual(code, 0, f"{name} accepted {sorted(files)}")
        self.assertIn(expected, output)


def emitted(suffixes):
    """Return {adopted path: text} of every linted file with one of `suffixes`.

    The set is the hook files named in EMITTED_CONSTANTS and the documentation gate's YAML.
    """
    files = {path: (ROOT / source).read_text(encoding="utf-8")
             for path, source in emitted_sources().items()}
    files.update(documentation_gate_yaml())
    return {path: text for path, text in files.items() if path.endswith(suffixes)}


class EmittedSourcesTest(LintCase):
    """Positive: every canonical hook source, generated hook file and gate YAML passes."""

    def emitted(self, suffixes):
        files = emitted(suffixes)
        self.assertTrue(files, f"no {suffixes} file is linted")
        return files

    def test_black_accepts_emitted_python(self):
        self.assertLintPasses("black", self.emitted((".py",)))

    def test_flake8_accepts_emitted_python(self):
        self.assertLintPasses("flake8", self.emitted((".py",)))

    def test_yamllint_accepts_emitted_yaml(self):
        self.assertLintPasses("yamllint", self.emitted((".yml", ".yaml")))


class PolicyFixtureTest(LintCase):
    """Negative and boundary fixtures: the policy the positive tests apply is really on."""

    @staticmethod
    def python_line(width):
        assignment = "value = "
        return f'{assignment}"{"x" * (width - len(assignment) - 2)}"\n'

    @staticmethod
    def yaml_line(width):
        return f"---\nkey: {'x' * (width - len('key: '))}\n"

    def test_flake8_line_limit(self):
        self.assertEqual(len(self.python_line(FLAKE8_MAX_LINE)), FLAKE8_MAX_LINE + 1)
        self.assertLintPasses("flake8", {"boundary.py": self.python_line(FLAKE8_MAX_LINE)})
        self.assertLintFails("flake8", {"long.py": self.python_line(FLAKE8_MAX_LINE + 1)},
                             "E501")

    def test_black_rejects_unformatted_source(self):
        self.assertLintPasses("black", {"formatted.py": 'value = {"a": 1}\n'})
        self.assertLintFails("black", {"unformatted.py": "value = {  'a':1 }\n"},
                             "would reformat")

    def test_yamllint_line_limit_and_document_start(self):
        width = len(self.yaml_line(YAMLLINT_MAX_LINE).splitlines()[1])
        self.assertEqual(width, YAMLLINT_MAX_LINE)
        self.assertLintPasses("yamllint", {"boundary.yml": self.yaml_line(YAMLLINT_MAX_LINE)})
        self.assertLintFails("yamllint", {"long.yml": self.yaml_line(YAMLLINT_MAX_LINE + 1)},
                             "line-length")
        self.assertLintFails("yamllint", {"nostart.yml": "key: value\n"}, "document-start")

    def test_yamllint_truthy_on_key(self):
        self.assertLintFails("yamllint", {"bare.yml": "---\non:\n  push:\n"}, "truthy")
        self.assertLintPasses("yamllint", {"quoted.yml": "---\n'on':\n  push:\n"})

    def test_yamllint_line_length_directive_scope(self):
        long_line = "key: " + " ".join(["x"] * 40)
        self.assertGreater(len(long_line), YAMLLINT_MAX_LINE)
        exempt = f"---\n# yamllint disable-line rule:line-length\n{long_line}\n"
        self.assertLintPasses("yamllint", {"exempt.yml": exempt})
        next_line = long_line.replace("key", "other")
        self.assertLintFails("yamllint", {"next.yml": f"{exempt}{next_line}\n"},
                             "line-length")


class RenderedTemplateTest(LintCase):
    """Negative: a defect in a generated or locked file fails the gate that passes it clean."""

    def test_yamllint_rejects_a_long_line_in_the_rendered_lefthook_config(self):
        files = emitted(("lefthook.yml",))
        self.assertEqual(sorted(files), ["lefthook.yml"])
        long_job = f"    unfolded:\n      run: echo {'x' * YAMLLINT_MAX_LINE}\n"
        self.assertLintFails("yamllint", {"lefthook.yml": files["lefthook.yml"] + long_job},
                             "line-length")

    def test_flake8_rejects_a_long_line_in_the_rendered_interceptor(self):
        files = emitted(("block_evasion.py",))
        self.assertEqual(len(files), 1)
        path, text = next(iter(files.items()))
        long_line = PolicyFixtureTest.python_line(FLAKE8_MAX_LINE + 1)
        self.assertLintFails("flake8", {path: text + long_line}, "E501")

    def test_yamllint_rejects_a_bare_on_key_in_the_documentation_gate_workflow(self):
        path = ".github/workflows/praetor-docs.yml"
        text = emitted((path,))[path]
        self.assertIn("\n'on':\n", text)
        self.assertLintFails("yamllint", {path: text.replace("\n'on':\n", "\non:\n", 1)},
                             "truthy")


class ResolutionTest(unittest.TestCase):
    """Tool resolution: skip locally, fail where the pinned toolchain is required."""

    pins = {"black": "26.5.1"}

    def test_pinned_versions_reads_only_tool_pins(self):
        text = "# header\nblack==26.5.1 \\\n    --hash=sha256:ab\nclick==8.5.0 \\\n" \
               "tomli==2.4.1 ; python_full_version < '3.11' \\\n"
        self.assertEqual(pinned_versions(text), {"black": "26.5.1"})
        self.assertEqual(pinned_versions(""), {})

    def test_lock_pins_every_tool(self):
        self.assertEqual(sorted(LintCase.pins), sorted(TOOLS))

    def test_matching_version_resolves(self):
        found = resolve_tool("black", self.pins, environ={}, which=lambda name: "/bin/black",
                             probe=lambda path: "26.5.1")
        self.assertEqual(found, ("/bin/black", None, False))

    def test_missing_tool_skips_locally_and_fails_when_required(self):
        _, problem, required = resolve_tool("black", self.pins, environ={},
                                            which=lambda name: None)
        self.assertIn("black not found on PATH", problem)
        self.assertFalse(required)
        _, problem, required = resolve_tool("black", self.pins, environ={TOOL_DIR_ENV: "/t"},
                                            which=lambda name, path=None: None)
        self.assertIn(f"in {TOOL_DIR_ENV}=/t", problem)
        self.assertTrue(required)

    def test_version_mismatch_and_unprobeable_tool_are_unusable(self):
        for reported in ("26.5.0", None):
            _, problem, _ = resolve_tool("black", self.pins, environ={},
                                         which=lambda name: "/bin/black",
                                         probe=lambda path, value=reported: value)
            self.assertIn("26.5.1 is pinned", problem)

    def test_documentation_gate_yaml_comes_from_go_source(self):
        files = documentation_gate_yaml()
        self.assertEqual(sorted(files), [".github/workflows/praetor-docs.yml",
                                         "tools/markdownlint/markdownlint-cli2.yaml"])
        self.assertTrue(files[".github/workflows/praetor-docs.yml"].startswith("---\n"))
        yaml = emitted((".yml", ".yaml"))
        self.assertLessEqual(set(files), set(yaml))
        self.assertIn("lefthook.yml", yaml)

    def test_emitted_sources_come_from_go_constants(self):
        sources = emitted_sources()
        self.assertEqual(len(sources), len(EMITTED_CONSTANTS))
        for source in sources.values():
            self.assertTrue((ROOT / source).is_file(), source)
        self.assertEqual(sources["lefthook.yml"], f"{RENDERED}/lefthook.yml")
        with self.assertRaises(AssertionError):
            go_constant("internal/adopt/checkpoint.go", "noSuchConstant")


if __name__ == "__main__":
    unittest.main(verbosity=2)
