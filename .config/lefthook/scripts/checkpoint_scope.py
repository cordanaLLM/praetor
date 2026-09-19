#!/usr/bin/env python3
"""Block new native file-tool paths after a configured checkpoint is due."""

import json
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / ".config/lefthook/scripts"))
from checkpoint import CheckpointError, _git, inspect_checkpoint
from common import resolved_relative_to

LIMIT = 1024 * 1024
PASSED = "PRAETOR_CHECKPOINT_SCOPE_OK"
TOOLS = {"Edit", "Write", "replace", "write_file"}


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def checkpoint():
    report = inspect_checkpoint(ROOT, "tool", include_paths=True)
    if "error" in report:
        raise ValueError(report["error"])
    if (not isinstance(report, dict) or report.get("schema_version") != 1
            or type(report.get("enabled")) is not bool
            or type(report.get("due")) is not bool
            or type(report.get("enforce_batch_scope")) is not bool
            or not isinstance(report.get("public_paths"), list)
            or len(report["public_paths"]) > 10000
            or not all(isinstance(path, str) and path for path in report["public_paths"])):
        raise ValueError("shared checkpoint job returned an invalid scope result")
    return report


def rooted_path(cwd, value):
    if not isinstance(value, str) or not value or "\x00" in value:
        raise ValueError("file-tool path must be one nonempty string")
    candidate = Path(value)
    if any(part == os.pardir for part in candidate.parts):
        raise ValueError("file-tool path must not contain parent traversal")
    if not candidate.is_absolute():
        candidate = cwd / candidate
    normalized = os.path.normpath(os.fspath(candidate))
    root_name = os.path.normpath(os.fspath(ROOT))
    try:
        relative = os.path.relpath(normalized, root_name)
    except ValueError as error:
        raise ValueError("file-tool path is outside the repository") from error
    if relative == os.pardir or relative.startswith(os.pardir + os.sep):
        raise ValueError("file-tool path is outside the repository")
    current = ROOT
    for part in Path(relative).parts:
        current /= part
        if current.is_symlink():
            raise ValueError("file-tool path must not traverse a symlink")
    return relative.replace(os.sep, "/")


def check(payload):
    if not isinstance(payload, dict) or payload.get("hook_event_name") not in {"PreToolUse", "BeforeTool"}:
        raise ValueError("expected a native pre-tool event")
    tool = payload.get("tool_name")
    if tool not in TOOLS:
        raise ValueError("unsupported native file tool")
    tool_input = payload.get("tool_input")
    if not isinstance(tool_input, dict) or "file_path" not in tool_input:
        raise ValueError("file-tool input requires one explicit file_path")
    root_value = payload.get("cwd")
    if not isinstance(root_value, str) or not root_value:
        raise ValueError("native pre-tool input requires cwd")
    if not Path(root_value).is_absolute():
        raise ValueError("native pre-tool cwd must be absolute")
    root = Path(root_value).resolve()
    try:
        resolved_relative_to(root, ROOT)
    except ValueError as error:
        raise ValueError("native pre-tool cwd is outside the configured repository") from error
    toplevel = Path(_git(root, "rev-parse", "--show-toplevel").decode().strip())
    if not root.is_dir() or toplevel.resolve() != ROOT.resolve():
        raise ValueError("native pre-tool cwd belongs to a different repository")
    report = checkpoint()
    relative = rooted_path(root, tool_input["file_path"])
    if not report["enabled"] or not report["due"] or not report["enforce_batch_scope"]:
        print(PASSED)
        return 0
    if relative == ".workingdir" or relative.startswith(".workingdir/"):
        print(PASSED)
        return 0
    if relative not in set(report["public_paths"]):
        sys.stderr.write("Praetor: checkpoint batch scope rejects a new public file path.\n")
        return 2
    print(PASSED)
    return 0


def main():
    raw = sys.stdin.buffer.read(LIMIT + 1)
    if not raw or len(raw) > LIMIT:
        raise ValueError("missing or oversized native pre-tool input")
    return check(json.loads(raw, object_pairs_hook=unique_object))


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (CheckpointError, OSError, TypeError, ValueError, RecursionError) as error:
        print("Praetor batch scope: " + str(error)[:1000], file=sys.stderr)
        sys.exit(2)
