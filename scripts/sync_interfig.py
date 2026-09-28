#!/usr/bin/env python3
"""Verify, drift-check and update the vendored interfig engine in tools/figures/third_party/interfig/.

The decision record is docs/adr/0015-interactive-figures-from-vendored-interfig.md (section 8);
the operator procedure is tools/figures/third_party/interfig/VENDOR.md.

- `verify` is offline and runs in `make interfig-verify`: the files under upstream/ match the
  vendor.json hashes, the include list covers every file, the LICENSE hash is the recorded one,
  and the REUSE.toml MIT override is the last annotation covering every vendored file.
- `check` is online: it compares the newest upstream commit touching hindsight-interfig/ with
  `path_commit` and exits EXIT_DRIFT on drift, 1 on any other failure.
- `update --commit <sha>` fetches the include list at <sha>, fails closed on a LICENSE change,
  an unlisted upstream file or an incompatible React peer range, swaps upstream/ and
  vendor.json in with a rollback on error, then runs the upstream tests and rebuilds figures.

Standard library only. Every request has a timeout and a size cap, every loop a bound.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath

MAX_FILE_SIZE = 1024 * 1024
TIMEOUT = 30
BUILD_TIMEOUT = 600
MAX_FILES = 256
MAX_DIRS = 32
MAX_ANNOTATIONS = 256
LOG_PAGE = 50
EXIT_DRIFT = 3

REPO_ROOT = Path(__file__).resolve().parents[1]
TOOLS_FIGURES = REPO_ROOT / "tools" / "figures"
VENDOR_DIR = TOOLS_FIGURES / "third_party" / "interfig"
UPSTREAM_DIR = VENDOR_DIR / "upstream"
VENDOR_JSON = VENDOR_DIR / "vendor.json"
REUSE_TOML = REPO_ROOT / "REUSE.toml"

UPSTREAM_REPO = "vectorize-io/hindsight"
UPSTREAM_PATH = "hindsight-interfig"
API = f"https://api.github.com/repos/{UPSTREAM_REPO}"
RAW = f"https://raw.githubusercontent.com/{UPSTREAM_REPO}"
# The vendored tree relative to the repository root, as REUSE.toml and git name it.
UPSTREAM_REL = "tools/figures/third_party/interfig/upstream"
OVERRIDE_PATH = f"{UPSTREAM_REL}/**"
LISTED_DIRS = ("src", "scripts")
FULL_SHA = re.compile(r"[0-9a-f]{40}")
REVERT_HINT = (
    "upstream/ and vendor.json are updated; revert with: "
    "git checkout -- tools/figures/third_party/interfig docs/assets/figures && "
    f"git clean -fd -- {UPSTREAM_REL} docs/assets/figures"
)


class SyncError(RuntimeError):
    """A failed check or step; main() turns it into exit status 1."""


class DriftError(SyncError):
    """Upstream moved past path_commit; main() turns it into EXIT_DRIFT."""


class Fetcher:
    """GETs one URL with a timeout and a size cap; 404 is returned, other HTTP errors raise."""

    def get(self, url: str) -> tuple[bytes, int]:
        req = urllib.request.Request(url)
        token = os.environ.get("GITHUB_TOKEN")
        if token and url.startswith("https://api.github.com/"):
            req.add_header("Authorization", f"Bearer {token}")
            req.add_header("X-GitHub-Api-Version", "2022-11-28")
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT) as response:
                body = response.read(MAX_FILE_SIZE + 1)
                status = response.status
        except urllib.error.HTTPError as e:
            if e.code == 404:
                e.close()
                return b"", 404
            raise
        if len(body) > MAX_FILE_SIZE:
            raise SyncError(f"{url} exceeds the 1 MiB limit")
        return body, status


def fetch_ok(fetcher: Fetcher, url: str) -> bytes:
    body, status = fetcher.get(url)
    if status != 200:
        raise SyncError(f"GET {url} returned HTTP {status}")
    return body


def fetch_json(fetcher: Fetcher, url: str):
    try:
        return json.loads(fetch_ok(fetcher, url).decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        raise SyncError(f"GET {url} returned invalid JSON") from e


def bounded(items, limit: int, what: str) -> list:
    items = list(items)
    if len(items) > limit:
        raise SyncError(f"{what}: {len(items)} entries exceed the cap of {limit}")
    return items


def load_vendor() -> dict:
    with open(VENDOR_JSON, encoding="utf-8") as f:
        return json.load(f)


def sha256_bytes(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def is_excluded(rel: str, exclude: list[str]) -> bool:
    return any(rel == ex or (ex.endswith("/") and rel.startswith(ex)) for ex in exclude)


# --- verify -------------------------------------------------------------------------------


def reuse_glob_matches(pattern: str, path: str) -> bool:
    """REUSE.toml globbing: `*` stops at `/`, `**` crosses it."""
    regex = re.escape(pattern).replace(r"\*\*", "\0").replace(r"\*", "[^/]*").replace("\0", ".*")
    return re.fullmatch(regex, path) is not None


def annotation_paths(annotation: dict) -> list[str]:
    paths = annotation.get("path", [])
    return [paths] if isinstance(paths, str) else list(paths)


def last_matching_annotation(annotations: list[dict], path: str) -> dict | None:
    """REUSE 3.3 applies the last annotation table whose path covers a file."""
    match = None
    for annotation in annotations:
        if any(reuse_glob_matches(p, path) for p in annotation_paths(annotation)):
            match = annotation
    return match


def check_override(annotation: dict | None, rel: str) -> None:
    paths = annotation_paths(annotation) if annotation else []
    if OVERRIDE_PATH not in paths:
        raise SyncError(
            f"REUSE.toml: the last annotation covering {UPSTREAM_REL}/{rel} is "
            f"{paths}; the {OVERRIDE_PATH} override must sit after the ** table"
        )
    if annotation.get("SPDX-License-Identifier") != "MIT":
        raise SyncError(f"REUSE.toml: the {OVERRIDE_PATH} override must be SPDX-License-Identifier MIT")
    if annotation.get("precedence") != "override":
        raise SyncError(f'REUSE.toml: the {OVERRIDE_PATH} override must set precedence = "override"')


def verify_reuse(files: list[str]) -> None:
    with open(REUSE_TOML, "rb") as f:
        reuse = tomllib.load(f)
    annotations = bounded(reuse.get("annotations", []), MAX_ANNOTATIONS, "REUSE.toml annotations")
    if not any(OVERRIDE_PATH in annotation_paths(a) for a in annotations):
        raise SyncError(f"REUSE.toml: missing override annotation for {OVERRIDE_PATH}")
    for rel in files:
        path = f"{UPSTREAM_REL}/{rel}"
        check_override(last_matching_annotation(annotations, path), rel)


def list_upstream_files() -> list[str]:
    if not UPSTREAM_DIR.is_dir():
        raise SyncError(f"{UPSTREAM_DIR} is missing")
    entries = []
    for count, path in enumerate(UPSTREAM_DIR.rglob("*")):
        if count >= MAX_FILES * 2:
            raise SyncError(f"{UPSTREAM_DIR} holds more than {MAX_FILES * 2} entries")
        entries.append(path)
    files = sorted(p.relative_to(UPSTREAM_DIR).as_posix() for p in entries if p.is_file())
    return bounded(files, MAX_FILES, "files under upstream/")


def check_hashes(vendor: dict, files: list[str]) -> None:
    expected = vendor["files"]
    unexpected = sorted(set(files) - set(expected))
    if unexpected:
        raise SyncError(f"Unexpected file in upstream/: {', '.join(unexpected)}")
    missing = sorted(set(expected) - set(files))
    if missing:
        raise SyncError(f"Missing file in upstream/: {', '.join(missing)}")
    for rel in files:
        actual = sha256_file(UPSTREAM_DIR / rel)
        if actual != expected[rel]:
            raise SyncError(f"Hash mismatch for {rel}: expected {expected[rel]}, got {actual}")


def check_license(vendor: dict) -> None:
    recorded = vendor["files"].get("LICENSE")
    if recorded != vendor["license_sha256"]:
        raise SyncError(
            f"LICENSE hash {recorded} in vendor.json files differs from license_sha256 "
            f"{vendor['license_sha256']}: a LICENSE change needs a new review, not a re-hash"
        )


def check_coverage(vendor: dict, files: list[str]) -> None:
    include = bounded(vendor["include"], MAX_FILES, "vendor.json include")
    exclude = bounded(vendor["exclude"], MAX_FILES, "vendor.json exclude")
    both = sorted(rel for rel in include if is_excluded(rel, exclude))
    if both:
        raise SyncError(f"File both included and excluded: {', '.join(both)}")
    on_disk = set(files) - {"LICENSE"}
    unlisted = sorted(on_disk - set(include))
    if unlisted:
        raise SyncError(f"File on disk not in include list: {', '.join(unlisted)}")
    absent = sorted(set(include) - on_disk)
    if absent:
        raise SyncError(f"File in include list not on disk: {', '.join(absent)}")


def verify() -> None:
    vendor = load_vendor()
    files = list_upstream_files()
    check_hashes(vendor, files)
    check_license(vendor)
    check_coverage(vendor, files)
    verify_reuse(files)
    print("Verify OK")


# --- check --------------------------------------------------------------------------------


def path_commits(fetcher: Fetcher, ref: str, per_page: int) -> list:
    """Commits reachable from `ref` that touch hindsight-interfig/, newest first."""
    url = f"{API}/commits?path={UPSTREAM_PATH}&sha={ref}&per_page={per_page}"
    commits = fetch_json(fetcher, url)
    if not isinstance(commits, list):
        raise SyncError(f"GET {url} did not return a commit list")
    return commits


def latest_path_commit(fetcher: Fetcher) -> str:
    commits = path_commits(fetcher, "main", 1)
    if not commits or "sha" not in commits[0]:
        raise SyncError(f"No commit found under {UPSTREAM_PATH}/ on upstream main")
    return commits[0]["sha"]


def compare_url(pinned: str, sha: str) -> str:
    return f"https://github.com/{UPSTREAM_REPO}/compare/{pinned}...{sha}"


def check(fetcher: Fetcher) -> None:
    pinned = load_vendor()["path_commit"]
    latest = latest_path_commit(fetcher)
    if latest != pinned:
        raise DriftError(
            f"Drift detected: {UPSTREAM_PATH}/ moved from {pinned} to {latest}\n"
            f"{compare_url(pinned, latest)}\n"
            f"python3 scripts/sync_interfig.py update --commit {latest}"
        )
    print("No drift")


# --- React peer range ---------------------------------------------------------------------

_VERSION = re.compile(r"v?(\d+|[xX*])(?:\.(\d+|[xX*]))?(?:\.(\d+|[xX*]))?(?:[-+][0-9A-Za-z.+-]*)?")
_OP_SPACE = re.compile(r"(>=|<=|>|<|=|\^|~)\s+")
_COMPARATOR = re.compile(r"(\^|~|>=|<=|>|<|=)?(.+)")


def parse_partial(text: str) -> list[int]:
    """Parse `19`, `19.3`, `19.x`, `*` or `19.3.0` into its numeric prefix: [19], [19, 3], []..."""
    match = _VERSION.fullmatch(text)
    if not match:
        raise SyncError(f"Unsupported version {text!r}")
    parts = []
    for group in match.groups():
        if group is None or not group.isdigit():
            break
        parts.append(int(group))
    return parts


def pad(parts: list[int]) -> tuple[int, int, int]:
    return tuple((parts + [0, 0, 0])[:3])


def bump(parts: list[int], index: int) -> tuple[int, int, int]:
    head = parts[:index] + [parts[index] + 1]
    return pad(head)


def caret_upper(parts: list[int]) -> tuple[int, int, int]:
    """^1.2.3 < 2.0.0, ^0.2.3 < 0.3.0, ^0.0.3 < 0.0.4: the first non-zero part is fixed."""
    index = next((i for i, p in enumerate(parts) if p != 0), len(parts) - 1)
    return bump(parts, index)


def comparator_bounds(op: str, parts: list[int]) -> list[tuple[str, tuple[int, int, int]]]:
    """Translate one comparator into primitive (operator, version) bounds."""
    if not parts:
        return []
    if op in ("<=", ">") and len(parts) < 3:
        # <=19 admits every 19.x.y and >19 none of them: both bound at the next major/minor.
        return [("<" if op == "<=" else ">=", bump(parts, len(parts) - 1))]
    if op in (">=", "<=", ">", "<"):
        return [(op, pad(parts))]
    if op == "^":
        return [(">=", pad(parts)), ("<", caret_upper(parts))]
    if op == "~":
        return [(">=", pad(parts)), ("<", bump(parts, min(1, len(parts) - 1)))]
    if len(parts) == 3:
        return [("=", pad(parts))]
    return [(">=", pad(parts)), ("<", bump(parts, len(parts) - 1))]


_OPS = {
    ">=": lambda v, b: v >= b,
    "<=": lambda v, b: v <= b,
    ">": lambda v, b: v > b,
    "<": lambda v, b: v < b,
    "=": lambda v, b: v == b,
}


def satisfies_set(version: tuple[int, int, int], comparators: str) -> bool:
    tokens = bounded(_OP_SPACE.sub(r"\1", comparators).split(), 16, f"range {comparators!r}")
    for token in tokens:
        op, text = _COMPARATOR.fullmatch(token).groups()
        for bound_op, bound in comparator_bounds(op or "=", parse_partial(text)):
            if not _OPS[bound_op](version, bound):
                return False
    return True


def satisfies(version: str, spec: str) -> bool:
    """Evaluate an npm range: `||` alternatives of space-separated comparators.

    Hyphen ranges and anything else unrecognised raise, so an unreadable range fails closed.
    """
    if " - " in spec:
        raise SyncError(f"Unsupported hyphen range {spec!r}")
    target = pad(parse_partial(version))
    alternatives = bounded(spec.split("||"), 16, f"range {spec!r}")
    results = [satisfies_set(target, alt.strip()) for alt in alternatives]
    return any(results)


def local_react_version() -> str:
    with open(TOOLS_FIGURES / "package.json", encoding="utf-8") as f:
        pkg = json.load(f)
    pinned = pkg.get("devDependencies", {}).get("react") or pkg.get("dependencies", {}).get("react")
    if not pinned:
        raise SyncError("tools/figures/package.json declares no react dependency")
    return pinned.lstrip("^~=v")


def check_react_compat(fetcher: Fetcher, sha: str) -> None:
    upstream = fetch_json(fetcher, f"{RAW}/{sha}/{UPSTREAM_PATH}/package.json")
    peer = upstream.get("peerDependencies", {}).get("react") if isinstance(upstream, dict) else None
    if not peer:
        raise SyncError(f"Upstream {UPSTREAM_PATH}/package.json at {sha} declares no react peer range")
    local = local_react_version()
    if not satisfies(local, peer):
        raise SyncError(f"React version mismatch: upstream peer range {peer!r}, tools/figures react {local}")


# --- update -------------------------------------------------------------------------------


def upstream_log(fetcher: Fetcher, pinned: str, sha: str) -> list[str]:
    """One line per commit touching hindsight-interfig/ after `pinned`, up to `sha`, newest first.

    The path-filtered commit list stays small (about 8 KiB a commit), unlike compare/, whose
    file patches exceed the 1 MiB cap after a few days of monorepo traffic.
    """
    lines = []
    for commit in path_commits(fetcher, sha, LOG_PAGE)[:LOG_PAGE]:
        if commit["sha"] == pinned:
            return lines
        lines.append(f"  {commit['sha'][:8]} {commit['commit']['message'].splitlines()[0]}")
    lines.append(f"  (pin {pinned[:8]} is not among the newest {LOG_PAGE} commits; see the compare URL)")
    return lines


def print_upstream_log(fetcher: Fetcher, pinned: str, sha: str) -> None:
    """Best effort: upstream/ and vendor.json already hold the new pin, so a failed lookup
    only loses the list, never the update; the compare URL is printed either way."""
    compare = compare_url(pinned, sha)
    try:
        lines = upstream_log(fetcher, pinned, sha)
    except (SyncError, OSError, ValueError, LookupError, TypeError, AttributeError) as e:
        print(f"Upstream commit list unavailable ({e}); see {compare}")
        return
    print(f"Upstream commits under {UPSTREAM_PATH}/ since {pinned[:8]} ({compare}):")
    for line in lines:
        print(line)


def fetch_license(fetcher: Fetcher, vendor: dict, sha: str) -> bytes:
    body = fetch_ok(fetcher, f"{RAW}/{sha}/LICENSE")
    actual = sha256_bytes(body)
    if actual != vendor["license_sha256"]:
        raise SyncError(f"LICENSE changed! Expected {vendor['license_sha256']}, got {actual}")
    return body


def classify_entry(item: dict, vendor: dict) -> str | None:
    """Return the entry's directory to descend into, None for a listed file; raise otherwise."""
    rel = str(item.get("path", "")).removeprefix(f"{UPSTREAM_PATH}/")
    kind = item.get("type")
    if kind == "dir":
        return None if is_excluded(rel + "/", vendor["exclude"]) else rel
    if kind != "file":
        raise SyncError(f"Unsupported upstream entry type {kind!r}: {rel}")
    if rel not in vendor["include"] and not is_excluded(rel, vendor["exclude"]):
        raise SyncError(f"Unlisted new file: {rel}")
    return None


