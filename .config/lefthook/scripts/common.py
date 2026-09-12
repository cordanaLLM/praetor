"""Bounded process execution and isolated Git snapshots for local checks."""

import contextlib
import os
from pathlib import Path
import subprocess
import signal
import tempfile


class HookError(Exception):
    """An actionable local gate failure."""


def run(args, cwd=None, *, data=None, timeout=180, capture=True, env=None, allowed=(0,)):
    """Execute argv without a shell; preserve failures and bound every process."""
    settings = dict(os.environ if env is None else env)
    settings["PYTHONDONTWRITEBYTECODE"] = "1"
    try:
        with subprocess.Popen(args, cwd=cwd, env=settings, start_new_session=True,
                              stdin=subprocess.PIPE if data is not None else None,
                              stdout=subprocess.PIPE if capture else None,
                              stderr=subprocess.PIPE if capture else None) as process:
            try:
                stdout, stderr = process.communicate(input=data, timeout=timeout)
            except (subprocess.TimeoutExpired, KeyboardInterrupt):
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass  # The group exited between timeout detection and cleanup.
                process.communicate(timeout=5)
                raise
    except (OSError, subprocess.TimeoutExpired) as error:
        raise HookError(f"{args[0]}: {error}") from error
    if process.returncode not in allowed:
        output = (stdout or b"") + (stderr or b"")
        raise HookError(f"{' '.join(map(str, args))} exited {process.returncode}\n"
                        + output.decode(errors="replace"))
    return stdout or b""


def git(*args, cwd=None):
    return run(["git", *args], cwd=cwd)


def paths(raw):
    return [os.fsdecode(item) for item in raw.split(b"\0") if item]


def changed(base, head="HEAD"):
    return paths(git("diff", "--name-only", "-z", "--no-renames", base, head, "--"))


def clean_env():
    env = dict(os.environ)
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
                "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"):
        env.pop(key, None)
    env.update(CI="true", GOWORK="off", GOFLAGS="-mod=readonly")
    return env


@contextlib.contextmanager
def snapshot(ref=None):
    """Export the exact index or commit; never stash, stage, or edit the source."""
    with tempfile.TemporaryDirectory(prefix="praetor-hook-") as directory:
        dest = Path(directory)
        if ref is None:
            git("checkout-index", "--all", "--force", f"--prefix={dest}/")
            # Lefthook's validator requires a repository even though it only
            # validates configuration. This metadata belongs solely to the export.
            run(["git", "init", "--quiet", str(dest)], env=clean_env())
        else:
            source = git("rev-parse", "--show-toplevel").decode().strip()
            env = clean_env()
            origin = run(["git", "config", "--get", "remote.origin.url"], allowed=(0, 1)).decode().strip()
            refs = git("for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/origin/")
            run(["git", "clone", "--quiet", "--no-hardlinks", "--no-checkout",
                 "--origin", "praetor-snapshot", source, str(dest)], env=env)
            if origin:
                run(["git", "remote", "add", "origin", origin], cwd=dest, env=env)
            for line in refs.decode().splitlines():
                oid, name = line.split()
                run(["git", "update-ref", name, oid], cwd=dest, env=env)
            run(["git", "checkout", "--quiet", "--detach", ref], cwd=dest, env=env)
        yield dest


def present_files(directory, names):
    files = []
    for name in names:
        path = directory / name
        if path.is_symlink():
            raise HookError(f"{name}: changed symlinks require explicit review")
        if path.is_file():
            files.append(name)
    return files
