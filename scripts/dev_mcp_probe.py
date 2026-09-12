"""Behavioral wire probes. Only disposable fixtures are mutated."""

import hashlib
import json
from pathlib import Path
import tempfile

from dev_mcp_rpc import RPCClient

OUTPUTS = ("CLAUDE.md", ".cursor/rules/hiss-invariants.mdc",
           ".github/copilot-instructions.md", ".windsurfrules",
           ".gemini/GEMINI.md", ".codex/rules.md")
SENTINEL = "DEV_MCP_PREFLIGHT_SENTINEL"


def require(condition, message):
    if not condition:
        raise RuntimeError("MCP preflight failed: " + message)


def tool_text(response, *, error=False):
    require("error" not in response, f"unexpected JSON-RPC error: {response}")
    result = response.get("result", {})
    require(bool(result.get("isError")) == error,
            f"expected tool error={error}, received {result}")
    return "\n".join(item.get("text", "") for item in result.get("content", [])
                     if item.get("type") == "text")


def fixture_checks(client, root):
    """Prove observable reads, writes, readback, and rejection of false successes."""
    source = root / "AGENTS.md"
    source.write_text("# Agent instructions\n\nPreserve " + SENTINEL + ".\n")
    (root / "fixture.go").write_text("package fixture\nfunc DevMCPMarker() {}\n")
    text = tool_text(client.call("standards_inspect_symbols", {"path": "fixture.go"}))
    require("DevMCPMarker" in text, "inspection did not read the fixture symbol")
    tool_text(client.call("standards_compile_context", {}))
    for relative in OUTPUTS:
        target = root / relative
        require(target.is_file() and SENTINEL in target.read_text(),
                f"compile_context did not write canonical content to {relative}")
    tool_text(client.call("standards_compile_context", {"verify_only": True}))
    (root / "CLAUDE.md").write_text("drift\n")
    tool_text(client.call("standards_compile_context", {"verify_only": True}), error=True)
    require((root / "CLAUDE.md").read_text() == "drift\n", "verify_only changed content")
    tool_text(client.call("standards_inspect_symbols", {"path": 42}), error=True)
    tool_text(client.call("standards_inspect_symbols", {"path": "../"}), error=True)
    return ["fixture symbol read", "six compiled outputs read back",
            "context verify accepts sync and rejects drift without writes",
            "invalid argument and outside-root path rejected"]


def failure_checks(client, root):
    unknown = client.call("dev_mcp_nonexistent_tool", {})
    require("error" in unknown, "unknown tool returned a success envelope")
    memory = root / ".workingdir/memory/distilled.json"
    memory.parent.mkdir(parents=True)
    memory.write_text("{invalid json\n")
    result = tool_text(client.call("standards_memory_recall", {"query": "fixture"}), error=True)
    require("memory recall failed" in result, "malformed cache did not reach strict recall")
    (root / ".standards.yaml").write_text(
        'version: 1\nrepository:\n  owner: fixture\n  name: repo\nprofiles: [framework]\n')
    (root / ".standards.lock").write_text("{invalid json\n")
    result = tool_text(client.call("standards_audit", {}), error=True)
    require("lock" in result.lower(), "malformed lock was not the audit failure")
    return ["unknown tool rejected", "corrupt cache rejected", "malformed lock rejected"]


def audit_fixture(root):
    source = "id: framework\nname: Framework\n"
    digest = "sha256:" + hashlib.sha256(source.encode()).hexdigest()
    aggregate = hashlib.sha256(("profile:framework=" + digest + "\n").encode()).hexdigest()
    lock = {"version": 1, "pinned_version": "v1.0.0", "digest": "sha256:" + aggregate,
            "profiles": [{"id": "framework", "version": "v1.0.0", "digest": digest}]}
    files = {
        ".standards.yaml": 'version: 1\nrepository:\n  owner: fixture\n  name: repo\nprofiles: [framework]\n',
        ".standards.lock": json.dumps(lock),
        ".config/archetypes/framework.yaml": source,
        ".config/labels.yaml": "labels: []\n",
        ".github/rulesets/main.json": "{}\n",
        ".standards-baseline.json": '{"version":1,"total_infractions":0,"infractions":[]}\n',
    }
    for relative, content in files.items():
        target = root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
    return source


def audit_checks(client, root):
    source = audit_fixture(root)
    tool_text(client.call("standards_compile_context", {}))
    tool_text(client.call("standards_audit", {}))
    archetype = root / ".config/archetypes/framework.yaml"
    archetype.write_text(source + "description: changed\n")
    result = tool_text(client.call("standards_audit", {}), error=True)
    require("digest" in result.lower(), "audit did not detect changed pinned content")
    archetype.write_text(source)
    baseline = root / ".standards-baseline.json"
    recorded = baseline.read_bytes()
    violation = root / "violation.go"
    violation.write_text("package fixture\nfunc Broken() { panic(1) }\n")
    result = tool_text(client.call("standards_audit", {}), error=True)
    require("HISS" in result, "audit did not run invariant analysis")
    violation.write_text("package fixture\nfunc Broken() {" + "if true {" * 1100
                         + "panic(1);" + "}" * 1100 + "}\n")
    result = tool_text(client.call("standards_audit", {}), error=True)
    require("scan truncated" in result, "incomplete AST analysis was not rejected")
    require(baseline.read_bytes() == recorded, "audit changed the recorded baseline")
    violation.unlink()
    return ["valid fixture audit", "changed pinned content rejected",
            "invariant violation rejected", "incomplete scan rejected without baseline writes"]


def probe(binary, root, metadata):
    from dev_mcp import check_identity, server_command
    with RPCClient(server_command(binary, root)) as client:
        check_identity(client, metadata)
        response = client.request("tools/list")
        require("error" not in response, "tools/list failed")
        tools = response.get("result", {}).get("tools", [])
        require(0 < len(tools) <= 100, "empty or oversized tool inventory")
        names = [tool["name"] for tool in tools]
        required = {"standards_inspect_symbols", "standards_compile_context",
                    "standards_memory_recall", "standards_audit"}
        require(required <= set(names), "required tools are absent")
        inspected = tool_text(client.call("standards_inspect_symbols",
                                         {"path": "cmd/standards-mcp/main.go"}))
        require("runTransport" in inspected, "checkout inspection did not read the actual entry point")
    with tempfile.TemporaryDirectory(prefix="praetor-mcp-fixture-") as directory:
        fixture = Path(directory)
        with RPCClient(server_command(binary, fixture)) as client:
            check_identity(client, metadata)
            checks = fixture_checks(client, fixture) + audit_checks(client, fixture)
            checks += failure_checks(client, fixture)
    return {"passed": ["source identity", "tool discovery", "checkout symbol read"] + checks,
            "tools": names, "mutations": "temporary fixtures only"}
