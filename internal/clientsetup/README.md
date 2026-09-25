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

`PlanAGYPermissions` is the separate native-settings projection for AGY 1.2.7.
It appends exact operator-declared `permissions.allow` rules, preserves unknown
root and permission keys plus existing allow/ask/deny entries, and returns a
secret-free count/delta alongside candidate bytes excluded from JSON metadata.
It refuses a declared grant that intersects a higher-precedence deny/ask rule by
exact or action-wide match, literal command prefix, recursive file scope, URL
domain/subdomain, or MCP server wildcard. Declared URL targets must be bare
canonical hosts; existing URL rules are reduced to their host before comparison,
so a scheme, userinfo, port, IPv6 brackets or path cannot hide an overlap, and an
existing URL rule that does not reduce to a canonical host fails closed. A deny `read_file` scope also conflicts
with an intersecting `write_file` allow. Same-action regex rules fail closed when
non-overlap cannot be proved; the adapter does not claim regex equivalence. Mixed
absolute and workspace-relative file rules also fail closed because static planning
does not know the future runtime workspace root. Native
permission readback remains required. An absent target plus an empty declaration
produces metadata only, never a zero-byte JSON candidate.
It performs no filesystem I/O; the command layer binds its plan to one resolved
target and publishes through the same snapshot, backup, CAS replacement and
readback path as other client settings.

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
| `agy` | `.agents/mcp_config.json` (workspace) or `<config root>/mcp_config.json` (host), `mcpServers`, stdio entries as `command`/`args` without a `type` key; native CLI grants are separately appended to `$HOME/.gemini/antigravity-cli/settings.json` | [Antigravity plugins](https://antigravity.google/docs/plugins), [CLI permissions](https://antigravity.google/docs/permissions?tab=cli); MCP shape measured on 2026-09-17 with AGY 1.2.5 and permission shape rechecked on 2026-09-20 with AGY 1.2.7 |

OpenCode v2 uses a different `mcp.servers` schema. The `opencode-v1` identifier is
intentional and does not imply support for that newer schema. Cline and Kilo are
separate projections. Existing filenames previously guessed by other modules
are not proof that an installed client reads them.

`Root`, `Resolve`, `Locate`, `BrainRoots` and `ExistingBrainRoots` (`roots.go`)
resolve the locations that differ per operating system from an injected `Env`. The
caller supplies the operating system, home, `APPDATA`, `LOCALAPPDATA`,
`XDG_CONFIG_HOME`, a variable reader and a directory probe; the package still
discovers nothing itself, and paths are joined with the separator of the named
operating system rather than the host's. `Resolution.Verified` is false for a root
chosen through a relocation variable that has not been observed in a client binary.
The [client bootstrap guide](../../docs/guides/client-bootstrap.md) holds the table.

Bounds are 32 servers, 64 arguments per server, 4096 bytes per command/argument,
256 KiB of registry values, 1 MiB of input configuration and 32 nesting levels.
Candidates are bounded to 2 MiB; callers with a smaller file-write ceiling must
reject larger candidates before mutation. Codex is the only native plan. It
does not know current client state and therefore marks `Changed` true; inspect
and compare that state before running its commands. These artifacts configure tool availability. They do not
prove that an agent used those tools or that model dispatch and caps are enforced.
