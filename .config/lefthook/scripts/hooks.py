#!/usr/bin/env python3
"""Git-stage entry points. Use exact index/ref snapshots for blocking checks."""

import os
import json
from pathlib import Path
import re
import shutil
import sys

sys.dont_write_bytecode = True
from common import HookError, changed, clean_env, git, paths, run, snapshot
from checks import checkpoint_checks, context_changed, file_checks, go_packages, source_checks
from privacy import check_private_history, check_private_index

SUBJECT = re.compile(r"^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)"
                     r"(\([^()\n]+\))?!?: \S.*$")
SIGNOFF = re.compile(r"^Signed-off-by: [^<>\n]+ <[^<>\s]+@[^<>\s]+>$", re.MULTILINE)
OID = re.compile(r"^[0-9a-f]{40}([0-9a-f]{24})?$")


def guard():
    run(["python3", ".config/agent/hooks/block_evasion.py", "--environment"])


def pre_commit():
    guard()
    names = paths(git("diff", "--cached", "--name-only", "-z", "--no-renames", "--"))
    check_private_index()
    git("diff", "--cached", "--check")
    if not names:
        print("Index: no changed files")
        return
    with snapshot() as directory:
        file_checks(directory, names)
        gofiles = [name for name in names if name.endswith(".go")]
        if gofiles:
            packages = go_packages(directory, gofiles)
            if packages:
                run(["go", "vet", *packages], cwd=directory, env=clean_env(), capture=False)
    print(f"Index: {len(names)} changed paths checked")


def check_message(filename):
    message = Path(filename).read_text()
    lines = [line for line in message.splitlines() if not line.startswith("#")]
    subject = next((line for line in lines if line.strip()), "")
    # Git-generated merge and revert subjects are valid during integration.
    generated = subject.startswith(("Merge ", 'Revert "'))
    if not (SUBJECT.fullmatch(subject) or generated):
        raise HookError("Commit subject must be 'type(scope): description' (scope optional).")
    if not SIGNOFF.search("\n".join(lines)):
        raise HookError("Commit requires a DCO trailer. Use git commit -s after reviewing your changes.")


def prepare_message(filename, source=""):
    if not source and not Path(filename).read_text().strip():
        with Path(filename).open("a") as stream:
            stream.write("# Use type(scope): description; commit -s supplies your DCO sign-off.\n")


def push_updates(text):
    updates = []
    for line in text.splitlines():
        fields = line.split()
        if len(fields) != 4 or not OID.fullmatch(fields[1]) or not OID.fullmatch(fields[3]):
            raise HookError("Malformed pre-push ref input; expected local-ref oid remote-ref oid")
        if set(fields[1]) != {"0"}:
            updates.append(fields)
    return updates


def new_branch_base(head, remote, include_checkpoints=False):
    """Use a known remote ancestor; without one, conservatively check the full tree."""
    prefix = f"refs/remotes/{remote}/"
    candidates = git("for-each-ref", "--format=%(refname) %(objectname)", prefix).decode().splitlines()
    for candidate in candidates:
        ref, oid = candidate.split()
        # A WIP checkpoint has never satisfied the strict gates. It cannot be a
        # trusted baseline for publishing a new strict branch or tag. Ignore the
        # symbolic remote HEAD too; its target may be a checkpoint branch.
        if ref == prefix + "HEAD" or (not include_checkpoints and ref.startswith(prefix + "checkpoint/")):
            continue
        result = run(["git", "merge-base", "--all", head, oid], allowed=(0, 1))
        bases = result.decode().splitlines()
        if bases:
            return bases[0]
    return None


def push_check_mode(destination):
    prefix = "refs/heads/checkpoint/"
    return "checkpoint" if destination.startswith(prefix) and len(destination) > len(prefix) else "strict"


def pre_push(remote):
    guard()
    updates = push_updates(sys.stdin.read())
    if not updates:
        print("Push: no new commits (empty input or ref deletion)")
    checked = set()
    for _, local, destination, old in updates:
        mode = push_check_mode(destination)
        head = git("rev-parse", "--verify", local + "^{commit}").decode().strip()
        base = old if set(old) != {"0"} else new_branch_base(head, remote, mode == "checkpoint")
        if base:
            kind = run(["git", "cat-file", "--batch-check=%(objecttype)"], data=(base + "\n").encode())
            if kind.strip().endswith(b" missing"):
                print(f"Push baseline {base[:12]} unavailable locally; checking the full tree")
                base = None
        key = (head, base, mode)
        if key in checked:
            continue
        checked.add(key)
        check_private_history(head, base)
        names = changed(base, head) if base else paths(git("ls-tree", "-r", "--name-only", "-z", head))
        if not names:
            continue
        with snapshot(head) as directory:
            file_checks(directory, names)
            if mode == "checkpoint":
                checkpoint_checks(directory, names)
            else:
                gated = source_checks(directory, names, base=base)
                if gated:
                    preserve_receipt(directory, head)
                if os.environ.get("PRAETOR_HOOK_SANDBOX") == "1":
                    run(["python3", ".config/lefthook/scripts/sandbox.py", head], timeout=2400,
                        capture=False)
        if mode == "checkpoint":
            print(f"WIP checkpoint: {destination}; local file/build/race checks passed. "
                  "Full CI diagnostics remain required; no release receipt issued.")
        print(f"Push: {head[:12]} checked ({len(names)} changed paths)")


