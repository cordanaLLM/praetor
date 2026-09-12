#!/usr/bin/env python3
"""Inspect native Codex hooks or trust one explicitly reviewed definition hash."""

import argparse
import json
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True

from dev_mcp_rpc import RPCClient, RPCError


class CodexClient(RPCClient):
    """Codex 0.145.0 handshake over the same bounded stdio transport as MCP."""

    def _initialize_params(self):
        return {"clientInfo": {"name": "praetor-hook-bootstrap", "version": "1.0.0"},
                "capabilities": {"experimentalApi": True}}

    def _initialized_notification(self):
        return {"method": "initialized"}

    def _validate_initialize(self):
        result = checked_result(self.initialize_response)
        if not isinstance(result.get("userAgent"), str) or not result["userAgent"]:
            raise RPCError("Codex initialize returned no userAgent")

    @staticmethod
    def _validate_envelope(message):
        if not isinstance(message, dict) or message.get("jsonrpc", "2.0") != "2.0":
            raise RPCError("Invalid Codex RPC envelope")


def checked_result(response):
    if "error" in response:
        raise RPCError(f"Codex request failed: {response['error']}")
    result = response.get("result")
    if not isinstance(result, dict):
        raise RPCError("Codex response result must be an object")
    return result


def project_hooks(client, root):
    """Reject discovery errors instead of reporting an empty successful inventory."""
    result = checked_result(client.request("hooks/list", {"cwds": [str(root)]}))
    rows = result.get("data")
    if not isinstance(rows, list) or len(rows) != 1:
        raise RPCError("Expected exactly one Codex hook inventory")
    row = rows[0]
    if not isinstance(row, dict) or row.get("cwd") != str(root):
        raise RPCError("Codex hook inventory has a different working directory")
    if row.get("errors") or row.get("warnings"):
        raise RPCError("Codex hook discovery reported errors or warnings")
    hooks = row.get("hooks")
    if not isinstance(hooks, list) or len(hooks) > 1024:
        raise RPCError("Invalid or oversized Codex hook inventory")
    if not all(isinstance(hook, dict) for hook in hooks):
        raise RPCError("Invalid Codex hook entry")
    source = str(root / ".codex/hooks.json")
    return [hook for hook in hooks if hook.get("sourcePath") == source]


def trust_hook(client, root, key, expected_hash):
    """Use Codex's native exact-hash trust write; never enable or trust other hooks."""
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", expected_hash):
        raise ValueError("Expected a reviewed sha256:<64 lowercase hex> definition hash")
    hooks = project_hooks(client, root)
    selected = [hook for hook in hooks if hook.get("key") == key]
    if len(selected) != 1:
        raise RPCError("The reviewed project hook key is missing or ambiguous")
    hook = selected[0]
    if hook.get("currentHash") != expected_hash:
        raise RPCError("Hook definition changed; review the new hash before trusting it")
    if hook.get("enabled") is not True or hook.get("handlerType") != "command":
        raise RPCError("Only enabled project command hooks can be trusted here")
    checked_result(client.request("config/batchWrite", {
        "edits": [{"keyPath": "hooks.state", "value": {key: {"trusted_hash": expected_hash}},
                   "mergeStrategy": "upsert"}], "reloadUserConfig": True,
    }))
    after = project_hooks(client, root)
    verified = [item for item in after if item.get("key") == key]
    if (len(verified) != 1 or verified[0].get("trustStatus") != "trusted"
            or verified[0].get("currentHash") != expected_hash):
        raise RPCError("Native hook trust readback failed; inspect the current configuration")
    return verified[0]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("status")
    trust = sub.add_parser("trust")
    trust.add_argument("--key", required=True)
    trust.add_argument("--expected-hash", required=True)
    args = parser.parse_args()
    root = args.root.resolve(strict=True)
    with CodexClient(["codex", "app-server", "--stdio"], timeout=30) as client:
        if args.action == "status":
            result = {"root": str(root), "hooks": project_hooks(client, root)}
        else:
            result = trust_hook(client, root, args.key, args.expected_hash)
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RPCError) as error:
        print(f"Codex hook bootstrap failed: {error}", file=sys.stderr)
        sys.exit(1)
