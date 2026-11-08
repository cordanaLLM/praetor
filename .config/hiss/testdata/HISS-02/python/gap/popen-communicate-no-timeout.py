"""Gap: subprocess.Popen.communicate without timeout= is not decided by the line scanner."""

import subprocess


def run_communicate(cmd):
    """Spawn cmd with Popen and call communicate without a deadline."""
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE)
    return proc.communicate()