def check_listing(fetcher: Fetcher, vendor: dict, sha: str) -> None:
    """Walk src/ and scripts/ breadth-first; every file must be included or excluded."""
    pending = list(LISTED_DIRS)
    for _ in range(MAX_DIRS):
        if not pending:
            return
        directory = pending.pop(0)
        listing = fetch_json(fetcher, f"{API}/contents/{UPSTREAM_PATH}/{directory}?ref={sha}")
        if not isinstance(listing, list):
            raise SyncError(f"{UPSTREAM_PATH}/{directory} at {sha} is not a directory")
        for item in bounded(listing, MAX_FILES, f"{UPSTREAM_PATH}/{directory}"):
            subdir = classify_entry(item, vendor)
            if subdir:
                pending.append(subdir)
    if pending:
        raise SyncError(f"More than {MAX_DIRS} upstream directories under {', '.join(LISTED_DIRS)}")


def fetch_included(fetcher: Fetcher, vendor: dict, sha: str) -> dict[str, bytes]:
    files = {}
    for rel in bounded(vendor["include"], MAX_FILES, "vendor.json include"):
        files[rel] = fetch_ok(fetcher, f"{RAW}/{sha}/{UPSTREAM_PATH}/{rel}")
    return files


def safe_target(root: Path, rel: str) -> Path:
    pure = PurePosixPath(rel)
    if pure.is_absolute() or ".." in pure.parts:
        raise SyncError(f"Refusing to write outside upstream/: {rel}")
    return root.joinpath(*pure.parts)


