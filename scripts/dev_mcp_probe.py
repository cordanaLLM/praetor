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


def transcript_checks(client, root):
    event = {"step_index": 1, "source": "MODEL", "type": "MESSAGE", "status": "DONE",
             "created_at": "2026-09-12T12:00:00Z", "content": "fixture observed event",
             "thinking": "fixture excluded thinking"}
    source = root / "transcript_full.jsonl"
    source.write_text((json.dumps(event) + "\n") * 2)
    args = {"source_path": source.name, "cache_dir": "events", "max_records": 1}
    first = json.loads(tool_text(client.call("standards_transcript_ingest", args)))["report"]
    require(first["stored"] == 1 and not first["complete"] and first["remaining"] == 1,
            "transcript ingestion did not expose the remaining page")
    cursor = first["next_cursor"]
    invalid = dict(args, cursor=cursor, cache_dir="other-events")
    tool_text(client.call("standards_transcript_ingest", invalid), error=True)
    second = json.loads(tool_text(client.call("standards_transcript_ingest", dict(args, cursor=cursor))))["report"]
    require(second["complete"] and second["stored"] == 1, "transcript continuation lost records")
    replay = json.loads(tool_text(client.call("standards_transcript_ingest", args)))["report"]
    require(replay["stored"] == 0 and replay["already_present"] == 1,
            "transcript replay did not deduplicate")
    records = list((root / "events").glob("*.json"))
    require(len(records) == 2, "transcript records missing or duplicated on disk")
    for path in records:
        observed = json.loads(path.read_text())
        require(observed["source_sha256"] == first["source"]["sha256"]
                and "thinking" not in observed, "transcript provenance or payload filter failed")
    return ["transcript pages persisted and read back", "transcript replay deduplicated",
            "transcript cursor rejected for another cache"]


def claude_transcript_checks(client, root):
    record = {"type": "assistant", "uuid": "message-id", "sessionId": "session-id",
              "timestamp": "2026-09-12T12:00:00Z", "message": {"role": "assistant",
              "content": [{"type": "thinking", "thinking": "EXCLUDED_CLAUDE_THINKING"},
                          {"type": "text", "text": "CLAUDE_OBSERVED_PAYLOAD"}]}}
    source = root / "claude-session.jsonl"
    source.write_text(json.dumps(record) + "\n")
    args = {"source_path": source.name, "cache_dir": "claude-events"}
    tool_text(client.call("standards_transcript_ingest", args), error=True)
    args["format"] = "claude-code-jsonl-v1"
    first = json.loads(tool_text(client.call("standards_transcript_ingest", args)))["report"]
    require(first["complete"] and first["stored"] == 1 and first["thinking_blocks"] == 1,
            "Claude adapter did not preserve the supported message")
    replay = json.loads(tool_text(client.call("standards_transcript_ingest", args)))["report"]
    require(replay["stored"] == 0 and replay["already_present"] == 1,
            "Claude replay did not deduplicate")
    files = list((root / "claude-events").glob("*.json"))
    require(len(files) == 1, "Claude cache record count changed")
    data = files[0].read_text()
    observed = json.loads(data)
    require(observed["content"] == "CLAUDE_OBSERVED_PAYLOAD" and "EXCLUDED_CLAUDE_THINKING" not in data
            and observed["source_format"] == args["format"] and observed["record_uuid"] == record["uuid"]
            and observed["source_sha256"] == first["source"]["sha256"],
            "Claude provenance or thinking exclusion failed")
    require("CLAUDE_OBSERVED_PAYLOAD" not in json.dumps(first), "Claude report leaked payload")
    record["message"]["content"] = [{"type": "unsupported-future-block"}]
    source.write_text(json.dumps(record) + "\n")
    args["cache_dir"] = "claude-invalid-events"
    tool_text(client.call("standards_transcript_ingest", args), error=True)
    require(not (root / args["cache_dir"]).exists(), "invalid Claude source wrote a cache")
    return ["Claude explicit format and cache readback", "Claude replay deduplicated",
            "Claude unsupported blocks rejected before writes"]


