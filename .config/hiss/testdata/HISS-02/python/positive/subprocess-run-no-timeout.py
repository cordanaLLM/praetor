import subprocess


def run_cmd(cmd):
    """Run cmd without a deadline (reported)."""
    return subprocess.run(cmd, check=True)
