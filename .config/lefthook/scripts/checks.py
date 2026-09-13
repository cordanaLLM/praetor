"""Scoped, read-only file and Go checks used by hooks and Make targets."""

import ast
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import re

from common import HookError, clean_env, paths, present_files, run
from privacy import PRIVATE_STATE_ERROR

GO_CONFIG = {"go.mod", "go.sum", "go.work", "go.work.sum", "Makefile",
             ".golangci.yml", ".gosec.json"}
GO_EXTENSIONS = {".go", ".s", ".c", ".h", ".cc", ".cpp", ".syso"}
CONTEXT = {"AGENTS.md", "CLAUDE.md", ".windsurfrules",
           ".github/copilot-instructions.md", ".gemini/GEMINI.md", ".codex/rules.md"}


def context_changed(names):
    return any(name in CONTEXT or name.startswith(".cursor/rules/") for name in names)


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


def file_checks(directory, names):
    files = present_files(directory, names)
    if any(name == ".workingdir" or name.startswith(".workingdir/") for name in files):
        raise HookError(PRIVATE_STATE_ERROR)
    text_checks(directory, files)
    gofiles = [name for name in files if name.endswith(".go")]
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
    yaml = [name for name in files if name.endswith((".yml", ".yaml"))]
    if yaml:
        commands.append(["yamllint", "--strict", "-d", "{extends: relaxed, rules: {line-length: disable}}", *yaml])
    if any(name in {"lefthook.yml", ".codex/hooks.json"}
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
        relative = Path(pkg["Dir"]).relative_to(directory).as_posix()
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
    root = directory.resolve()
    listed = decode_packages(run(["go", "list", "-json", *packages], cwd=directory,
                                 env=clean_env()))
    patterns = {}
    for package in listed:
        path = Path(package["Dir"]).resolve()
        try:
            relative = path.relative_to(root).as_posix()
        except ValueError as error:
            raise HookError(f"Go package directory is outside the checked snapshot: {path}") from error
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
    packages = go_packages(directory, names, reverse=True)
    governance = governance_commands(directory, names, bool(packages), base=base)
    full_gate = gate == "all" and any(cmd[3:4] == ["audit"] for cmd in governance)
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


def run_full_gate(directory):
    command = ["go", "run", "./cmd/standardsctl", "gate"]
    for subcommand in ("run", "verify"):
        run([*command, subcommand, "--path=."], cwd=directory, env=clean_env(),
            timeout=600, capture=False)
    return True


def semgrep_commands(directory, names):
    rules = ".config/semgrep/hiss-invariants.yml"
    source = [name for name in present_files(directory, names)
              if Path(name).suffix in {".go", ".py", ".rs", ".c", ".cpp", ".js", ".ts"}]
    if any(name.startswith(".config/semgrep/") for name in names):
        source = ["."]
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
        or name == "lefthook.yml" for name in names)
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