def stage_files(files: dict[str, bytes]) -> Path:
    staged = VENDOR_DIR / "upstream.new"
    shutil.rmtree(staged, ignore_errors=True)
    for rel, body in files.items():
        target = safe_target(staged, rel)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(body)
    return staged


def updated_vendor(vendor: dict, files: dict[str, bytes], sha: str) -> dict:
    result = dict(vendor)
    result["commit"] = sha
    result["path_commit"] = sha
    result["fetched"] = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    result["files"] = {rel: sha256_bytes(files[rel]) for rel in sorted(files)}
    return result


def write_json_atomic(path: Path, data: dict) -> None:
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", newline="\n", dir=path.parent, suffix=".tmp", delete=False
    ) as f:
        json.dump(data, f, indent=2)
        f.write("\n")
        tmp = Path(f.name)
    try:
        os.replace(tmp, path)
    except OSError:
        tmp.unlink(missing_ok=True)
        raise


def swap_in(staged: Path, vendor: dict) -> None:
    """Replace upstream/ and vendor.json together; on failure restore the previous upstream/.

    rename(2) cannot replace a non-empty directory, so the old tree moves aside first.
    vendor.json is written last through os.replace, so it only changes once upstream/ has.
    """
    old = VENDOR_DIR / "upstream.old"
    shutil.rmtree(old, ignore_errors=True)
    had_upstream = UPSTREAM_DIR.exists()
    if had_upstream:
        os.replace(UPSTREAM_DIR, old)
    try:
        os.replace(staged, UPSTREAM_DIR)
        write_json_atomic(VENDOR_JSON, vendor)
    except OSError as e:
        rollback(old, had_upstream)
        raise SyncError(f"Could not install the new upstream/: {e}; previous tree restored") from e
    shutil.rmtree(old, ignore_errors=True)


