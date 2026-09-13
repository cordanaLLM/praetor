# Agent lifecycle coverage

Praetor has a shared repository policy and client-specific lifecycle adapters.
Keep those layers separate when installing or reviewing an integration.

## Shared contracts

Git invokes the installed Lefthook jobs for every coding agent and for human
contributors. `agent-pre-tool`, `agent-state-stop`, `agent-checkpoint-tool`, and
`agent-checkpoint-stop` are explicit, bounded jobs. Any client can invoke these
jobs through its command tool while a native lifecycle adapter is unavailable.
The jobs do not stage, commit, push, publish, grant trust, or prove that an
agent used an MCP tool.

The repository's `AGENTS.md` and compiled vendor context provide the same
fallback instructions to supported agent environments. Context projection is
configuration delivery; it is not lifecycle enforcement.

Native Stop/AfterAgent first requires `agent-state-stop` to verify that the
existing private ledger has a fresh state snapshot. This calls the shared CLI
through the Git-hook entry point and requires its structured success marker.
Missing, malformed, stale, or unverifiable state requests continuation even when
checkpoint publication policy is disabled. Repair the ledger and run
`praetorctl state sync .` before retrying. Stop does not silently synchronize it.
The state probe has a 20-second budget and the subsequent checkpoint observation
has 30 seconds, within the native adapter's 60-second timeout. A slow or missing
CLI is an explicit verification failure.

## Native adapter coverage

The client projection modes are defined in `internal/clientsetup`. Native
lifecycle configuration and runtime qualification are separate:

| Client | MCP projection | Native lifecycle definition | Events and current qualification |
| --- | --- | --- | --- |
| Codex | native command/export | `.codex/hooks.json` | Bash `PreToolUse`, `PostToolUse`, `Stop`; configuration and process behavior tested, session activation requires trust/readback |
| Claude Code | merge `.mcp.json` | `.claude/settings.json` | Bash command guard and `Edit`/`Write` batch-scope `PreToolUse`, all `PostToolUse`, `Stop`; configuration and process behavior tested, session activation requires approval/reload/readback |
| Gemini CLI | merge `.gemini/settings.json` | `.gemini/settings.json` | `run_shell_command` command guard and `replace`/`write_file` batch-scope `BeforeTool`, all `AfterTool`, `AfterAgent`; configuration and process behavior tested, session activation requires approval/reload/readback |
| OpenCode v1 | merge `opencode.json` | not implemented | Projection and conflict tests only |
| Continue | merge `.continue/mcpServers/praetor.yaml` | not implemented | Projection and conflict tests only |
| Cline | export JSON | not implemented | Export and conflict tests only |
| Kilo | export JSON | not implemented | Export and conflict tests only |
| AGY | native command/export | not implemented | Native command plan tests only |

All three native adapters invoke the shared `command_guard.py` and
`checkpoint.py`; Codex reaches the guard through its compatibility wrapper. The command
timeouts are 15/60 seconds for Codex and Claude, and 15,000/60,000 milliseconds
for Gemini. These adapters are configured and their processes/Lefthook jobs are
tested; no checked-in configuration proves that every native session loaded,
trusted, reloaded, or exercised the hooks.

Cursor, Windsurf, and Copilot currently receive compiled repository context
where configured, but have no Praetor native lifecycle or MCP adapter claim.
Projection tests cover schemas and conflict handling; native discovery, trust,
handshake, and tool readback must be recorded per installed client.

## Review sequence

1. Verify the shared Lefthook installation and run the relevant job directly.
2. Inspect the client adapter's generated or native command plan and its exact
   destination; retain no credentials in plans or repository files.
3. Complete the client's native approval/trust and reload step.
4. Read back initialization, tool discovery, and one harmless named tool call.
5. Exercise a native before-tool event and checkpoint event in an isolated
   repository. Verify rejection of a disallowed proposed command without
   executing it, clean/dirty checkpoint outcomes, and unchanged private files.
6. Record client, version, session, source hash, and result privately. Treat a
   missing or untested adapter as unavailable rather than as a passing setup.

See [Git hooks](git-hooks.md), [checkpoint cadence](checkpoint-cadence.md), and
[client bootstrap](client-bootstrap.md) for the detailed contracts.
