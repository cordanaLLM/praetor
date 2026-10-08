#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Praetor-owned hook sources, generated hook files and every managed asset adoption writes pass
downstream lint policies.

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

The managed asset registry (internal/managedasset) is the second set: every locked file a
family writes, such as the documentation gate's workflow and YAML, the figure engine's
scripts and the API compatibility gate's Go program. Audit locks an adopter's copies to those
bytes, so an adopter whose linters cover the tree cannot fix a finding in them (#842, #845,
#578). They are enumerated by `go run ./internal/managedasset/export`, which writes the
registry's canonical bytes, never by a list kept here, so a registered family is linted the day
it is added. This is the one lint harness for them (HISS-19); internal/managedasset keeps only
the enumeration tests.

The policy is the one such a repository gets without configuring anything, per language:

- Python: black with its defaults and flake8 at 100 columns over every file. Over the
  registry's Python assets also `ruff check` at the rule set of templates/python/ruff.toml.tmpl
  plus the codes of #845 (S, RUF, PERF, ASYNC, C90) at the template's line length on
  Python 3.12, and `ruff format --check`. ruff's formatter reproduces black's, so the one
  formatting policy (black's default width) is checked by both tools; flake8 and E501 are
  limits, not formats. The hook sources are formatted by black alone and keep their
  long-standing scope.
- YAML: yamllint in strict mode with its defaults.
- Go: gofmt, gofumpt and `go vet` on each package directory alone, with no build tag.
- Shell: shellcheck at its defaults.

Each file is linted from a copy in an empty temporary directory and each tool is told to ignore
configuration files, so no project or user configuration can loosen the check.

The Python and YAML tools come from the hash-locked lock and gofumpt from tools/go/go.mod, the
one version source for Go tools:

    python3 -m pip install --require-hashes -r .config/hook-lint/requirements.txt
    GOBIN=<dir> go install -modfile=tools/go/go.mod mvdan.cc/gofumpt

go, gofmt and shellcheck are taken from PATH, since they come with the toolchain and the runner
image. A test skips, naming the reason, when its tool is missing or is not the pinned version,
because another version formats differently. With PRAETOR_HOOK_LINT_BIN set to the
directory holding the pinned tools (CI sets it after installing them), the pinned tools are
taken only from there and a missing or mismatched tool, system tools included, fails the test:
a gate that cannot run is not a passing gate (HISS-21).
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from portability_selftest import hook_toolchain

ROOT = Path(__file__).resolve().parents[1]
# The hook policy's own reader of requirement lines and its one version probe: the lock is
# read and the tools are asked their version here the way the hooks do it (HISS-19).
TOOLCHAIN = hook_toolchain()
LOCK = ROOT / ".config" / "hook-lint" / "requirements.txt"
# The one version source for Go tools (Makefile GO_SECURITY_TOOL, the CI install steps).
GO_TOOLS_MOD = ROOT / "tools" / "go" / "go.mod"
TOOL_DIR_ENV = "PRAETOR_HOOK_LINT_BIN"
# Tools pinned in the hash-locked lock, and the Go tool pinned in tools/go/go.mod. Both are
# looked up in PRAETOR_HOOK_LINT_BIN where it is set, and their version must be the pin.
TOOLS = ("black", "flake8", "yamllint", "ruff")
GO_PINNED_TOOLS = ("gofumpt",)
# Tools that come with the platform or the Go toolchain: taken from PATH, with no pin to
# compare, and required (not skipped) where PRAETOR_HOOK_LINT_BIN is set.
SYSTEM_TOOLS = ("go", "gofmt", "shellcheck")
# Lint names whose executable is not their own name.
EXECUTABLE = {"ruff-check": "ruff", "ruff-format": "ruff", "go-vet": "go"}
# One lint run over a handful of small files; a hung tool must fail, not hang the gate.
LINT_TIMEOUT_SECONDS = 120
# Building and running the export compiles the registry's packages once.
EXPORT_TIMEOUT_SECONDS = 600
MAX_REGISTRY_ASSETS = 4096
FLAKE8_MAX_LINE = 100
YAMLLINT_MAX_LINE = 80
# black's default width, which ruff's formatter reproduces.
BLACK_LINE_LENGTH = 88
# The Python release every managed script must run on (#845).
RUFF_TARGET = "py312"
# The ruff codes of #845 that the Python template does not select.
RUFF_EXTRA_RULES = ("S", "RUF", "PERF", "ASYNC", "C90")
RUFF_TEMPLATE = ROOT / "templates" / "python" / "ruff.toml.tmpl"
# Lints that print the files they would change and exit 0: any output is a finding.
LISTING_LINTS = ("gofmt", "gofumpt")
GO_LINTS = ("gofmt", "gofumpt", "go-vet")
# The package whose output is the managed asset registry, one canonical file per line.
EXPORT_PACKAGE = "./internal/managedasset/export"
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
    ("internal/adopt/engine_launcher.go", "engineLauncherFile", RENDERED),
)
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


def hook_files():
    """Return {adopted path: text} of the hook sources and generated hook files."""
    return {
        path: (ROOT / source).read_text(encoding="utf-8")
        for path, source in emitted_sources().items()
    }


def pinned_versions(text):
    """Return {tool: version} for each tool pinned with == in the lock text."""
    pins = {}
    for line in text.splitlines()[:2000]:
        parsed = TOOLCHAIN.requirement(line)
        if parsed and parsed[1] == "==" and parsed[0].lower() in TOOLS:
            pins[parsed[0].lower()] = parsed[2]
    return pins


GO_TOOL_MODULES = {"gofumpt": "mvdan.cc/gofumpt"}


def go_tool_pins(text):
    """Return {tool: version} for each Go tool in `GO_TOOL_MODULES` that the go.mod text pins."""
    pins = {}
    for tool, module in GO_TOOL_MODULES.items():
        match = re.search(rf"^\s*{re.escape(module)}\s+v(\d+(?:\.\d+)+)", text, re.M)
        if match:
            pins[tool] = match.group(1)
    return pins


def toolchain_pins():
    """Return {tool: pinned version} from the lock and from tools/go/go.mod."""
    pins = pinned_versions(LOCK.read_text(encoding="utf-8"))
    pins.update(go_tool_pins(GO_TOOLS_MOD.read_text(encoding="utf-8")))
    return pins


def ruff_template_policy(text):
    """Return (line length, rule codes) of the Python template's ruff configuration text."""
    width = re.search(r"^line-length\s*=\s*(\d+)", text, re.M)
    select = re.search(r"^select\s*=\s*\[([^\]]*)\]", text, re.M)
    if width is None or select is None:
        raise AssertionError(f"{RUFF_TEMPLATE} no longer sets line-length and [lint] select")
    return int(width.group(1)), re.findall(r'"([A-Z0-9]+)"', select.group(1))


def ruff_policy():
    """Return (line length, comma-joined rule codes) of the one ruff check policy: the
    Python template's rules, so a rule added to the template is held here too, plus the
    codes of #845."""
    width, rules = ruff_template_policy(RUFF_TEMPLATE.read_text(encoding="utf-8"))
    rules = list(rules) + [code for code in RUFF_EXTRA_RULES if code not in rules]
    return width, ",".join(rules)


def go_language_version():
    """Return the `go` directive of the repository's go.mod, major.minor."""
    match = re.search(r"^go (\d+\.\d+)", (ROOT / "go.mod").read_text(encoding="utf-8"), re.M)
    if match is None:
        raise AssertionError("go.mod declares no go version")
    return match.group(1)


def probe_version(executable):
    """Return the first dotted version number `executable --version` prints, or None.

    A tool that is missing, does not start or exits nonzero states no version.
    """
    try:
        return TOOLCHAIN.stated_version([executable, "--version"], VERSION)
    except TOOLCHAIN.HookError:
        return None


def install_hint(name):
    """Return the command that installs the pinned tool `name`."""
    if name in GO_PINNED_TOOLS:
        return "GOBIN=<dir> go install -modfile=tools/go/go.mod " + GO_TOOL_MODULES[name]
    if name in SYSTEM_TOOLS:
        return "your platform's package manager or the Go toolchain"
    lock = LOCK.relative_to(ROOT).as_posix()
    return f"python3 -m pip install --require-hashes -r {lock}"


def resolve_tool(name, pins, environ=None, which=shutil.which, probe=probe_version):
    """Return (executable, problem, required) for one tool.

    `problem` is None when the executable is the pinned version (a system tool has no pin),
    else the reason it is not usable. `required` is true when PRAETOR_HOOK_LINT_BIN names the
    tool directory, which turns an unusable tool into a failure instead of a skip. Pinned
    tools are taken only from that directory; system tools always come from PATH.
    """
    environ = os.environ if environ is None else environ
    tool_dir = environ.get(TOOL_DIR_ENV, "")
    required = bool(tool_dir)
    system = name in SYSTEM_TOOLS
    in_dir = required and not system
    executable = which(name, path=tool_dir) if in_dir else which(name)
    if executable is None:
        where = f"in {TOOL_DIR_ENV}={tool_dir}" if in_dir else "on PATH"
        return None, f"{name} not found {where}; install it with: {install_hint(name)}", required
    if system:
        return executable, None, required
    installed = probe(executable)
    if installed != pins.get(name):
        return None, (f"{name} {pins.get(name)} is pinned but {executable} reports "
                      f"{installed or 'no version'}; install it with: {install_hint(name)}"), required
    return executable, None, required


def run_tool(command, cwd, env=None):
    """Run one lint command in `cwd`; return (exit code, combined output)."""
    result = subprocess.run(command, cwd=cwd, capture_output=True, text=True, env=env,
                            timeout=LINT_TIMEOUT_SECONDS, check=False)
    return result.returncode, result.stdout + result.stderr


def write_tree(work, files):
    """Write {relative path: text} under `work`, byte for byte (no newline translation)."""
    for relative, text in files.items():
        target = work / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(text.encode("utf-8"))


def go_package_dirs(paths):
    """Return the sorted distinct directories of the .go paths."""
    return sorted({str(Path(path).parent.as_posix()) for path in paths if path.endswith(".go")})


class LintCase(unittest.TestCase):
    """Shared tool resolution and hermetic lint invocations."""

    pins = toolchain_pins()

    def tool(self, name):
        executable, problem, required = resolve_tool(EXECUTABLE.get(name, name), self.pins)
        if problem is None:
            return executable
        if required:
            self.fail(problem)
        raise unittest.SkipTest(problem)

    def registry(self):
        """Return {path: text} of every managed asset, from the registry through the export."""
        go, problem, required = resolve_tool("go", self.pins)
        if problem is not None:
            if required:
                self.fail(problem)
            raise unittest.SkipTest(problem)
        return registry_assets(go)

    def commands(self, name, work, paths, width):
        """Return the command lines that lint `paths` under `work` with the lint `name`."""
        executable = self.tool(name)
        empty_config = work / "empty-black-config.toml"
        empty_config.write_text("", encoding="utf-8")
        ruff_width, rules = ruff_policy()
        ruff = [executable, "--isolated", "--no-cache"]
        commands = {
            "black": [[executable, "--check", "--config", str(empty_config), *paths]],
            "flake8": [[executable, "--isolated", f"--max-line-length={FLAKE8_MAX_LINE}", *paths]],
            # -f parsable: on GitHub Actions yamllint switches to its annotation format
            # (rule names in brackets), so an output assertion that passes locally fails
            # in CI; one fixed format keeps the output the same everywhere.
            "yamllint": [[executable, "--strict", "-f", "parsable", "-d", "default", *paths]],
            "ruff-check": [[executable, "check", *ruff[1:], "--line-length", str(ruff_width),
                            "--target-version", RUFF_TARGET, "--select", rules, *paths]],
            "ruff-format": [[executable, "format", *ruff[1:], "--check",
                             "--line-length", str(width), *paths]],
            "gofmt": [[executable, "-l", "."]],
            "gofumpt": [[executable, "-l", "."]],
            "go-vet": [[executable, "vet", f"./{directory}"] for directory in go_package_dirs(paths)],
            "shellcheck": [[executable, *paths]],
        }
        return commands[name]

    def lint(self, name, files, width=BLACK_LINE_LENGTH):
        """Lint {relative path: text} from an empty directory; return (code, output)."""
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp)
            tree = dict(files)
            env = None
            if name in GO_LINTS:
                tree["go.mod"] = f"module managedassetlint\n\ngo {go_language_version()}\n"
                env = {**os.environ, "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""}
            write_tree(work, tree)
            code, output = 0, ""
            for command in self.commands(name, work, list(files), width):
                step_code, step_output = run_tool(command, work, env)
                code = code or step_code
                output += step_output
            if name in LISTING_LINTS and output.strip():
                code = code or 1
            return code, output

    def assertLintPasses(self, name, files, **options):
        code, output = self.lint(name, files, **options)
        self.assertEqual(code, 0, f"{name} rejected {sorted(files)}:\n{output}")

    def assertLintFails(self, name, files, expected, **options):
        code, output = self.lint(name, files, **options)
        self.assertNotEqual(code, 0, f"{name} accepted {sorted(files)}")
        self.assertIn(expected, output)


def registry_assets(go):
    """Return {path: text} of every managed asset, written by `go run EXPORT_PACKAGE`."""
    with tempfile.TemporaryDirectory() as tmp:
        result = subprocess.run([go, "run", EXPORT_PACKAGE, tmp], cwd=ROOT, capture_output=True,
                                text=True, timeout=EXPORT_TIMEOUT_SECONDS, check=False)
        if result.returncode != 0:
            raise AssertionError(f"{EXPORT_PACKAGE} failed ({result.returncode}):\n"
                                 f"{result.stdout}{result.stderr}")
        paths = result.stdout.split()
        if not paths or len(paths) > MAX_REGISTRY_ASSETS:
            raise AssertionError(f"{EXPORT_PACKAGE} listed {len(paths)} assets")
        return {rel: (Path(tmp) / rel).read_bytes().decode("utf-8") for rel in paths}


def only(files, suffixes):
    """Return the entries of {path: text} whose path ends with one of `suffixes`."""
    return {path: text for path, text in files.items() if path.endswith(suffixes)}


class EmittedSourcesTest(LintCase):
    """Positive: every canonical hook source and generated hook file passes."""

    def hooks(self, suffixes):
        files = only(hook_files(), suffixes)
        self.assertTrue(files, f"no {suffixes} hook file is linted")
        return files

    def test_black_accepts_hook_python(self):
        self.assertLintPasses("black", self.hooks((".py",)))

    def test_flake8_accepts_hook_python(self):
        self.assertLintPasses("flake8", self.hooks((".py",)))

    def test_yamllint_accepts_hook_yaml(self):
        self.assertLintPasses("yamllint", self.hooks((".yml", ".yaml")))

    def test_shellcheck_accepts_the_engine_launcher(self):
        self.assertLintPasses("shellcheck", self.hooks(("engine.sh",)))


class ManagedAssetsTest(LintCase):
    """Positive: every managed asset the registry lists passes the linters of its language."""

    def assets(self, suffixes):
        files = only(self.registry(), suffixes)
        self.assertTrue(files, f"the registry lists no {suffixes} asset")
        return files

    def test_black_accepts_managed_python(self):
        self.assertLintPasses("black", self.assets((".py",)))

    def test_flake8_accepts_managed_python(self):
        self.assertLintPasses("flake8", self.assets((".py",)))

    def test_ruff_check_accepts_managed_python(self):
        self.assertLintPasses("ruff-check", self.assets((".py",)))

    def test_ruff_format_accepts_managed_python(self):
        self.assertLintPasses("ruff-format", self.assets((".py",)))

    def test_yamllint_accepts_managed_yaml(self):
        self.assertLintPasses("yamllint", self.assets((".yml", ".yaml")))

    def test_gofmt_accepts_managed_go(self):
        self.assertLintPasses("gofmt", self.assets((".go",)))

    def test_gofumpt_accepts_managed_go(self):
        self.assertLintPasses("gofumpt", self.assets((".go",)))

    def test_go_vet_accepts_managed_go(self):
        self.assertLintPasses("go-vet", self.assets((".go",)))

    def test_shellcheck_accepts_managed_shell(self):
        files = only(self.registry(), (".sh",))
        if files:  # no family ships shell today; a family that does is held to shellcheck
            self.assertLintPasses("shellcheck", files)

    def test_the_registry_reaches_the_named_assets(self):
        files = self.registry()
        for path in ("tools/apicompat/gate/main.go", "tools/figures/mkdocs_hook.py",
                     ".github/workflows/praetor-docs.yml",
                     "draft-skip/.github/workflows/praetor-docs.yml",
                     "draft-skip/.github/workflows/praetor-api.yml",
                     "tools/markdownlint/markdownlint-cli2.yaml"):
            self.assertIn(path, files)


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

    def test_ruff_check_rejects_concatenation_into_a_literal_list(self):
        # The defect #845 reported in tools/figures/mkdocs_hook.py.
        good = "SERVED = [2]\nVALUE = [1, *SERVED]\n"
        bad = "SERVED = [2]\nVALUE = [1] + SERVED\n"
        self.assertLintPasses("ruff-check", {"served.py": good})
        self.assertLintFails("ruff-check", {"served.py": bad}, "RUF005")

    def test_ruff_check_rejects_an_unused_noqa_and_an_unsorted_import(self):
        self.assertLintFails("ruff-check", {"noqa.py": "VALUE = 1  # noqa: ARG001\n"}, "RUF100")
        self.assertLintFails("ruff-check", {"order.py": "import sys\nimport os\n\n"
                                             "print(os.name, sys.argv)\n"}, "I001")

    def test_ruff_check_line_length_comes_from_the_template(self):
        width, _ = ruff_policy()
        self.assertEqual(width, FLAKE8_MAX_LINE)
        self.assertLintPasses("ruff-check", {"boundary.py": self.python_line(width)})
        self.assertLintFails("ruff-check", {"long.py": self.python_line(width + 1)}, "E501")

    def test_ruff_check_covers_the_template_rules_and_the_extra_codes(self):
        _, rules = ruff_policy()
        for code in ("SIM", "N", "T20", *RUFF_EXTRA_RULES):
            self.assertIn(code, rules.split(","))

    def test_ruff_format_rejects_unformatted_source(self):
        self.assertLintPasses("ruff-format", {"formatted.py": 'value = {"a": 1}\n'})
        self.assertLintFails("ruff-format", {"unformatted.py": "value = {  'a':1 }\n"},
                             "would be reformatted")

    def test_ruff_format_keeps_a_magic_trailing_comma_and_joins_what_fits(self):
        wrapped = "value = [\n    1,\n    2,\n]\n"
        self.assertLintPasses("ruff-format", {"wrapped.py": wrapped})
        fits = "value = foo(\n    1\n)\n"
        self.assertLintFails("ruff-format", {"fits.py": fits}, "would be reformatted")

    def test_gofmt_rejects_unformatted_go(self):
        self.assertLintPasses("gofmt", {"ok/main.go": "package main\n\nfunc main() {\n\tprintln(1)\n}\n"})
        self.assertLintFails("gofmt", {"bad/main.go": "package main\n\nfunc main() {\nprintln( 1 )\n}\n"},
                             "bad/main.go")

    def test_gofumpt_rejects_what_gofmt_accepts(self):
        source = ("package main\n\nfunc args() []string {\n\treturn []string{\"a\",\n"
                  "\t\t\"b\"}\n}\n\nfunc main() { _ = args() }\n")
        self.assertLintPasses("gofmt", {"one/main.go": source})
        self.assertLintFails("gofumpt", {"one/main.go": source}, "one/main.go")

    def test_go_vet_runs_per_package_without_a_build_tag(self):
        tagged = "//go:build plantedtag\n\npackage main\n\nfunc main() {}\n"
        self.assertLintFails("go-vet", {"tool/main.go": tagged}, "build constraints")
        broken = "package main\n\nfunc main() { var unused int }\n"
        self.assertLintFails("go-vet", {"tool/main.go": broken}, "tool")

    def test_shellcheck_rejects_an_unquoted_expansion(self):
        self.assertLintPasses("shellcheck", {"run.sh": "#!/bin/sh\necho \"$1\"\n"})
        self.assertLintFails("shellcheck", {"run.sh": "#!/bin/sh\necho $1\n"}, "SC2086")

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
        files = only(hook_files(), ("lefthook.yml",))
        self.assertEqual(sorted(files), ["lefthook.yml"])
        long_job = f"    unfolded:\n      run: echo {'x' * YAMLLINT_MAX_LINE}\n"
        self.assertLintFails("yamllint", {"lefthook.yml": files["lefthook.yml"] + long_job},
                             "line-length")

    def test_shellcheck_rejects_an_unquoted_expansion_in_the_rendered_engine_launcher(self):
        files = only(hook_files(), ("engine.sh",))
        self.assertEqual(sorted(files), [".config/lefthook/engine.sh"])
        path, text = next(iter(files.items()))
        self.assertIn("  pin=$1 #\n", text)
        self.assertLintFails("shellcheck", {path: text.replace("  pin=$1 #\n", "  pin=$1 #\n  ls $pin #\n", 1)},
                             "SC2086")

    def test_flake8_rejects_a_long_line_in_the_rendered_interceptor(self):
        files = only(hook_files(), ("block_evasion.py",))
        self.assertEqual(len(files), 1)
        path, text = next(iter(files.items()))
        long_line = PolicyFixtureTest.python_line(FLAKE8_MAX_LINE + 1)
        self.assertLintFails("flake8", {path: text + long_line}, "E501")

    def test_yamllint_rejects_a_bare_on_key_in_the_documentation_gate_workflow(self):
        path = ".github/workflows/praetor-docs.yml"
        text = self.registry()[path]
        self.assertIn("\n'on':\n", text)
        self.assertLintFails("yamllint", {path: text.replace("\n'on':\n", "\non:\n", 1)},
                             "truthy")

    def test_ruff_check_rejects_the_figure_engine_hook_with_its_old_return(self):
        # Rule 13: the #845 defect itself, planted into the real asset, is refused.
        path = "tools/figures/mkdocs_hook.py"
        text = self.registry()[path]
        fixed = "return [(CSS_URI, CSS_FILE), *served]"
        self.assertIn(fixed, text)
        self.assertLintFails("ruff-check",
                             {path: text.replace(fixed, "return [(CSS_URI, CSS_FILE)] + served")},
                             "RUF005")

    def test_gofmt_rejects_the_gate_program_with_a_plant(self):
        path = "tools/apicompat/gate/main.go"
        text = self.registry()[path]
        code = "package main\n"
        self.assertIn(code, text)
        self.assertLintFails("gofmt", {path: text.replace(code, code + "\nvar _ = [] int{1,2}\n", 1)},
                             path)


class ResolutionTest(unittest.TestCase):
    """Tool resolution: skip locally, fail where the pinned toolchain is required."""

    pins = {"black": "26.5.1", "gofumpt": "0.12.0"}

    def test_pinned_versions_reads_only_tool_pins(self):
        text = "# header\nblack==26.5.1 \\\n    --hash=sha256:ab\nclick==8.5.0 \\\n" \
               "tomli==2.4.1 ; python_full_version < '3.11' \\\n"
        self.assertEqual(pinned_versions(text), {"black": "26.5.1"})
        self.assertEqual(pinned_versions(""), {})

    def test_lock_pins_every_tool(self):
        self.assertEqual(sorted(pinned_versions(LOCK.read_text(encoding="utf-8"))),
                         sorted(TOOLS))

    def test_go_tools_are_pinned_in_the_go_tools_module(self):
        text = "tool (\n\tmvdan.cc/gofumpt\n)\nrequire (\n\tmvdan.cc/gofumpt v0.12.0 // indirect\n)\n"
        self.assertEqual(go_tool_pins(text), {"gofumpt": "0.12.0"})
        self.assertEqual(go_tool_pins("module x\n"), {})
        self.assertEqual(sorted(go_tool_pins(GO_TOOLS_MOD.read_text(encoding="utf-8"))),
                         sorted(GO_PINNED_TOOLS))

    def test_ruff_template_policy_reads_the_template(self):
        text = 'line-length = 99\ntarget-version = "py314"\n\n[lint]\nselect = ["E", "SIM"]\n'
        self.assertEqual(ruff_template_policy(text), (99, ["E", "SIM"]))
        with self.assertRaises(AssertionError):
            ruff_template_policy("[lint]\n")

    def test_ruff_policy_adds_the_issue_codes_once(self):
        _, rules = ruff_policy()
        codes = rules.split(",")
        self.assertEqual(len(codes), len(set(codes)))
        self.assertTrue(set(RUFF_EXTRA_RULES) <= set(codes))

    def test_probe_reads_the_version_a_program_states(self):
        self.assertEqual(probe_version(sys.executable), "%d.%d.%d" % sys.version_info[:3])
        # Negative: a program that is not there, and one that is no program.
        self.assertIsNone(probe_version(str(ROOT / "praetor-no-such-tool")))
        self.assertIsNone(probe_version(str(ROOT)))

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
        for name, pinned in (("black", "26.5.1"), ("gofumpt", "0.12.0")):
            for reported in ("0.0.1", None):
                _, problem, _ = resolve_tool(name, self.pins, environ={},
                                             which=lambda tool: f"/bin/{tool}",
                                             probe=lambda path, value=reported: value)
                self.assertIn(f"{pinned} is pinned", problem)

    def test_system_tools_come_from_path_and_are_required_where_the_directory_is_set(self):
        found = resolve_tool("go", self.pins, environ={TOOL_DIR_ENV: "/t"},
                             which=lambda name, path=None: None if path else "/usr/bin/go",
                             probe=lambda path: self.fail("a system tool has no pin to probe"))
        self.assertEqual(found, ("/usr/bin/go", None, True))
        _, problem, required = resolve_tool("shellcheck", self.pins, environ={TOOL_DIR_ENV: "/t"},
                                            which=lambda name, path=None: None)
        self.assertIn("shellcheck not found on PATH", problem)
        self.assertTrue(required)

    def test_lint_names_map_to_their_executables(self):
        for name in ("ruff-check", "ruff-format"):
            self.assertEqual(EXECUTABLE[name], "ruff")
        self.assertEqual(EXECUTABLE["go-vet"], "go")

    def test_go_package_dirs_are_distinct_and_sorted(self):
        self.assertEqual(go_package_dirs(["b/x.go", "a/y.go", "b/z.go", "a/README.md"]),
                         ["a", "b"])

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