def rollback(old: Path, had_upstream: bool) -> None:
    if not had_upstream:
        shutil.rmtree(UPSTREAM_DIR, ignore_errors=True)
        return
    if old.exists():
        shutil.rmtree(UPSTREAM_DIR, ignore_errors=True)
        os.replace(old, UPSTREAM_DIR)


def run_tool(argv: list[str]) -> None:
    exe = shutil.which(argv[0])
    if exe is None:
        raise SyncError(f"{argv[0]} not found on PATH")
    try:
        subprocess.run([exe, *argv[1:]], check=True, timeout=BUILD_TIMEOUT, cwd=REPO_ROOT)
    except subprocess.TimeoutExpired as e:
        raise SyncError(f"{' '.join(argv)} timed out after {BUILD_TIMEOUT}s") from e
    except (subprocess.CalledProcessError, OSError) as e:
        raise SyncError(f"{' '.join(argv)} failed: {e}") from e


def run_upstream_checks() -> None:
    tests = bounded(sorted(str(p) for p in (UPSTREAM_DIR / "src").glob("*.test.ts")), MAX_FILES, "upstream tests")
    try:
        run_tool(["node", "--test", *tests])
        run_tool(["npm", "--prefix", str(TOOLS_FIGURES), "run", "build"])
    except SyncError as e:
        raise SyncError(f"{e}\n{REVERT_HINT}") from e


