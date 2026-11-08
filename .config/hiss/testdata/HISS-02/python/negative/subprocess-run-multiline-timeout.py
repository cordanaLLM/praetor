import subprocess


def run_cmd(cmd):
    """Run cmd with a deadline passed on a later line of the call."""
    return subprocess.run(
        cmd,
        check=True,
        text=True,
        timeout=30,
    )
