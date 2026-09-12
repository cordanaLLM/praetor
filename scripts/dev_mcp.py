#!/usr/bin/env python3
"""Build this checkout and expose its MCP over stdio or a direct JSON-RPC call."""

import argparse
import hashlib
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

sys.dont_write_bytecode = True

from dev_mcp_rpc import RPCClient

ROOT = Path(__file__).resolve().parent.parent
BUILD_TIMEOUT = 180
SESSION_TIMEOUT = 12 * 60 * 60
MAX_SOURCE_FILES = 20_000
MAX_FILE_BYTES = 32 * 1024 * 1024


def command_output(command, timeout=10):
    """Run a bounded, shell-free build/inventory command."""
    return subprocess.run(command, cwd=ROOT, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=timeout).stdout


def source_hash():
    """Fingerprint tracked and untracked Go sources and module inputs, including tests."""
    paths = command_output([
        "git", "ls-files", "--cached", "--others", "--exclude-standard", "-z",
        "--", "*.go", "go.mod", "go.sum",
    ]).decode().split("\0")
    paths = sorted(set(path for path in paths if path))
    if not paths or len(paths) > MAX_SOURCE_FILES:
        raise RuntimeError("Go source inventory is empty or exceeds its bound")
    digest = hashlib.sha256()
    for relative in paths:
        path = ROOT / relative
        # A tracked deletion is part of the working tree identity too.
        data = read_bounded(path) if path.exists() else b"<deleted>"
        digest.update(relative.encode() + b"\0" + hashlib.sha256(data).digest())
    return digest.hexdigest()


def read_bounded(path):
    with path.open("rb") as stream:
        data = stream.read(MAX_FILE_BYTES + 1)
    if len(data) > MAX_FILE_BYTES:
        raise RuntimeError(f"File exceeds development provenance bound: {path}")
    return data


def build(directory):
    """Always build; Go's own cache handles reuse without trusting an old bin/ artifact."""
    source = source_hash()
    version = "dev-" + source
    binary = directory / "praetor-mcp"
    command_output(["go", "build", "-ldflags", "-X main.mcpVersion=" + version,
                    "-o", str(binary), "./cmd/standards-mcp"], BUILD_TIMEOUT)
    if source_hash() != source:
        raise RuntimeError("Go sources changed during build; rerun the preflight")
    metadata = {
        "checkout": str(ROOT), "source_sha256": source,
        "binary_sha256": hashlib.sha256(read_bounded(binary)).hexdigest(),
        "git_head": command_output(["git", "rev-parse", "HEAD"]).decode().strip(),
        "git_dirty": bool(command_output(["git", "status", "--porcelain"])),
        "go_version": command_output(["go", "version"]).decode().strip(),
        "server_version": version,
    }
    return binary, metadata


def server_command(binary, root, allow_remote=False):
    command = [str(binary), "--transport", "stdio", "--root", str(root)]
    if allow_remote:
        command.append("--allow-remote-benchmarks")
    return command


def check_identity(client, metadata):
    actual = client.initialize_response.get("result", {}).get("serverInfo", {})
    if actual.get("version") != metadata["server_version"]:
        raise RuntimeError("MCP server version does not match the source fingerprint")


def failed(response):
    return "error" in response or response.get("result", {}).get("isError", False)


def direct_call(binary, root, metadata, name, arguments, allow_remote=False, timeout=30):
    with RPCClient(server_command(binary, root, allow_remote), timeout=timeout) as client:
        check_identity(client, metadata)
        response = client.call(name, arguments)
    if source_hash() != metadata["source_sha256"]:
        raise RuntimeError("Go sources changed during the call; rerun against the new build")
    print(json.dumps({"provenance": metadata, "response": response}, indent=2))
    return 1 if failed(response) else 0


def rpc_timeout(value):
    seconds = int(value)
    if not 1 <= seconds <= 300:
        raise argparse.ArgumentTypeError("RPC timeout must be 1..300 seconds")
    return seconds


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    for action in ("serve", "probe", "call"):
        child = actions.add_parser(action)
        child.add_argument("--root", type=Path, default=ROOT,
                           help="Confined server root (default: this checkout)")
        child.add_argument("--allow-remote-benchmarks", action="store_true",
                           help="Explicitly enable configured public repository clones")
        if action == "call":
            child.add_argument("--timeout", type=rpc_timeout, default=30,
                               metavar="SECONDS", help="RPC deadline, 1..300 seconds (default: 30)")
            child.add_argument("tool", help="Name from the MCP tools/list response")
            child.add_argument("arguments", help="Tool arguments as a JSON object")
    args = parser.parse_args()
    if args.action == "probe" and args.allow_remote_benchmarks:
        parser.error("--allow-remote-benchmarks applies to serve/call only")
    args.root = args.root.resolve(strict=True)
    if not args.root.is_dir():
        parser.error("--root must name a directory")
    if args.action == "call":
        if len(args.arguments.encode()) > 65_536:
            parser.error("tool arguments exceed 64 KiB")
        args.arguments = json.loads(args.arguments)
        if not isinstance(args.arguments, dict):
            parser.error("tool arguments must be a JSON object")
    return args


def main():
    args = parse_args()
    with tempfile.TemporaryDirectory(prefix="praetor-dev-mcp-") as directory:
        binary, metadata = build(Path(directory))
        if args.action == "serve":
            print(json.dumps({"dev_mcp": metadata}), file=sys.stderr, flush=True)
            return subprocess.run(server_command(binary, args.root, args.allow_remote_benchmarks),
                                  timeout=SESSION_TIMEOUT, check=False).returncode
        if args.action == "call":
            return direct_call(binary, args.root, metadata, args.tool, args.arguments,
                               args.allow_remote_benchmarks, args.timeout)
        from dev_mcp_probe import probe
        report = probe(binary, args.root, metadata)
        if source_hash() != metadata["source_sha256"]:
            raise RuntimeError("Go sources changed during preflight; rerun it")
        print(json.dumps({"provenance": metadata, "checks": report}, indent=2))
        return 0


def terminate(_signal, _frame):
    # subprocess.run kills and waits for its child when this exception unwinds it.
    raise SystemExit(143)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, terminate)
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        print(f"Development MCP failed: {error}", file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr.decode(errors="replace")[:8192], file=sys.stderr)
        sys.exit(1)