def preserve_receipt(directory, head):
    target = Path(git("rev-parse", "--git-path", "praetor-receipts").decode().strip())
    target.mkdir(parents=True, exist_ok=True)
    receipt = target / (head + ".json")
    shutil.copyfile(directory / ".standards-receipt.json", receipt)
    print(f"Verified receipt preserved at {receipt}")


def cli(args):
    run(["make", "--no-print-directory", "-s", "hook-cli"], capture=False, timeout=180)
    run(["bin/praetorctl", *args], capture=False, timeout=180)


def refresh(names):
    if any(name in {"go.mod", "go.sum"} for name in names):
        # Module commands may rewrite go.sum, so warm the cache in an isolated clone.
        with snapshot("HEAD") as directory:
            run(["go", "mod", "download"], cwd=directory, env=clean_env(), capture=False)
    if any(name.endswith(".go") or name in {"go.mod", "go.sum"} for name in names):
        run(["make", "--no-print-directory", "-s", "hook-cli"], capture=False)
    if context_changed(names):
        cli(["compile-context", "--verify"])
    if ".standards.yaml" in names:
        print("Governance configuration changed: run make audit before the next push.")


def post_stage(stage, args):
    if stage == "post-commit":
        cadence_status()
    elif stage == "post-checkout":
        if len(args) == 3 and args[2] == "1" and set(args[0]) != {"0"}:
            refresh(changed(args[0], args[1]))
    elif stage == "post-merge":
        refresh(changed("ORIG_HEAD"))
    else:
        names = set()
        for line in sys.stdin.read().splitlines():
            pairs = line.split()
            if len(pairs) < 2 or not all(OID.fullmatch(oid) for oid in pairs[:2]):
                raise HookError("Malformed post-rewrite input")
            names.update(changed(pairs[0], pairs[1]))
        refresh(sorted(names))


def cadence_status():
    """Read the cadence without executing or claiming to have completed a scan."""
    count = int(git("rev-list", "--count", "HEAD"))
    path = Path(".workingdir/cadence.json")
    state = json.loads(path.read_text()) if path.exists() else {}
    last = state.get("last_commit_count", 0)
    if not isinstance(last, int) or last < 0:
        raise HookError("Invalid last_commit_count in .workingdir/cadence.json")
    delta = count - last if count >= last else count
    if not state or delta >= 20:
        print(f"Dedupe review due ({delta} commits since recorded audit); schedule make dedupe.")
    else:
        print(f"Dedupe cadence: {delta}/20 commits; no scan needed.")


def main(argv):
    if not argv:
        raise HookError("Expected hook name or 'changed <gate> <base>'")
    stage, *args = argv
    root = git("rev-parse", "--show-toplevel").decode().strip()
    os.chdir(root)
    if stage == "pre-commit":
        pre_commit()
    elif stage == "commit-msg":
        check_message(args[0])
    elif stage == "prepare-commit-msg":
        prepare_message(*args)
    elif stage == "pre-push":
        pre_push(args[0] if args else "origin")
    elif stage == "changed":
        gate, base = args
        base = git("rev-parse", "--verify", "--end-of-options", base + "^{commit}").decode().strip()
        head = git("rev-parse", "--verify", "HEAD^{commit}").decode().strip()
        names = changed(base, head)
        with snapshot(head) as directory:
            if gate == "packages":
                print("\n".join(go_packages(directory, names, reverse=True)))
            else:
                source_checks(directory, names, gate, base=base)
    elif stage in {"post-commit", "post-checkout", "post-merge", "post-rewrite"}:
        post_stage(stage, args)
    else:
        raise HookError(f"Unknown hook: {stage}")


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except (HookError, OSError, ValueError, IndexError) as error:
        print(f"praetor hooks: {error}", file=sys.stderr)
        sys.exit(1)
