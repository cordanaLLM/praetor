#!/usr/bin/env python3
"""The hook policy's declared external tools: resolved programs, programs started by name and
linters with their version floors.

Run this file to see what the host provides, one line per declared tool:
`python3 -B .config/lefthook/scripts/toolchain.py`. With `--require NAMES` the named tools
must be usable and the run fails when one is not.
"""

import argparse
from pathlib import Path
import re
import shutil
import sys

from common import HookError, clean_env, run

# The one place the linters and their floors are declared, beside the policy that enforces
# them (#343).
FLOORS = Path(__file__).resolve().parents[1] / "tool-floors.txt"
FLOORS_NAME = ".config/lefthook/" + FLOORS.name
# One requirement line, as the requirements files beside the policy write one: a name, then
# an operator and a version, or the name alone. The floors file and the lint lock
# (scripts/test_emitted_hook_lint.py) are both read through it.
REQUIREMENT = re.compile(r"([A-Za-z][A-Za-z0-9_.-]*)(?:(>=|==)([0-9][0-9A-Za-z.]*))?")
FLOOR_VERSION = re.compile(r"\d+(?:\.\d+)*")
# The file holds a handful of requirement lines and their comments (HISS-02).
MAX_FLOOR_LINES = 256
# One version probe: a start-up and a line of output. A tool that hangs must fail the
# hook, not hold it.
VERSION_TIMEOUT = 30
# Where each floored linter states its own version in `--version` output. Anchored per tool,
# never "the first dotted number": an actionlint built from an untagged checkout prints
# "(devel)" and then "built with go1.26.4 ...", and the Go version must not pass as its own.
VERSION_OUTPUT = {
    "actionlint": re.compile(r"\Av?(\d+(?:\.\d+)+)\s"),
    "shellcheck": re.compile(r"^version: (\d+(?:\.\d+)+)\s*$", re.M),
    "yamllint": re.compile(r"\Ayamllint (\d+(?:\.\d+)+)"),
}
# The two programs the policy resolves instead of starting by a name a command spells (#339,
# #341). No environment variable selects either: one naming any program that exits 0 would
# pass every gate without running it. Each has a fixed candidate list, tried in order, and a
# candidate is used only once it has proven what it is (proven).
#
# python.sh applies PYTHON_CANDIDATES, PYTHON_FLOOR and PYTHON_PROBE in shell, before any
# Python runs, and the operator setting hooks.python defaults to the same candidates
# (internal/config/operator_sections.go). The HookInterpreter cases of test_hooks.py and
# TestHookLauncherCandidatesAreTheOperatorDefault (internal/forge) hold the three equal.
PYTHON_CANDIDATES = (("python3",), ("python",), ("py", "-3"))
# The release .config/hook-lint/requirements.txt is compiled for (--python-version).
PYTHON_FLOOR = "3.10"
PYTHON_PROBE = "import sys; print(sys.version_info[0], sys.version_info[1], sep=chr(46))"
PYTHON_STATED = re.compile(r"\A(\d+\.\d+)\s*\Z")
LAUNCHER = ".config/lefthook/python.sh"
# GNU Make under the names it is installed by: gmake where make is another make, as on the
# BSDs, and mingw32-make on a MinGW host. The policy passes --always-make and
# --no-print-directory, which are GNU Make's, so any other make is not accepted.
MAKE_CANDIDATES = (("make",), ("gmake",), ("mingw32-make",))
MAKE_STATED = re.compile(r"\AGNU Make (\d+(?:\.\d+)+)")
RESOLVED = {"python": PYTHON_CANDIDATES, "make": MAKE_CANDIDATES}
# Every other program checks.py and hooks.py start, by the name PATH resolves, and sh, which
# starts every job of praetor.yml and the pre-push script. run (common.py) refuses the hook
# with the program named when one is missing. The DeclaredPrograms cases of test_hooks.py
# hold this list to what those files start, so a new program cannot land undeclared (#341).
BY_NAME = ("git", "go", "gofmt", "gosec", "govulncheck", "lefthook", "semgrep", "sh")
# What a rejected candidate printed is quoted in one bounded line of the refusal.
MAX_REASON = 240


def requirement(line):
    """Return (name, operator, version) for one requirement line, or None for any other line.

    A comment, an environment marker and a line continuation are set aside. Operator and
    version are None where the line is a name alone.
    """
    text = line.split("#", 1)[0].split(";", 1)[0].strip().rstrip("\\").strip()
    match = REQUIREMENT.fullmatch(text)
    return match.groups() if match else None


