"""Reject new private state using Git metadata, before exporting file contents."""

import subprocess

from common import HookError, git

MAX_PRIVATE_COMMITS = 1000
PRIVATE_STATE_ERROR = ("Private .workingdir content must stay untracked. "
                       "Publish reviewed, sanitized documentation under docs/ instead.")


def private_diff(*args):
    """A quiet metadata diff uses constant output and never follows blob links."""
    command = ["git", "--no-replace-objects", *args, "--quiet", "--no-ext-diff", "--no-textconv",
               "--no-renames", "--ignore-submodules=none", "--diff-filter=ACMRTUXB",
               "--", ".workingdir"]
    try:
        result = subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                timeout=60, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise HookError(f"Git private-state inspection failed: {error}") from error
    if result.returncode == 1:
        raise HookError(PRIVATE_STATE_ERROR)
    if result.returncode != 0:
        raise HookError(f"Git private-state inspection failed (exit {result.returncode})")


def check_private_index():
    private_diff("diff", "--cached")


def check_private_history(head, base):
    """Inspect every outgoing commit; a removed tip file may remain in history."""
    revisions = [head] + (["^" + base] if base else [])
    commits = git("--no-replace-objects", "rev-list", "--parents",
                  f"--max-count={MAX_PRIVATE_COMMITS + 1}", *revisions, "--").splitlines()
    if len(commits) > MAX_PRIVATE_COMMITS:
        raise HookError(f"Private-state history exceeds {MAX_PRIVATE_COMMITS} commits; "
                        "refresh the remote baseline or split the reviewed push.")
    if commits and git("rev-parse", "--is-shallow-repository").strip() != b"false":
        raise HookError("Private-state history is incomplete in a shallow repository; fetch full history.")
    for entry in commits:
        fields = entry.decode("ascii").split()
        if not fields or len(fields) > 65:
            raise HookError("Private-state history has an invalid or excessive parent list")
        # All outgoing parents are visited. Compare merges to their first parent
        # so unchanged private state already on that baseline is not reintroduced.
        revision = [fields[1], fields[0]] if len(fields) > 1 else [fields[0]]
        private_diff("diff-tree", "--root", "-r", *revision)
