#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Select Platform Neutrality steps; reuse the hook's Go dependency selector."""

import argparse
import json
import sys
from pathlib import Path

sys.dont_write_bytecode = True

from checks import GO_CONFIG, GO_EXTENSIONS, go_packages
from common import HookError, clean_env, paths, run

FLAGS = (
    "go",
    "adopt",
    "cli",
    "harness",
    "schemacheck",
    "figures",
    "interfig",
    "markdown",
)
FULL_PREFIXES = (".github/", ".config/", "tools/go/", "tools/lefthook/")
STEP_FLAGS = {
    "Select Buildable Go Packages": "go",
    "Compile Every Package": "go",
    "Vet Every Package": "go",
    "Download And Build Pinned Tools": "go",
    "Test Every Package": "go",
    "Upload Per-Test Timing": "go",
    "run-2 (adopt)": "adopt",
    "run-3/run-4 (CLI halves)": "cli",
    "Test The Client Schema Oracle Module": "schemacheck",
    "Install Pinned Git Hook Toolchain": "harness",
    "Install Security Scanner": "harness",
    "Install YAML Linter": "harness",
    "Install Shell Linter": "harness",
    "Assert Hook Toolchain": "harness",
    "Harness Self-Tests": "harness",
    "Figure Build Check": "figures",
    "Interfig Verify Gate": "interfig",
    "Markdown Gate Self-Test": "markdown",
}


def full_scope(root, count, reason):
    try:
        packages = go_packages(root, ["go.mod"])
        if not packages:
            raise HookError("go list returned no main-module packages")
    except (HookError, OSError, ValueError) as error:
        # The workflow expands this explicit full-tree pattern with go list. A persistent
        # listing failure fails that step, rather than becoming an empty successful test.
        packages = ["./..."]
        reason += f"; full package listing failed: {error}; using ./..."
    return "full", count, reason, dict.fromkeys(FLAGS, True), packages


def path_kind(root, name):
    """Allow known step inputs; unknown non-Go inputs require an explicit full run."""
    if (
        name in GO_CONFIG
        or name.startswith(FULL_PREFIXES)
        or name == "scripts/portability_selftest.py"
    ):
        return "full"
    if name.startswith(
        ("tools/schemacheck/", "tools/figures/", "tools/markdownlint/", "scripts/")
    ):
        return "step"
    if name.startswith(("changelog.d/", "docs/", "LICENSES/")) or name == "REUSE.toml":
        return "step"
    if "/" not in name and name.endswith(".md"):
        return "step"
    if Path(name).suffix in GO_EXTENSIONS or "/testdata/" in name:
        parent = (root / name).parent
        if not (root / name).exists() and not any(parent.glob("*.go")):
            # A removed package matches no listed package, so its importers would go
            # unselected; only a full run builds them.
            return "full"
        return "go"
    # Only the existing selector knows which arbitrary input paths Go embeds.
    if go_packages(root, [name], reverse=True):
        return "go"
    return "full"


def selected_scope(root, names):
    flags = dict.fromkeys(FLAGS, False)
    for name in names:
        if path_kind(root, name) == "full":
            return full_scope(root, len(names), "full-scope path: " + json.dumps(name))
    main_names = [name for name in names if not name.startswith("tools/schemacheck/")]
    packages = go_packages(root, main_names, reverse=True) if main_names else []
    flags["go"] = bool(packages)
    flags["adopt"] = any(pkg.endswith("/internal/adopt") for pkg in packages)
    flags["cli"] = any(pkg.endswith("/cmd/standardsctl") for pkg in packages)
    flags["harness"] = flags["cli"] or any(
        name.startswith("scripts/") for name in names
    )
    flags["schemacheck"] = any(name.startswith("tools/schemacheck/") for name in names)
    if packages and (root / "tools/schemacheck/go.mod").exists():
        dependencies = (
            run(
                ["go", "-C", "tools/schemacheck", "list", "-deps", "-test", "./..."],
                cwd=root,
                env=clean_env(),
                timeout=180,
            )
            .decode()
            .splitlines()
        )
        flags["schemacheck"] |= bool(set(packages).intersection(dependencies))
    flags["figures"] = any(
        name.startswith(("tools/figures/", "docs/")) or name == "README.md"
        for name in names
    )
    flags["interfig"] = any(
        name.startswith("tools/figures/third_party/interfig/")
        or name in {"scripts/sync_interfig.py", "scripts/test_sync_interfig.py"}
        for name in names
    )
    flags["markdown"] = any(name.startswith("tools/markdownlint/") for name in names)
    return (
        "selected",
        len(names),
        "changed-path allow-list and reverse Go importers",
        flags,
        packages,
    )


def scope(root, event, base, head):
    names = []
    try:
        names = paths(
            run(
                [
                    "git",
                    "diff",
                    "--name-only",
                    "--no-renames",
                    "-z",
                    f"{base}...{head}",
                    "--",
                ],
                cwd=root,
                env=clean_env(),
                timeout=60,
            )
        )
    except (HookError, OSError) as error:
        return full_scope(
            root,
            0,
            f"git diff failed (changed file count unavailable): {error}; event: {event}",
        )
    if event != "pull_request":
        return full_scope(root, len(names), f"event {event} requires every step")
    try:
        return selected_scope(root, names)
    except (HookError, OSError, ValueError) as error:
        return full_scope(root, len(names), f"Go scope discovery failed: {error}")


def report(result):
    mode, count, reason, flags, packages = result
    reason = " ".join(reason.splitlines())
    lines = [
        f"changed files: {count}",
        f"scope: {mode}; reason: {reason}",
        f"Go packages: {len(packages)}",
    ]
    for step, flag in STEP_FLAGS.items():
        state = "run" if flags[flag] else "skipped"
        why = (
            reason
            if mode == "full"
            else f"{'affected' if flags[flag] else 'no affected'} {flag} inputs/packages"
        )
        lines.append(f"{step}: {state}; reason: {why}")
    node = any(flags[flag] for flag in ("figures", "interfig", "markdown"))
    lines.append(
        f"Setup Node Toolchain: {'run' if node else 'skipped'}; reason: "
        f"{'affected' if node else 'no affected'} figures/interfig/markdown inputs"
    )
    lines.extend(
        f"{step}: run; reason: required on every scope"
        for step in ("Build praetorctl", "Documentation References")
    )
    return "\n".join(lines) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--event", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--summary", type=Path, required=True)
    parser.add_argument("--packages", type=Path, required=True)
    args = parser.parse_args(argv)
    result = scope(Path.cwd(), args.event, args.base, args.head)
    mode, count, _, flags, packages = result
    # newline="\n": text mode writes os.linesep, and on Windows bash `read -r` would keep the
    # carriage return in every package name it hands to go list.
    args.packages.write_text(
        "".join(package + "\n" for package in packages), encoding="utf-8", newline="\n"
    )
    values = {
        "scope": mode,
        "files": count,
        **{key: str(value).lower() for key, value in flags.items()},
    }
    with args.output.open("a", encoding="utf-8", newline="\n") as output:
        output.write("".join(f"{key}={value}\n" for key, value in values.items()))
    text = report(result)
    print(text, end="")  # caveman:not-applicable structured-protocol
    with args.summary.open("a", encoding="utf-8") as summary:
        summary.write(text)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, HookError) as error:
        print(f"CI scope failed: {error}", file=sys.stderr)
        sys.exit(1)
