"""Gap: subprocess.Popen.wait without timeout= is not decided by the line scanner."""

import subprocess


def run_wait(cmd):
    """Spawn cmd with Popen and wait without a deadline."""
    proc = subprocess.Popen(cmd)
    return proc.wait()
