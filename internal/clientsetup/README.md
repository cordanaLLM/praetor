# Client configuration projections

`DecodeRegistry` and `BuildPlan` provide one stdio registry for the supported
coding clients. They operate on explicit bytes and return review artifacts;
writing settings, invoking native commands, reading credentials, granting trust,
checking discovery and executing tools belong to the caller. The registry never
contains environment values. Executable paths are absolute; arguments remain
separate literal strings without environment or file substitution.

`SourceSHA256` binds the candidate to the supplied existing bytes.
`RegistrySHA256` identifies the validated registry sorted by server name.
`Content` is excluded from JSON metadata because existing settings can contain
private values. Unrelated settings and matching servers' access options remain
intact. A same-name command, argument or transport conflict stops preparation.
Idempotent merges return the original bytes. JSON inputs must be strict JSON;
JSONC comments, YAML aliases, duplicate keys and multiple documents are rejected
explicitly. Do not silently convert an existing JSONC configuration to JSON.

Adapter schemas were verified on 2026-09-12:

| Adapter | Artifact or native operation | Source |
| --- | --- | --- |
| `codex` | TOML export plus `codex mcp add` argv; native CLI owns arbitrary TOML updates | [Codex MCP](https://developers.openai.com/codex/mcp/) and installed Codex 0.145.0 help |
| `claude` | `.mcp.json`, `mcpServers`, explicit stdio type | [Claude Code MCP](https://code.claude.com/docs/en/mcp) |
| `gemini` | `.gemini/settings.json`, `mcpServers` | [Gemini CLI MCP](https://geminicli.com/docs/tools/mcp-server/) |
| `opencode-v1` | `opencode.json`, direct `mcp` server map, local command array | [OpenCode MCP](https://opencode.ai/docs/mcp-servers/) and [schema](https://opencode.ai/config.json) |
| `continue` | `.continue/mcpServers/praetor.yaml`, schema v1 block metadata and server sequence | [Continue MCP](https://docs.continue.dev/customize/deep-dives/mcp) |
| `cline` | Explicit JSON export, `mcpServers`; caller selects the actual profile path | [Cline MCP](https://docs.cline.bot/mcp/mcp-overview) |
| `kilo` | Explicit JSON export, direct `mcp` server map and local command array; caller selects the actual profile path | [Kilo MCP](https://kilo.ai/docs/automate/mcp/using-in-kilo-code) |
| `agy` | Native `agy mcp add --type stdio NAME -- COMMAND ARGS...` argv | Installed AGY 1.2.2 `agy mcp add --help` |

OpenCode v2 uses a different `mcp.servers` schema. The `opencode-v1` identifier is
intentional and does not imply support for that newer schema. Cline and Kilo are
separate projections. Existing filenames previously guessed by other modules
are not proof that an installed client reads them.

Bounds are 32 servers, 64 arguments per server, 4096 bytes per command/argument,
256 KiB of registry values, 1 MiB of input configuration and 32 nesting levels.
Candidates are bounded to 2 MiB; callers with a smaller file-write ceiling must
reject larger candidates before mutation. Native plans do not know current
client state and therefore mark `Changed` true; inspect and compare that state
before applying them. These artifacts configure tool availability. They do not
prove that an agent used those tools or that model dispatch and caps are enforced.
