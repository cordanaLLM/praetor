"""Scoped, read-only file and Go checks used by hooks and Make targets."""

import ast
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import re

from common import HookError, clean_env, paths, present_files, resolved_relative_to, run
from privacy import PRIVATE_STATE_ERROR

GO_CONFIG = {"go.mod", "go.sum", "go.work", "go.work.sum", "Makefile",
             ".golangci.yml", ".gosec.json"}
# Every suffix `go build` compiles or links into a package: go/build's fileListForExt, which
# sorts cgo, assembler, Fortran, SWIG and .syso inputs into a Package's file lists.
GO_EXTENSIONS = {".go", ".c", ".cc", ".cpp", ".cxx", ".m", ".h", ".hh", ".hpp", ".hxx",
                 ".f", ".F", ".for", ".f90", ".s", ".S", ".sx", ".swig", ".swigcxx", ".syso"}
# What `compile-context --verify` reads or checks: canonical AGENTS.md and .agents/ (personas,
# skills, plugin copies), the six vendor files (internal/agentcontext/render.go
# vendorTargets) and the vendor persona directories (compiler.CompileAgents).
# test_context_changed_covers_every_compile_context_path runs the real compile-context and
# fails if it writes a path these do not match.
CONTEXT = {"AGENTS.md", "CLAUDE.md", ".windsurfrules",
           ".github/copilot-instructions.md", ".gemini/GEMINI.md", ".codex/rules.md"}
CONTEXT_PREFIXES = (".agents/", ".cursor/rules/", ".claude/agents/", ".codex/agents/",
                    ".gemini/agents/", ".github/agents/")
# The extensions semgrep assigns each language a rule can target, from semgrep's language
# table (semgrep_interfaces/lang.json, "exts", semgrep 1.177.0). Keyed by the names
# .config/semgrep/hiss-invariants.yml uses; test_semgrep_suffixes_cover_every_rule_language
# fails when a rule names a language missing here.
SEMGREP_LANGUAGE_EXTENSIONS = {
    "go": {".go"},
    "python": {".py", ".pyi"},
    "rust": {".rs"},
    "c": {".c", ".h"},
    "cpp": {".cc", ".cpp", ".cxx", ".c++", ".pcc", ".tpp", ".C", ".h", ".hh", ".hpp", ".hxx",
            ".inl", ".ipp"},
    "javascript": {".cjs", ".js", ".jsx", ".mjs"},
    "typescript": {".ts", ".tsx"},
}
SEMGREP_SUFFIXES = frozenset().union(*SEMGREP_LANGUAGE_EXTENSIONS.values())
# `go run` rebuilds the CLI before the gate starts its own clock. The gate enforces its run
# deadline itself; this margin only keeps the hook from killing it before it can report
# which deadline fired.
GATE_LAUNCH_MARGIN = 120
# Bounds `gate deadline --json`: a build of the CLI and one environment read.
GATE_QUERY_TIMEOUT = 300


def context_changed(names):
    return any(name in CONTEXT or name.startswith(CONTEXT_PREFIXES) for name in names)


def parallel(commands, directory):
    """Bound concurrency and collect every error; no result is silently dropped."""
    failures = []
    with ThreadPoolExecutor(max_workers=3) as pool:
        jobs = [(cmd, pool.submit(run, cmd, cwd=directory, env=clean_env(), timeout=600))
                for cmd in commands]
        for cmd, future in jobs:
            try:
                output = future.result()
                if output:
                    print(output.decode(errors="replace"), end="")
            except HookError as error:
                failures.append(str(error))
    if failures:
        raise HookError("\n".join(failures))


def text_checks(directory, names):
    for name in names:
        path = directory / name
        raw = path.read_bytes()
        if b"\0" in raw:
            continue
        text = raw.decode("utf-8", errors="replace")
        if re.search(r"(?m)^(<{7} |={7}$|>{7} )", text):
            raise HookError(f"{name}: unresolved merge marker")
        if path.suffix == ".py":
            try:
                ast.parse(raw, filename=name)
            except (SyntaxError, UnicodeError) as error:
                raise HookError(str(error)) from error
        if path.suffix == ".json":
            try:
                json.loads(raw)
            except (ValueError, UnicodeError) as error:
                raise HookError(f"{name}: {error}") from error


# Go's own convention: a directory with this name holds inputs, never source. is_fixture
# applies it to a file list and semgrep_commands applies it to a whole-tree scan, so both
# forms of one scan agree on what the corpus is.
FIXTURE_DIRECTORY = "testdata"


