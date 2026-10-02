#!/usr/bin/env python3
"""The hook policy's declared external tools: linter version floors and resolved programs.

Run this file to see what the host provides, one line per declared tool:
`python3 -B .config/lefthook/scripts/toolchain.py`. With `--require NAMES` the named tools
must be usable and the run fails when one is not.
"""

import argparse
import os
from pathlib import Path
import re
import sys

from common import HookError, clean_env, run

ROOT = Path(__file__).resolve().parents[3]
# The one place the floors are declared, beside the policy that enforces them (#343).
FLOORS = Path(__file__).resolve().parents[1] / "tool-floors.txt"
FLOORS_NAME = ".config/lefthook/" + FLOORS.name
FLOOR_LINE = re.compile(r"([a-z][a-z0-9_.-]*)>=(\d+(?:\.\d+)*)")
# The file holds a handful of requirement lines and their comments (HISS-02).
MAX_FLOOR_LINES = 256
# One `--version` call: a start-up and a line of output. A tool that hangs must fail the
# hook, not hold it.
VERSION_TIMEOUT = 30
# Where each floored tool states its own version in `--version` output. Anchored per tool,
# never "the first dotted number": an actionlint built from an untagged checkout prints
# "(devel)" and then "built with go1.26.4 ...", and the Go version must not pass as its own.
VERSION_OUTPUT = {
    "actionlint": re.compile(r"\Av?(\d+(?:\.\d+)+)\s"),
    "hadolint": re.compile(r"\AHaskell Dockerfile Linter v?(\d+(?:\.\d+)+)"),
    "shellcheck": re.compile(r"^version: (\d+(?:\.\d+)+)\s*$", re.M),
    "yamllint": re.compile(r"\Ayamllint (\d+(?:\.\d+)+)"),
}
# Programs the policy resolves once from the environment, {tool: (variable, default)},
# instead of by a name each command spells (#339). python.sh, beside the policy, is the one
# place a shell starts the interpreter; a hook already running reuses sys.executable.
RESOLVED = {"python": ("PRAETOR_PYTHON", "python3")}
LAUNCHER = ".config/lefthook/python.sh"
PYTHON_VERSION = re.compile(r"\APython (3\.\d+\.\d+\S*)")


def parse_floors(text):
    """Return {tool: floor} for the `tool>=version` lines of text; comments and blanks aside.

    Any other line, and a tool named twice, is an error naming the line: a floor that was
    misread is a gate that silently stopped checking.
    """
    lines = text.splitlines()
    if len(lines) > MAX_FLOOR_LINES:
        raise HookError(f"{FLOORS_NAME}: more than {MAX_FLOOR_LINES} lines")
    floors = {}
    for number, raw in enumerate(lines, 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        match = FLOOR_LINE.fullmatch(line)
        if match is None or match.group(1) in floors:
            raise HookError(f"{FLOORS_NAME}:{number}: expected one 'tool>=version' line per "
                            f"tool, got {raw.strip()!r}")
        floors[match.group(1)] = match.group(2)
    return floors


def tool_floors():
    """Return the declared floors; an unreadable file fails the hook rather than waiving them."""
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


def installed_version(tool):
    """Return the version `tool --version` states, from one bounded process.

    A tool that is missing, exits nonzero, or states no version this policy reads is a
    HookError saying which; the caller adds the floor it was needed for.
    """
    pattern = VERSION_OUTPUT.get(tool)
    if pattern is None:
        raise HookError(f"{tool}: no --version reader in toolchain.VERSION_OUTPUT")
    output = run([tool, "--version"], env=clean_env(), timeout=VERSION_TIMEOUT)
    match = pattern.search(output.decode(errors="replace"))
    if match is None:
        raise HookError(f"{tool} --version states no version this policy reads")
    return match.group(1)


def checked_version(tool, floors):
    """Return the installed version of tool when it meets its declared floor.

    A tool without a floor, one that is missing or states no version, and one older than its
    floor are each a HookError naming the requirement.
    """
    floor = floors.get(tool)
    if floor is None:
        raise HookError(f"{tool}: no version floor declared in {FLOORS_NAME}")
    required = f"{tool} >= {floor} is required ({FLOORS_NAME})"
    try:
        version = installed_version(tool)
    except HookError as error:
        raise HookError(f"{required}: {error}") from error
    if below(version, floor):
        raise HookError(f"{required}: the {tool} on PATH is {version}")
    return version


def require_floors(tools):
    """Fail unless every named tool is installed at or above its declared floor.

    Every failing tool is reported, not only the first, so one run names the whole gap.
    """
    names = list(tools)
    if not names:
        return
    floors = tool_floors()
    failures = []
    for tool in names:
        try:
            checked_version(tool, floors)
        except HookError as error:
            failures.append(str(error))
    if failures:
        raise HookError("\n".join(failures))


def resolved_program(tool, environ=None):
    """Return the program the policy runs for a resolved tool.

    It is the value of the tool's variable, or its default where the variable is unset or
    empty: the rule python.sh applies to PRAETOR_PYTHON.
    """
    variable, default = RESOLVED[tool]
    return (os.environ if environ is None else environ).get(variable) or default


def python_version():
    """Start the interpreter the way every hook does, through python.sh, and return what it is.

    A launcher that cannot start it, and a program that is no Python 3, are a HookError.
    """
    variable, default = RESOLVED["python"]
    program = resolved_program("python")
    try:
        output = run(["sh", LAUNCHER, "-V"], cwd=ROOT, timeout=VERSION_TIMEOUT)
    except HookError as error:
        raise HookError(f"hook interpreter {program} did not start ({variable} names it, "
                        f"{default} where unset): {error}") from error
    match = PYTHON_VERSION.search(output.decode(errors="replace"))
    if match is None:
        raise HookError(f"{program} is not Python 3; set {variable} to a Python 3 interpreter")
    return f"{match.group(1)} ({program})"


def describe(tool, floors):
    """Return one declared tool's state on this host; a HookError says what is wrong with it."""
    if tool == "python":
        return python_version()
    return f"{checked_version(tool, floors)} (floor {floors[tool]})"


def check(required):
    """Print one line per declared tool and return the required ones that are unusable.

    A tool outside required is reported and never fails the run: a host without hadolint is
    usable until a Dockerfile is staged, and its line carries what the hook would say then.
    """
    floors = tool_floors()
    declared = [*RESOLVED, *floors]
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
