# Shared client configuration

`praetorctl clients` projects an explicit server registry into client settings.

The [IDE-driven agent setup goal](../plans/ide-agent-setup.md) extends this shared
service to complete wrapper installation, native activation and ongoing
enforcement checks for every selected coding agent. The current setup entry
point covers MCP configuration; those later stages remain explicit work.

Use `praetorctl clients capabilities` or the read-only MCP tool
`standards_client_capabilities` for the current versioned adapter inventory.
It reports projection mode, documented destination and lifecycle definition
coverage. `runtime_verified: false` and `activation: unverified` mean that the
inventory has not inspected or activated any native client session.

For gateway endpoints, credential-file references, logical provider selectors and
repository memory binding, start with [connection profiles](client-connections.md).
The same registry can be supplied from a workstation directory, container mount,
plugin host or private GitOps checkout. No account, endpoint, organization or home
directory is assumed by the registry or its adapters.

This implements configuration preparation, explicit file application, and an
operator-selected AGY permission projection. It does not yet implement a shared
runtime tool pool, automatic trust, dispatch budgets or an independently updating data service. Follow the
[management data design](../research/compact-management-data.md) for those tracked
requirements.

## Configuration roots per operating system

One helper in `internal/clientsetup` resolves every client location that differs
between operating systems. It is pure: the command layer hands it the operating
system, the home directory, `APPDATA`, `LOCALAPPDATA` and `XDG_CONFIG_HOME`, and the
helper reads neither the process environment nor the filesystem. The Linux, macOS
and Windows tables are therefore tested on every CI leg, whatever the host.

| Location | Linux | macOS | Windows |
| :-- | :-- | :-- | :-- |
| Antigravity global customization root | `$HOME/.gemini/config` | same | `%USERPROFILE%\.gemini\config` |
| Antigravity workspace root | `<repo>/.agents` | same | same |
| Antigravity CLI settings | `$HOME/.gemini/antigravity-cli/settings.json` | same | `%USERPROFILE%\.gemini\antigravity-cli\settings.json` |
| Antigravity IDE user settings | `<config>/Antigravity/User/settings.json` | `$HOME/Library/Application Support/Antigravity/User/settings.json` | `%APPDATA%\Antigravity\User\settings.json` |
| Claude desktop configuration | `<config>/Claude/claude_desktop_config.json` | `$HOME/Library/Application Support/Claude/claude_desktop_config.json` | `%APPDATA%\Claude\claude_desktop_config.json` |
| PowerShell console history | does not apply | does not apply | `%APPDATA%\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt` |
| Default binary directory | `$HOME/.local/bin` | `$HOME/.local/bin` | `%LOCALAPPDATA%\Programs\praetor` |

`<config>` is `$XDG_CONFIG_HOME`, or `$HOME/.config` when that variable is unset or
relative; the XDG base directory specification declares a relative value invalid. An
unset `APPDATA` or `LOCALAPPDATA` derives from the home directory.

The global root resolves in this order: an explicit override, then
`ANTIGRAVITY_CONFIG_DIR`, then `GEMINI_CONFIG_DIR` plus `/config`, then the default.
An override or variable that is relative, contains `.` or `..` segments, or names a
missing directory is an error, never a fallback. Both variables are unverified:
neither name occurs in the `agy` 1.2.5 binary. A root chosen through them is marked
unverified and may be relied on only after a native readback agrees.
`praetorctl harvest` does not consult them.

Antigravity keeps a brain directory per product under `$HOME/.gemini`:
`antigravity/brain`, `antigravity-ide/brain` and `antigravity-cli/brain`.
`harvest bundle` and `harvest memory` used to read one directory each, and not the
same one. Both now read every directory that exists, in that order, and report when
more than one exists. When two directories hold the same conversation, the first
wins and the bundle report records the skip. `harvest memory --brain=<dir>` still
reads exactly that directory.

`harvest bundle --home=<dir>` describes a foreign home, so the `APPDATA`,
`LOCALAPPDATA` and `XDG_CONFIG_HOME` of the running process are not applied to it.

## Prepare a configuration