def is_fixture(name):
    """Report whether a path is fixture input rather than source this repository owns."""
    slashed = name.replace("\\", "/")
    return slashed.startswith(FIXTURE_DIRECTORY + "/") or f"/{FIXTURE_DIRECTORY}/" in slashed


# A Helm chart renders YAML; its templates are not YAML. "{{- if }}" is a syntax
# error to every YAML parser, so a template can never pass yamllint, while the
# chart's own Chart.yaml and values.yaml are ordinary documents and stay in scope.
# Helm renders templates/ recursively, so a template sits at any depth below it;
# matching only a direct child would block every commit staging the first
# template someone files under templates/rbac/ or templates/tests/.
CHART_MANIFEST = "Chart.yaml"
CHART_TEMPLATE_DIRECTORY = "templates"


def is_chart_template(directory, name):
    """Report whether a path is a Helm chart template rather than a YAML document."""
    parts = Path(name.replace("\\", "/")).parts
    # The last part is the file name; a chart root is whatever directory holds
    # Chart.yaml next to the templates/ directory the file sits under.
    for index in range(len(parts) - 1):
        if parts[index] != CHART_TEMPLATE_DIRECTORY:
            continue
        if (directory / Path(*parts[:index]) / CHART_MANIFEST).is_file():
            return True
    return False


def file_checks(directory, names):
    files = present_files(directory, names)
    if any(name == ".workingdir" or name.startswith(".workingdir/") for name in files):
        raise HookError(PRIVATE_STATE_ERROR)
    text_checks(directory, files)
    # testdata holds inputs to the rules, not source governed by them: a HISS-10 fixture is
    # deliberately unformatted because that is what it demonstrates, and a semgrep fixture
    # deliberately violates an invariant. Go itself never builds testdata either.
    gofiles = [name for name in files if name.endswith(".go") and not is_fixture(name)]
    if gofiles:
        output = run(["gofmt", "-l", *gofiles], cwd=directory)
        if output:
            raise HookError("Run gofmt and stage the intended changes:\n" + output.decode())
    commands = []
    groups = [(["shellcheck"], lambda p: p.endswith(".sh")),
              (["actionlint"], lambda p: p.startswith(".github/workflows/")
               and p.endswith((".yml", ".yaml"))),
              (["hadolint"], lambda p: Path(p).name == "Dockerfile")]
    for command, predicate in groups:
        matches = [name for name in files if predicate(name)]
        if matches:
            commands.append([*command, *matches])
    yaml = [name for name in files
            if name.endswith((".yml", ".yaml")) and not is_chart_template(directory, name)]
    if yaml:
        commands.append(["yamllint", "--strict", "-d", "{extends: relaxed, rules: {line-length: disable}}", *yaml])
    if any(name in {"lefthook.yml", ".codex/hooks.json", ".claude/settings.json",
                    ".gemini/settings.json", "scripts/test_checkpoint_hooks.py"}
           or name.startswith((".config/lefthook/", ".config/agent/")) for name in files):
        commands.append(["lefthook", "validate"])
        commands.append(["python3", "-B", ".config/lefthook/scripts/test_hooks.py"])
        commands.append(["python3", "-B", ".config/lefthook/scripts/test_security_scope.py"])
        commands.append(["python3", "-B", ".config/lefthook/scripts/test_checkpoint.py"])
        if (directory / "scripts/test_checkpoint_hooks.py").exists():
            commands.append(["python3", "-B", "scripts/test_checkpoint_hooks.py"])
    if context_changed(names):
        commands.append(["go", "run", "./cmd/standardsctl", "compile-context", "--verify"])
    parallel(commands, directory)


def decode_packages(raw):
    text = raw.decode()
    decoder = json.JSONDecoder()
    packages = []
    offset = 0
    for _ in range(len(text) + 1):
        if offset >= len(text):
            return packages
        item, offset = decoder.raw_decode(text, offset)
        packages.append(item)
        while offset < len(text) and text[offset].isspace():
            offset += 1
    raise HookError("Go package listing exceeded its input bound")


def package_relative(path, directory):
    """Return a Go package directory relative to the snapshot as a slash path.

    A directory outside the snapshot is a HookError naming it, never an uncaught ValueError
    traceback. go_packages and local_package_patterns both confine `go list` output this way.
    """
    try:
        return resolved_relative_to(path, directory).as_posix()
    except ValueError as error:
        raise HookError(f"Go package directory is outside the checked snapshot: {path}") from error


