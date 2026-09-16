#!/usr/bin/env python3
"""Git-stage entry points. Use exact index/ref snapshots for blocking checks."""

import os
import pathlib
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
    audit_live_state()
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
    # Lefthook restores partially staged worktree bytes before commit-msg.
    # Checking freshness here preserves partial commits without certifying the
    # temporary staged-only tree that Lefthook exposes during pre-commit.
    verify_live_state()
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


# MAX_LS_REMOTE_LINES bounds the remote listing this hook reads (HISS-02).
MAX_LS_REMOTE_LINES = 64


def remote_default_ref(remote):
    """Return the remote's own default branch ref, or None when it cannot be established.

    refs/remotes/<remote>/HEAD is a local symref set once at clone time. Git never updates it
    when the remote's default changes, it is inherited when a clone is cloned, and
    `git remote set-head` can point it anywhere. Trusting it made the push scan scope depend on
    a local ref nobody set deliberately: in one checkout it named a feature branch, the base
    resolved six merges stale, and the staged scan grew from 3 files to 36 -- enough for
    semgrep-core to exhaust the host memlock limit and refuse the push with an allocation error
    that named nothing relevant (#113).

    A push is already a network operation, so the remote is asked directly and the answer is
    authoritative. An offline or slow remote falls back to the local symref rather than failing
    the push, and says which answer it used.
    """
    result = run(["git", "ls-remote", "--symref", remote, "HEAD"], allowed=(0, 1, 128))
    for line in result.decode(errors="replace").splitlines()[:MAX_LS_REMOTE_LINES]:
        fields = line.split()
        if len(fields) >= 3 and fields[0] == "ref:" and fields[2] == "HEAD":
            return f"refs/remotes/{remote}/" + fields[1].removeprefix("refs/heads/")
    return None


def new_branch_base(head, remote, include_checkpoints=False):
    """Prefer the remote's own default branch; otherwise check known refs or all history."""
    prefix = f"refs/remotes/{remote}/"
    records = git("for-each-ref", "--format=%(refname) %(objectname) %(symref)", prefix).decode().splitlines()
    candidates = [record.split() for record in records]
    default = remote_default_ref(remote)
    if default is None:
        # The local symref is the fallback, not the source of truth. Naming it keeps a stale
        # one visible instead of silently choosing the scan scope.
        default = next((row[2] for row in candidates if len(row) == 3 and row[0] == prefix + "HEAD"), None)
        if default:
            print(f"Push baseline: remote default unavailable; using local {default}")
    candidates.sort(key=lambda row: row[0] != default)
    for ref, oid, *symbolic in candidates:
        # A WIP checkpoint has never satisfied the strict gates. It cannot be a
        # trusted baseline for publishing a new strict branch or tag. Resolve
        # HEAD to its actual candidate; symbolic aliases cannot hide checkpoints.
        if symbolic or ref == prefix + "HEAD" or (not include_checkpoints and ref.startswith(prefix + "checkpoint/")):
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
    if updates:
        verify_live_state()
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
        # Say what is being scanned and which base produced it. A scope that is wrong because the
        # base is wrong otherwise surfaces only as whatever the scanner does when handed too much
        # work -- in #113 that was semgrep-core exhausting the host memlock limit and reporting an
        # allocation failure, which points at memory, the kernel and semgrep, and never at the
        # scope. One line here is the difference between a five-minute diagnosis and an hour.
        print(f"Push scope: {len(names)} file(s) versus "
              f"{base[:12] if base else 'the full tree'}")
        if not names:
            continue
        check_pushed_snapshot(head, base, mode, names)
        if mode == "checkpoint":
            print(f"WIP checkpoint: {destination}; local file/build/race checks passed. "
                  "Full CI diagnostics remain required; no release receipt issued.")
        print(f"Push: {head[:12]} checked ({len(names)} changed paths)")


def check_pushed_snapshot(head, base, mode, names):
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


def preserve_receipt(directory, head):
    target = Path(git("rev-parse", "--git-path", "praetor-receipts").decode().strip())
    target.mkdir(parents=True, exist_ok=True)
    receipt = target / (head + ".json")
    shutil.copyfile(directory / ".standards-receipt.json", receipt)
    print(f"Verified receipt preserved at {receipt}")


def praetorctl_path():
    """Absolute path to the CLI built for the repository being hooked.

    CreateProcessW cannot resolve a bare relative path that uses forward slashes and lacks a
    ``./`` prefix, so ``bin/praetorctl`` raised FileNotFoundError on Windows before a single check
    ran. The path is therefore absolute, and the ``.exe`` suffix matches what the Makefile now
    produces for the host.

    It resolves from the working directory rather than from this file. The hook runs inside the
    repository being committed, which during the harness self-tests is a temporary fixture with
    its own ``bin/``; anchoring to this script's location would have pointed every fixture at the
    real repository's binary instead.
    """
    suffix = ".exe" if os.name == "nt" else ""
    return os.path.abspath(os.path.join("bin", "praetorctl" + suffix))

def cli(args):
    run(["make", "--always-make", "--no-print-directory", "-s", "hook-cli"], capture=False, timeout=180)
    run([praetorctl_path(), *args], capture=False, timeout=180)


def verify_live_state():
    """Check private workstation state outside the exported Git snapshot."""
    audit_live_state()
    run([praetorctl_path(), "state", "sync", "--verify", "."], capture=False, timeout=30)
    print('PRAETOR_STATE_RESULT={"schema_version":1,"verified":true}', flush=True)


def audit_live_state():
    cli(["state", "audit", "."])


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
        cli(["state", "sync", ".", "--log=Git post-commit synchronization"])
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
    elif stage == "state-verify":
        verify_live_state()
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