def context_checks(client, root):
    """Verify reduction and fail-closed bounds through the actual read-only tool."""
    sources = ["AGENTS.md", *OUTPUTS]
    before = {name: (root / name).read_bytes() for name in sources}
    report = json.loads(tool_text(client.call("standards_context_analyze", {"sources": sources})))
    require(len(report["documents"]) == 1 and report["saved_bytes"] > 0,
            "context analyzer did not identify current compiler projections")
    require(report["review_required"] and SENTINEL not in json.dumps(report),
            "context analysis leaked payload or claimed automatic activation")
    require(all((root / name).read_bytes() == data for name, data in before.items()),
            "read-only context analysis changed sources")
    (root / "CLAUDE.md").write_bytes(before["CLAUDE.md"] + b"\nKeep this additional rule.\n")
    drift = json.loads(tool_text(client.call("standards_context_analyze", {"sources": sources})))
    require(len(drift["documents"]) == 2, "context analyzer discarded changed policy")
    for args in ({"sources": []}, {"sources": ["../AGENTS.md"]},
                 {"sources": ["AGENTS.md"], "output_dir": "never-write"}):
        tool_text(client.call("standards_context_analyze", args), error=True)
    bounded = [f"context-source-{index}.md" for index in range(64)]
    for name in bounded:
        (root / name).write_text("private duplicate fixture\n" * 100)
    exact = json.loads(tool_text(client.call("standards_context_analyze", {"sources": bounded})))
    require(len(exact["sources"]) == 64 and len(exact["documents"]) == 1,
            "exact source bound was not fully analyzed")
    tool_text(client.call("standards_context_analyze", {"sources": bounded + ["extra.md"]}), error=True)
    (root / bounded[0]).write_bytes(b"x" * (1 << 20))
    tool_text(client.call("standards_context_analyze", {"sources": bounded[:1]}))
    (root / bounded[0]).write_bytes(b"x" * ((1 << 20) + 1))
    tool_text(client.call("standards_context_analyze", {"sources": bounded[:1]}), error=True)
    return ["context compiler projections reduced without writes or payload disclosure",
            "context drift retained", "context invalid and outside paths rejected",
            "context 64-source boundary enforced", "context 1 MiB boundary enforced"]


def suite_checks(client, root):
    folder = root / "suite-input"
    folder.mkdir()
    source = folder / "transcript_full.jsonl"
    record = {"step_index": 1, "source": "MODEL", "type": "MESSAGE", "status": "DONE",
              "created_at": "2026-09-12T12:00:00Z", "content": "private suite payload"}
    original = ((json.dumps(record) + "\n") * 2).encode()
    source.write_bytes(original)
    config = {"version": 1, "public_repositories": [], "transcripts": [
        {"id": "fixture", "source_path": str(source), "sha256": hashlib.sha256(original).hexdigest(),
         "format": "antigravity-jsonl-v1"}]}
    path = root / "suite.json"
    path.write_text(json.dumps(config))
    args = {"config_path": path.name, "artifact_dir": "suite-plan"}
    planned = json.loads(tool_text(client.call("standards_dogfood_suite", args)))["report"]
    require(planned["status"] == "planned" and not planned["verified"]
            and "ingestion" not in planned["cases"][0], "suite plan executed or claimed verification")
    args.update(stage="verify", artifact_dir="suite-verified")
    report = json.loads(tool_text(client.call("standards_dogfood_suite", args)))["report"]
    case = report["cases"][0]
    require(report["verified"] and case["ingestion"]["stored"] == 2
            and case["replay"]["stored"] == 0 and case["replay"]["already_present"] == 2,
            "suite did not finish and replay its actual cache")
    require(source.read_bytes() == original and "private suite payload" not in json.dumps(report),
            "suite changed or disclosed original payload")
    require(len(list((root / "suite-verified/case-01/events").glob("*.json"))) == 2,
            "suite cache was not persisted")
    config["transcripts"][0]["sha256"] = "0" * 64
    path.write_text(json.dumps(config))
    args["artifact_dir"] = "suite-failed"
    failed = json.loads(tool_text(client.call("standards_dogfood_suite", args), error=True))["report"]
    require(not failed["verified"] and failed["cases"][0]["status"] == "failed"
            and (root / "suite-failed/report.json").is_file(), "suite lost failed-case evidence")
    config["transcripts"][0]["source_path"] = str(root.parent / "outside.jsonl")
    path.write_text(json.dumps(config))
    args.update(stage="plan", artifact_dir="suite-outside")
    tool_text(client.call("standards_dogfood_suite", args), error=True)
    require(not (root / "suite-outside").exists(), "embedded path escaped suite confinement")
    config.update(transcripts=[], public_repositories=["https://github.com/spf13/cobra#" + "a" * 40])
    path.write_text(json.dumps(config))
    args.update(stage="verify", artifact_dir="suite-remote")
    tool_text(client.call("standards_dogfood_suite", args), error=True)
    require(not (root / "suite-remote").exists(), "suite bypassed server remote opt-in")
    return ["suite plan remains unverified", "suite complete ingestion and same-cache replay read back",
            "suite failures retain evidence", "suite embedded paths confined", "suite remote opt-in enforced"]


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
                    "standards_memory_recall", "standards_audit", "standards_transcript_ingest",
                    "standards_context_analyze", "standards_dogfood_suite"}
        require(required <= set(names), "required tools are absent")
        inspected = tool_text(client.call("standards_inspect_symbols",
                                         {"path": "cmd/standards-mcp/main.go"}))
        require("runTransport" in inspected, "checkout inspection did not read the actual entry point")
    with tempfile.TemporaryDirectory(prefix="praetor-mcp-fixture-") as directory:
        fixture = Path(directory)
        with RPCClient(server_command(binary, fixture)) as client:
            check_identity(client, metadata)
            checks = fixture_checks(client, fixture) + audit_checks(client, fixture)
            checks += context_checks(client, fixture)
            checks += failure_checks(client, fixture)
            checks += transcript_checks(client, fixture)
            checks += claude_transcript_checks(client, fixture)
            checks += suite_checks(client, fixture)
    return {"passed": ["source identity", "tool discovery", "checkout symbol read"] + checks,
            "tools": names, "mutations": "temporary fixtures only"}