def go_packages(directory, names, reverse=False):
    if not (directory / "go.mod").exists():
        return []
    relevant = [name for name in names if Path(name).suffix in GO_EXTENSIONS
                or "/testdata/" in name]
    full = any(name in GO_CONFIG or name.startswith(".config/semgrep/") for name in names)
    if not names:
        return []
    # Embedded inputs can be markdown or any other suffix. Inspect Go's actual
    # EmbedFiles metadata before concluding that documentation has no consumers.
    packages = decode_packages(run(["go", "list", "-json", "./..."], cwd=directory,
                                   env=clean_env()))
    selected = set()
    for pkg in packages:
        relative = package_relative(pkg["Dir"], directory)
        prefix = "" if relative == "." else relative + "/"
        embedded = [prefix + item for field in ("EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles")
                    for item in pkg.get(field, [])]
        patterns = any(pkg.get(field) for field in
                       ("EmbedPatterns", "TestEmbedPatterns", "XTestEmbedPatterns"))
        deleted_input = patterns and any(name.startswith(prefix) and not (directory / name).exists()
                                         for name in names)
        if full or any(Path(name).parent.as_posix() == relative for name in relevant) \
                or any(name.startswith(prefix + "testdata/") for name in relevant) \
                or set(embedded).intersection(names) or deleted_input:
            selected.add(pkg["ImportPath"])
    if reverse:
        for _ in range(len(packages)):
            previous = len(selected)
            for pkg in packages:
                imports = pkg.get("Imports", []) + pkg.get("TestImports", []) + pkg.get("XTestImports", [])
                if selected.intersection(imports):
                    selected.add(pkg["ImportPath"])
            if len(selected) == previous:
                break
    return sorted(selected)


def local_package_patterns(directory, packages):
    """Resolve selected import paths to confined directories for filesystem-based tools."""
    listed = decode_packages(run(["go", "list", "-json", *packages], cwd=directory,
                                 env=clean_env()))
    patterns = {}
    for package in listed:
        path = Path(package["Dir"])
        relative = package_relative(path, directory)
        if not path.is_dir():
            raise HookError(f"Go package directory does not exist: {path}")
        patterns[package["ImportPath"]] = "." if relative == "." else "./" + relative
    if set(patterns) != set(packages):
        raise HookError("Go package directory listing does not match the selected import paths")
    return [patterns[package] for package in packages]


def checkpoint_checks(directory, names):
    """Build and race-test affected packages for an explicitly named WIP destination."""
    packages = go_packages(directory, names, reverse=True)
    if not packages:
        print("Checkpoint Go gates: no affected packages")
        return
    print("Checkpoint Go scope: " + ", ".join(packages))
    listed = decode_packages(run(["go", "list", "-json", *packages], cwd=directory,
                                 env=clean_env()))
    buildable = [pkg["ImportPath"] for pkg in listed if pkg.get("GoFiles") or pkg.get("CgoFiles")]
    if buildable:
        parallel([["go", "build", *buildable]], directory)
    # go build rejects explicit test-only packages. Keep those in the race gate,
    # which compiles and executes their tests rather than silently dropping them.
    parallel([["go", "test", "-race", "-count=1", "-timeout=5m", *packages]], directory)


def source_checks(directory, names, gate="all", base=None):
    """Verify an exported snapshot; governance bootstraps only its absent private ledger."""
    packages = go_packages(directory, names, reverse=True)
    governance = governance_commands(directory, names, bool(packages), base=base)
    full_gate = gate == "all" and any(cmd[3:4] == ["audit"] for cmd in governance)
    if full_gate:
        # The Make target initializes only absent state, then audits it strictly.
        # Finish before parallel flavor checks and the later receipt pipeline.
        run(["make", "--no-print-directory", "state-audit"], cwd=directory, env=clean_env())
    if gate == "all":
        parallel(governance, directory)
        parallel(semgrep_commands(directory, names), directory)
    if not packages:
        print("Go gates: no affected packages")
        return run_full_gate(directory) if full_gate else False
    print("Go scope: " + ", ".join(packages))
    local = local_package_patterns(directory, packages) if gate in {"all", "lint", "sec"} else []
    commands = {"test": ["go", "test", "-race", "-count=1", "-timeout=5m", *packages],
                "lint": ["go", "run", "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest",
                         "run", *local],
                "sec": ["gosec", "-conf", ".gosec.json", *local],
                "vuln": ["govulncheck", *packages]}
    selected = list(commands.values()) if gate == "all" else [commands[gate]]
    if full_gate:
        # gate run already enforces full race tests, gosec and vulnerability checks.
        selected = [commands["lint"]]
    parallel(selected, directory)
    return run_full_gate(directory) if full_gate else False