Create a private registry with version 1 and literal executable arguments:

```json
{
  "version": 1,
  "servers": [
    {
      "name": "shared-tools",
      "command": "/absolute/path/to/your-mcp-bridge",
      "args": ["--profile", "workstation"]
    }
  ]
}
```

Use the actual installed bridge command and its supported arguments. Credential
loading remains the bridge's responsibility; registry entries do not carry
environment values or perform shell expansion. Keep private registry files and
generated settings out of public commits.

```bash
praetorctl clients prepare --registry /private/registry.json --client gemini \
  --existing /private/client/settings.json --out /private/new-review
```

The parent of `--out` must already exist and the output directory must be new.
Omit `--existing` when preparing a new configuration. `plan.json` records source
and registry hashes; the separate artifact contains the candidate settings.
Existing unrelated settings and matching servers' access options are preserved.
A same-name server with a different executable, arguments or transport is an
explicit conflict. Strict JSON and YAML are supported; JSONC, ambiguous keys and
YAML aliases are rejected.

Supported adapter identifiers are `codex`, `claude`, `gemini`, `agy`,
`opencode-v1`, `continue`, `cline` and `kilo`. Exact schemas and source versions
are in the [adapter reference](https://github.com/CordanaLLM/praetor/blob/main/internal/clientsetup/README.md).

## Apply to an explicit destination

```bash
praetorctl clients apply --registry /private/registry.json --client gemini \
  --target /private/client/settings.json --out /private/new-backup
```

The plan, backup and replacement share one observed snapshot. Existing bytes are
retained as `config.before` before any configuration replacement. Files, backup
directory entries and the backup's parent directory are synced first. The writer
rejects symlinks, overlapping target/backup paths and changes to the observed
target; it reads back the published bytes before reporting success. Existing
restrictive file permissions are retained. An error after publication does not
prove the target stayed unchanged: inspect the target and retained artifacts
before retrying. Cooperating writers serialize, but external editors can still
race the final comparison and rename.

Codex requires its native command workflow and cannot use `clients apply`. Its
prepared plan contains argument arrays for inspection and native configuration.
Cline and Kilo require an explicitly selected profile destination;
the command does not guess a client profile path.

### Antigravity (`agy`)

Antigravity reads one strict-JSON `mcp_config.json` with an `mcpServers` map. The
`agy` adapter merges into it like any other JSON adapter: existing servers, their
`env` blocks and unknown keys are kept, and the replaced file is retained as
`config.before`. Name the destination explicitly:

```bash
# Host-wide configuration (default root; Windows: %USERPROFILE%\.gemini\config)
praetorctl clients apply --registry /private/registry.json --client agy \
  --target "$HOME/.gemini/config/mcp_config.json" --out /private/agy-backup

# One workspace
praetorctl clients apply --registry /private/registry.json --client agy \
  --target .agents/mcp_config.json --out /private/agy-workspace-backup
```

An existing entry of the same name that is remote (`serverUrl`, `url`, `httpUrl`)
or that names another executable or other arguments is a conflict, and nothing is
written. The workspace file holds absolute host paths, so it is written per host
and never committed: `/.agents/mcp_config.json` is ignored here, and `praetorctl
adopt` adds the same rule to an adopting repository. On Windows the registry
still demands an absolute literal executable, so an `npx` server is declared as
`C:\Windows\System32\cmd.exe` with the arguments `/c`, `npx` and the package.
After applying, check discovery with `agy mcp list`; the merge itself proves
nothing about the running client.

### Govern AGY headless permissions

Headless AGY cannot prompt for approval. Its scoped grants live under
`permissions.allow` in `$HOME/.gemini/antigravity-cli/settings.json`, separately
from MCP server discovery. Declare only the tools a review needs in a fleet or
workstation operator document:

```yaml
clients:
  selected:
    agy:
      permissions:
        manage: true
        allow:
          - read_file(/absolute/path/to/repository)
          - mcp(hindsight/hindsight_list_knowledge_pages)
          - mcp(hindsight/hindsight_search_knowledge_pages)
          - mcp(hindsight/hindsight_read_knowledge_page)
          - command(git status)
          - command(git diff)
          - command(git log)
          - command(git show)
          - command(git rev-parse)
          - command(rg)
```

Replace the example repository path with the exact checkout to inspect. The MCP
names must match the server and tool names registered on that host. This
profile deliberately omits file-write grants and mutating Git commands. Do not
assume that the active workspace is implicitly readable in headless mode: AGY
1.2.7 returned `denied_actions: [{action: read_file}]` for a native file read
inside the active workspace when no matching `read_file(...)` grant was loaded.
Declare every read scope that the headless task needs. An omitted write grant
likewise leaves a native write unable to prompt and therefore denied.

Review the exact destination and append-only delta, then apply and verify it:

```bash
praetorctl clients permissions plan --client agy \
  --fleet-config /configuration/fleet.yaml --out /private/agy-permission-plan

praetorctl clients permissions apply --client agy \
  --fleet-config /configuration/fleet.yaml --out /private/agy-permission-backup

praetorctl clients permissions verify --client agy \
  --fleet-config /configuration/fleet.yaml
```

The default destination is the per-OS AGY CLI settings path in the table above.
`--home` resolves that path under an explicit home; `--target` selects one exact
file. Fleet and workstation settings follow the flag, environment, install-manifest,
then built-in selection order documented in [effective policy](effective-policy.md#which-documents-the-hook-client-and-workstation-commands-use).

`plan.json` contains the target, source hash, before/after counts and exact grants
to append. Candidate settings remain a separate private artifact because unrelated
keys may contain private data. Apply preserves unknown settings and existing
allow/ask/deny rules, retains the exact previous bytes, rejects symlinks and
concurrent changes, replaces atomically, then reads back the result. The target
and `--out` directory must be disjoint: equality or containment in either direction
whether spelled directly or through a symlinked ancestor fails before artifact
writes. An absent target with an empty declared grant list
writes metadata only and omits the candidate settings artifact. With
`permissions.manage: false`, every action reports `unmanaged` without reading or
creating the target or artifact directory.

AGY evaluates conflicts as Deny > Ask > Allow. Plan, apply and verify reject a
declared allow grant intersecting an existing deny/ask rule by exact or
action-wide match, literal command prefix, recursive file scope, URL
domain/subdomain (an existing rule is compared by host even when it is spelled
with a scheme, userinfo, port or path), or MCP server wildcard without removing that operator-owned
rule. A deny `read_file` scope also conflicts with an intersecting `write_file`
allow. The adapter does not interpret regex equivalence: a same-action regex on
either side fails closed with a `cannot prove non-overlap` diagnostic. Native
workspace-relative and absolute file rules also fail closed when compared with
each other because their intersection depends on the runtime workspace root. Native
permission readback remains required for AGY's runtime scopes and any future
matcher syntax.

`verify` exits successfully only when every declared grant is present and no
supported higher-precedence conflict is detected. Its report
sets `configuration_verified: true` but always keeps `runtime_verified: false`:
settings readback does not prove that a native AGY session exercised a tool. Run a
native `agy --print=/permissions --output-format=json` readback to inspect the
merged project, shared and global permission scopes that AGY actually loaded, then
run a
bounded `agy --print --mode plan --sandbox` review without
`--dangerously-skip-permissions` to establish that separate runtime evidence.
For a read-only review, launch AGY from a disposable workspace outside the
repository under review and grant that repository only with `read_file(...)`.
Prove both directions: an explicitly granted read completes, while a write to a
fresh, explicitly named path with no matching grant is reported in
`denied_actions` and leaves no file behind. Keep that negative target outside the
reviewed repository so an unexpected success cannot alter source. `--mode plan`
controls agent behavior rather than authorization, and `--sandbox` confines
terminal commands rather than the native file-writing tool; neither flag is a
substitute for the explicit least-privilege grants above.

These are adapter-specific workflows, not a universal lifecycle integration.
Configuration is only the first acceptance stage. Verify native discovery,
connection, user trust and a named tool call in each client. A configured server
does not establish that every tool is authorized, that all schemas are loaded
lazily or that a running client has reloaded its settings.
