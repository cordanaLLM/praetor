#!/usr/bin/env python3
"""Run a security scanner against the exact packages reported by Go."""

from pathlib import Path
import sys

sys.dont_write_bytecode = True

from checks import go_packages, local_package_patterns  # noqa: E402
from common import HookError, clean_env, run  # noqa: E402


def package_patterns(root):
    root = root.resolve()
    packages = go_packages(root, ["go.mod"])
    if not packages or len(packages) > 20_000:
        raise HookError("security scan requires 1..20000 actual Go packages")
    return local_package_patterns(root, packages)


def main():
    if "--" not in sys.argv[1:]:
        raise RuntimeError("security scope requires -- followed by the scanner command")
    split = sys.argv.index("--")
    command = sys.argv[split + 1:]
    if not command:
        raise RuntimeError("security scope requires a scanner command")
    root = Path.cwd()
    patterns = package_patterns(root)
    run([*command, *patterns], cwd=root, env=clean_env(), timeout=600, capture=False)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, HookError, RuntimeError) as error:
        print(f"security scope failed: {error}", file=sys.stderr)
        sys.exit(1)
