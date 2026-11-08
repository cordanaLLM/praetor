import subprocess


def run_forwarded(cmd, **kwargs):
    """Forward caller keyword arguments, deadline included, to subprocess.run."""
    return subprocess.run(cmd, **kwargs)
