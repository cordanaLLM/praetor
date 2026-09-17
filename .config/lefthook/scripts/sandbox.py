#!/usr/bin/env python3
"""Run the full gate for an exact commit in the local devcontainer image."""

import os
from pathlib import Path
import sys
import tempfile
import uuid

sys.dont_write_bytecode = True
from common import HookError, clean_env, git, run, snapshot


def user_arguments(host=os):
    """Run the container as the invoking user where the host has one.

    On POSIX a bind mount keeps host ownership, so the gate runs as the host uid:gid and leaves no
    root-owned files in the snapshot or home. Windows has no uid -- os.getuid does not exist, and
    the sandbox raised AttributeError before Docker was asked for anything -- and Docker Desktop
    maps bind-mount ownership itself, so the flag is omitted there.
    """
    if not hasattr(host, "getuid"):
        return []
    return ["--user", f"{host.getuid()}:{host.getgid()}"]


def main(args):
    if len(args) > 1:
        raise HookError("usage: sandbox.py [commit]; default is HEAD")
    ref = args[0] if args else "HEAD"
    head = git("rev-parse", "--verify", ref + "^{commit}").decode().strip()
    image = os.environ.get("PRAETOR_SANDBOX_IMAGE", "praetor-dev:audit")
    # Inspect before allocating a clone. Missing Docker/image is a real failure.
    run(["docker", "image", "inspect", image])
    name = "praetor-gate-" + uuid.uuid4().hex
    with snapshot(head) as directory, tempfile.TemporaryDirectory(prefix="praetor-home-") as home:
        try:
            run(["docker", "run", "--name", name, "--rm", *user_arguments(),
                 "--env", "HOME=/sandbox-home", "--env", "GOCACHE=/sandbox-home/go-cache",
                 "--env", "GOPATH=/sandbox-home/go", "--env", "CI=true", "--env", "GOWORK=off",
                 "--env", "GOFLAGS=-mod=readonly", "--env", "PRAETOR_SANDBOX=1",
                 "--mount", f"type=bind,src={directory},dst=/workspace",
                 "--mount", f"type=bind,src={Path(home)},dst=/sandbox-home",
                 "--workdir", "/workspace", image, "make", "verify-all"],
                timeout=2400, capture=False, env=clean_env())
        finally:
            # A timed-out CLI does not stop Docker's daemon-owned container.
            active = run(["docker", "ps", "-aq", "--filter", f"name=^{name}$"], timeout=30)
            if active.strip():
                run(["docker", "container", "rm", "--force", name], timeout=30)
    print(f"Sandbox full gate passed for {head}")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (HookError, OSError) as error:
        print(f"praetor sandbox: {error}", file=sys.stderr)
        sys.exit(1)
