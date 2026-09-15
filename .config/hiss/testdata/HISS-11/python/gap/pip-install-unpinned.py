"""HISS-11: a dependency is installed at run time with no version pin and no hash
requirement, so the interpreter ends up running whatever the index serves today."""

import subprocess
import sys


def install_requests() -> None:
    subprocess.check_call([sys.executable, "-m", "pip", "install", "requests"])
