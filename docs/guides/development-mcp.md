# Develop against this checkout's MCP

Run from the repository root with Python 3, Git, and the Go version required by
`go.mod` available on `PATH`:

```bash
python3 scripts/dev_mcp.py probe
```

This builds the current Go sources into a temporary binary, initializes its real
stdio MCP transport, and checks `serverInfo.version` against
`provenance.server_version` (`dev-<source_sha256>`). The report includes the checkout,
source and binary hashes, Git revision, dirty state, and Go version. Keep that
provenance with defect evidence. A source change during the build or probe fails
the run; retry against the completed edit.

The probe checks checkout symbol inspection, canonical context compilation with
all six outputs read back, verification drift, invalid arguments, path escape,
unknown tools, corrupt memory, and malformed locks. It also audits a valid fixture
before checking changed pinned content, invariant violations, and incomplete scans
that must fail without changing the baseline. All mutations use disposable
fixtures. A discovered tool name alone does not prove working behavior.

## Native project connections

The tracked configs register `praetor-dev`; they contain no machine-specific paths
or credentials. Both run `scripts/dev_mcp.py serve`, which builds once per server
startup. Existing native connections keep that binary after source edits. Restart
or reconnect the server before testing the new code, or use the fresh direct
commands below. Check the server's initialization version against a fresh probe's
`provenance.server_version`; a matching Git commit alone misses uncommitted edits.

**Codex:** open this trusted checkout in Codex. Its project `.codex/config.toml`
registers the server with 120-second startup and tool timeouts. The launcher resolves
the Git worktree root from the session directory, so nested starts work. Verify the
loaded configuration with `codex mcp get praetor-dev --json`; use `/mcp` to inspect
the connection. Restart the CLI session or IDE extension to load a new server.
Project config and these MCP options are documented in the
[Codex MCP reference](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

**Claude Code:** open the checkout and enable its project `.mcp.json` server when
Claude presents the project approval. Check `claude mcp get praetor-dev` and use
`/mcp` to reconnect after edits. The launcher expands `CLAUDE_PROJECT_DIR` inside
the child shell; this documented server environment variable avoids depending on
Claude's launch directory. Allow cold Go builds with `MCP_TIMEOUT=120000 claude`;
the checked-in per-tool timeout is 120 seconds. See the
[Claude Code MCP reference](https://code.claude.com/docs/en/mcp).

Configuration parsing was checked with Codex CLI 0.145.0 and Claude Code 2.1.259.
Codex's real app-server tool-call path was checked from the repository root and a
nested directory. Claude's config was recognized as project-scoped, and its exact
launcher completed a handshake and symbol read from an unrelated directory with
the documented child environment. Its native connection remains subject to
Claude's project approval. If a client cannot load the project connection, record
that limitation and use the direct wire client.

## Reproduce through a real tool

Each direct call fresh-builds the checkout, verifies the server identity, and
prints its JSON-RPC response with provenance. Tool errors and protocol errors
produce a nonzero exit code:

```bash
python3 scripts/dev_mcp.py call standards_inspect_symbols \
  '{"path":"cmd/standards-mcp/main.go"}'
```

Use a temporary server root for mutation cases. `--root` follows the subcommand
and changes the tool's confined filesystem root; the code still comes from this
checkout. This example checks a real write, readback, and verification:

```bash
python3 - <<'PY'
import json
from pathlib import Path
import subprocess
import tempfile

with tempfile.TemporaryDirectory(prefix="praetor-mcp-case-") as directory:
    root = Path(directory)
    (root / "AGENTS.md").write_text("# Fixture\n\nPreserve fixture-policy.\n")
    command = ["python3", "scripts/dev_mcp.py", "call", "--root", directory,
               "standards_compile_context"]
    subprocess.run(command + ["{}"], check=True)
    assert "fixture-policy" in (root / "CLAUDE.md").read_text()
    subprocess.run(command + [json.dumps({"verify_only": True})], check=True)
PY
```

For each defect, retain the input, expected behavior, actual response, provenance,
and filesystem evidence. Exercise positive, negative, and boundary behavior of the
affected tool. Report placeholder results, missing operations, and blocked
connections explicitly; the smoke probe does not establish correctness of every
tool. Run the relevant code tests and `make verify-all` after implementing the fix.
