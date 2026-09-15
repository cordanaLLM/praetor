"""Pinned to an exact resolution and verified against the hashes in the lockfile."""

import subprocess
import sys


def install_requests() -> None:
    subprocess.check_call(
        [
            sys.executable,
            "-m",
            "pip",
            "install",
            "--require-hashes",
            "-r",
            "requirements.lock",
        ]
    )
