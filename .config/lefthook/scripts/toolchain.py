"""The hook policy's declared external tools: the version floors its linters are held to."""

from pathlib import Path
import re

from common import HookError, clean_env, run

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


def floor_failure(tool, floors):
    """Return why tool does not meet its declared floor, or None when it does."""
    floor = floors.get(tool)
    if floor is None:
        return f"{tool}: no version floor declared in {FLOORS_NAME}"
    required = f"{tool} >= {floor} is required ({FLOORS_NAME})"
    try:
        version = installed_version(tool)
    except HookError as error:
        return f"{required}: {error}"
    if below(version, floor):
        return f"{required}: the {tool} on PATH is {version}"
    return None


def require_floors(tools):
    """Fail unless every named tool is installed at or above its declared floor.

    Every failing tool is reported, not only the first, so one run names the whole gap.
    """
    names = list(tools)
    if not names:
        return
    floors = tool_floors()
    failures = [failure for failure in (floor_failure(tool, floors) for tool in names)
                if failure is not None]
    if failures:
        raise HookError("\n".join(failures))