def gate_timeout(report):
    """Bound the gate subprocess by the gate's own resolved run deadline plus the launch margin.

    ``report`` is the output of ``gate deadline --json``. The gate resolves
    PRAETOR_TEST_STAGE_TIMEOUT, clamps it and adds its allowance for the other stages
    itself; the hook reuses that value instead of parsing the variable a second time, so the
    two cannot drift. A fixed 600 s here killed the gate below its documented ceiling (#314).
    """
    try:
        seconds = json.loads(report)["timeout_seconds"]
    except (ValueError, KeyError, TypeError) as error:
        raise HookError(f"gate deadline: no usable run deadline in {report!r}: {error}") from error
    if isinstance(seconds, bool) or not isinstance(seconds, int) or seconds <= 0:
        raise HookError(f"gate deadline: run deadline must be a positive number of seconds, got {seconds!r}")
    return seconds + GATE_LAUNCH_MARGIN


def run_full_gate(directory):
    command = ["go", "run", "./cmd/standardsctl", "gate"]
    report = run([*command, "deadline", "--json"], cwd=directory, env=clean_env(),
                 timeout=GATE_QUERY_TIMEOUT)
    timeout = gate_timeout(report)
    for subcommand in ("run", "verify"):
        run([*command, subcommand, "--path=."], cwd=directory, env=clean_env(),
            timeout=timeout, capture=False)
    return True


def semgrep_commands(directory, names):
    rules = ".config/semgrep/hiss-invariants.yml"
    # The HISS-20 corpus is input to the rules, not source governed by them: its positive
    # fixtures exist precisely because they violate an invariant, so scanning them blocks
    # every push that touches the corpus. The HISS scanner skips it for the same reason.
    source = [name for name in present_files(directory, names)
              if Path(name).suffix in SEMGREP_SUFFIXES and not is_fixture(name)]
    if any(name.startswith(".config/semgrep/") for name in names):
        # A changed rule is judged against the whole tree, and the corpus stays out of that
        # scan too. Naming "." alone dropped the filter above, so every push whose range
        # touched the rules failed on the fixtures written to violate them.
        source = ["--exclude", FIXTURE_DIRECTORY, "."]
    if source and (directory / rules).exists():
        return [["semgrep", "scan", "--error", "--config", rules, *source]]
    return []


def governance_commands(directory, names, source, base=None):
    """Retain governance, flavor and ledger controls where changes affect them."""
    commands = []
    cli = ["go", "run", "./cmd/standardsctl"]
    config = context_changed(names) or any(
        name.startswith((".standards", ".config/", ".agents/", ".claude/", ".codex/",
                         ".gemini/", ".cursor/", ".devcontainer/", ".github/", "templates/"))
        or name in {"lefthook.yml", "README.md"} for name in names)
    if (directory / ".standards.yaml").exists() and (source or config):
        scope = audit_scope(directory, names, base)
        commands.extend([[*cli, "audit", *scope], [*cli, "flavor", "audit", "."]])
    if (directory / ".workingdir").exists() and any(name.startswith(".workingdir/") for name in names):
        commands.append([*cli, "state", "audit", "."])
    return commands


def audit_scope(directory, names, base):
    """Bind G02's debt ratchet and touched-file guard to the actual snapshot diff."""
    args = []
    touched = names
    if base is not None:
        # A caller-supplied missing/invalid ref is a failure, never permission to
        # turn off the historical baseline comparison. Freeze valid refs to OIDs.
        oid = run(["git", "rev-parse", "--verify", "--end-of-options", base + "^{commit}"],
                  cwd=directory, env=clean_env()).decode().strip()
        args.append("--base=" + oid)
    else:
        touched = paths(run(["git", "ls-tree", "-r", "--name-only", "-z", "HEAD"],
                            cwd=directory, env=clean_env()))
        print("Audit: no trusted prior commit; every tracked path is touched. "
              "Historical baseline growth cannot be compared.")
    # The current G02 CLI accepts CSV and at most 10,000 trimmed entries. Refuse
    # inputs it cannot represent instead of silently auditing a smaller set.
    if len(touched) > 10000 or any(not name or name != name.strip() or
                                  any(char in name for char in ",\r\n") for name in touched):
        raise HookError("Audit touched paths exceed the CLI's lossless CSV/10,000-path contract")
    if touched:
        args.append("--touched=" + ",".join(touched))
    return args