def floor_line(line):
    """Return (tool, floor) for a floors line written exactly `tool>=version` or `tool`.

    The floor is None for a tool alone. Any other spelling returns None.
    """
    parsed = requirement(line)
    if parsed is None:
        return None
    tool, operator, version = parsed
    exact = tool if operator is None else f"{tool}>={version}"
    if exact != line or (version is not None and FLOOR_VERSION.fullmatch(version) is None):
        return None
    return tool, version


def parse_floors(text):
    """Return {tool: floor} for the requirement lines of text; comments and blanks aside.

    A `tool>=version` line gives the floor. A `tool` line gives None: a linter that must be
    installed, at any version. Any other line, and a tool named twice, is an error naming the
    line: a floor that was misread is a gate that silently stopped checking.
    """
    lines = text.splitlines()
    if len(lines) > MAX_FLOOR_LINES:
        raise HookError(f"{FLOORS_NAME}: more than {MAX_FLOOR_LINES} lines")
    floors = {}
    for number, raw in enumerate(lines, 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        declared = floor_line(line)
        if declared is None or declared[0] in floors:
            raise HookError(f"{FLOORS_NAME}:{number}: expected one 'tool>=version' or 'tool' "
                            f"line per tool, got {raw.strip()!r}")
        floors[declared[0]] = declared[1]
    return floors


def tool_floors():
    """Return the declared linters; an unreadable file fails the hook rather than waiving them."""
    try:
        text = FLOORS.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        raise HookError(f"{FLOORS_NAME}: cannot read the tool floors: {error}") from error
    return parse_floors(text)


def below(version, floor):
    """Report whether dotted version is older than floor, comparing numbers, not text.

    A missing trailing component counts as zero, so 1.7 meets a floor of 1.7.0.
    """
    left = [int(part) for part in version.split(".")]
    right = [int(part) for part in floor.split(".")]
    width = max(len(left), len(right))
    return left + [0] * (width - len(left)) < right + [0] * (width - len(right))


def stated_version(argv, pattern):
    """Return what pattern reads in the output of argv, run in one bounded process, or None.

    The policy's one version probe: a linter, an interpreter candidate and a make candidate
    are all asked this way. A program that is missing, does not start or exits nonzero is a
    HookError.
    """
    output = run(argv, env=clean_env(), timeout=VERSION_TIMEOUT)
    match = pattern.search(output.decode(errors="replace"))
    return match.group(1) if match else None


def installed_version(tool):
    """Return the version `tool --version` states.

    A tool that is missing, exits nonzero, or states no version this policy reads is a
    HookError saying which; the caller adds the floor it was needed for.
    """
    pattern = VERSION_OUTPUT.get(tool)
    if pattern is None:
        raise HookError(f"{tool}: no --version reader in toolchain.VERSION_OUTPUT")
    version = stated_version([tool, "--version"], pattern)
    if version is None:
        raise HookError(f"{tool} --version states no version this policy reads")
    return version


def checked_version(tool, floors, which=shutil.which):
    """Return the installed version of tool when it meets its floor, or None where it has none.

    A tool the floors file does not declare, one that is missing or states no version, and
    one older than its floor are each a HookError naming the requirement. A tool declared
    without a floor only has to be on PATH.
    """
    if tool not in floors:
        raise HookError(f"{tool}: not declared in {FLOORS_NAME}")
    floor = floors[tool]
    if floor is None:
        if which(tool) is None:
            raise HookError(f"{tool} is required ({FLOORS_NAME}, any version): "
                            f"it is not on PATH")
        return None
    required = f"{tool} >= {floor} is required ({FLOORS_NAME})"
    try:
        version = installed_version(tool)
    except HookError as error:
        raise HookError(f"{required}: {error}") from error
    if below(version, floor):
        raise HookError(f"{required}: the {tool} on PATH is {version}")
    return version


def require_floors(tools, which=shutil.which):
    """Fail unless every named linter is installed, at or above its floor where it has one.

    Every failing tool is reported, not only the first, so one run names the whole gap.
    """
    names = list(tools)
    if not names:
        return
    floors = tool_floors()
    failures = []
    for tool in names:
        try:
            checked_version(tool, floors, which)
        except HookError as error:
            failures.append(str(error))
    if failures:
        raise HookError("\n".join(failures))


def python_proof(argv):
    """Return the version argv states for the probe: Python 3 at or above the floor.

    The probe is a program only an interpreter can run, so a stand-in that exits 0, the
    Microsoft Store alias Windows names python3 and a Python older than the floor each fail
    it with a HookError.
    """
    stated = stated_version([*argv, "-c", PYTHON_PROBE], PYTHON_STATED)
    if stated is None:
        raise HookError("does not answer the version probe as Python")
    if not stated.startswith("3.") or below(stated, PYTHON_FLOOR):
        raise HookError(f"is Python {stated}; Python {PYTHON_FLOOR} or newer is required")
    return stated


def make_proof(argv):
    """Return the GNU Make version argv states; any other make or program is a HookError."""
    stated = stated_version([*argv, "--version"], MAKE_STATED)
    if stated is None:
        raise HookError("does not state a GNU Make version")
    return stated


def proven(what, candidates, proof, which=shutil.which):
    """Return (argv, version) for the first candidate on PATH that proves itself.

    A candidate that is not on PATH, does not start, exits nonzero or fails proof is skipped
    with its reason. With none left this is a missing dependency naming every candidate
    tried: the caller is refused, never passed.
    """
    tried = []
    for candidate in candidates:
        name = " ".join(candidate)
        path = which(candidate[0])
        if path is None:
            tried.append(f"{name} (not on PATH)")
            continue
        argv = [path, *candidate[1:]]
        try:
            return argv, proof(argv)
        except HookError as error:
            tried.append(f"{name} ({' '.join(str(error).split())[:MAX_REASON]})")
    raise HookError(f"missing dependency: {what}. Tried: {'; '.join(tried)}")


def python_program(which=shutil.which):
    """Return (argv, version) of the interpreter python.sh starts on this host."""
    return proven(f"a Python {PYTHON_FLOOR} or newer interpreter", PYTHON_CANDIDATES,
                  python_proof, which)


def resolved_make(which=shutil.which):
    """Return (argv, version) of the GNU Make the hooks run on this host."""
    return proven("GNU Make, which builds the CLI the hooks run (the hook-cli target)",
                  MAKE_CANDIDATES, make_proof, which)


def make_program(which=shutil.which):
    """Return the GNU Make the hooks run, or fail as a missing dependency when there is none.

    A hook without make cannot build the CLI it is about to run, so nothing is skipped: the
    commit or push is refused with every candidate named, where a bare "[Errno 2]" from the
    first command read like a rule the commit had broken (#341).
    """
    return resolved_make(which)[0][0]


def on_path(tool, which=shutil.which):
    """Return where PATH resolves a program the policy starts by name; a HookError when nowhere."""
    path = which(tool)
    if path is None:
        raise HookError(f"{tool} is not on PATH")
    return path


def describe(tool, floors):
    """Return one declared tool's state on this host; a HookError says what is wrong with it."""
    if tool == "python":
        argv, version = python_program()
        return f"{version} ({' '.join(argv)})"
    if tool == "make":
        argv, version = resolved_make()
        return f"{version} ({argv[0]})"
    if tool in BY_NAME:
        return on_path(tool)
    version = checked_version(tool, floors)
    return "installed (no floor)" if version is None else f"{version} (floor {floors[tool]})"


def check(required):
    """Print one line per declared tool and return the required ones that are unusable.

    A tool outside required is reported and never fails the run: a host without hadolint is
    usable until a Dockerfile is staged, and its line carries what the hook would say then.
    """
    floors = tool_floors()
    declared = [*RESOLVED, *BY_NAME, *floors]
    unknown = sorted(set(required) - set(declared))
    if unknown:
        raise HookError(f"not declared hook tools: {', '.join(unknown)}; "
                        f"declared: {', '.join(declared)}")
    failed = []
    for tool in declared:
        try:
            line = f"{tool}: {describe(tool, floors)}"
        except HookError as error:
            state = "UNUSABLE" if tool in required else "not asserted"
            failed.extend([tool] if tool in required else [])
            line = f"{tool}: {state}: {error}"
        print(line)  # caveman:not-applicable structured-protocol
    return failed


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--require", default="", metavar="NAMES",
                        help="comma-separated declared tools that must be usable")
    args = parser.parse_args(argv)
    failed = check([name for name in args.require.split(",") if name])
    if failed:
        raise HookError("required hook tools unusable: " + ", ".join(failed))


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except HookError as error:
        print(f"praetor hooks: {error}", file=sys.stderr)
        sys.exit(1)