def update(fetcher: Fetcher, sha: str) -> None:
    if not FULL_SHA.fullmatch(sha):
        # check compares path_commit with the API's full sha, so a short sha or branch name
        # pinned here would report drift forever.
        raise SyncError(f"--commit needs a full 40-character lowercase commit sha, got {sha!r}")
    vendor = load_vendor()
    if sha == vendor["path_commit"]:
        print("Commit matches current path_commit, no-op")
        return
    license_body = fetch_license(fetcher, vendor, sha)
    check_react_compat(fetcher, sha)
    check_listing(fetcher, vendor, sha)
    files = fetch_included(fetcher, vendor, sha)
    files["LICENSE"] = license_body
    staged = stage_files(files)
    try:
        swap_in(staged, updated_vendor(vendor, files, sha))
    finally:
        shutil.rmtree(staged, ignore_errors=True)
    run_upstream_checks()
    print_upstream_log(fetcher, vendor["path_commit"], sha)


# --- entry point --------------------------------------------------------------------------


def run(args: argparse.Namespace, fetcher: Fetcher) -> None:
    if args.command == "verify":
        verify()
    elif args.command == "check":
        check(fetcher)
    elif not args.commit:
        raise SyncError("update requires --commit <sha>")
    else:
        update(fetcher, args.commit)


def main(argv: list[str] | None = None, fetcher: Fetcher | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("command", choices=["verify", "check", "update"])
    parser.add_argument("--commit", help="upstream commit to vendor (update only)")
    args = parser.parse_args(argv)
    try:
        run(args, fetcher or Fetcher())
    except DriftError as e:
        print(e, file=sys.stderr)
        return EXIT_DRIFT
    except (SyncError, OSError, KeyError, ValueError, tomllib.TOMLDecodeError) as e:
        print(f"sync_interfig: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
