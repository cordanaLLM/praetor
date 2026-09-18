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

This implements configuration preparation and explicit file application. It does
not yet implement a shared runtime tool pool, global policy resolution, automatic
trust, dispatch budgets or an independently updating data service. Follow the
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

Codex and AGY require their native command workflow and cannot use `clients
apply`. Their prepared plans contain argument arrays for inspection and native
configuration. Cline and Kilo require an explicitly selected profile destination;
the command does not guess a client profile path.

These are adapter-specific workflows, not a universal lifecycle integration.
Configuration is only the first acceptance stage. Verify native discovery,
connection, user trust and a named tool call in each client. A configured server
does not establish that every tool is authorized, that all schemas are loaded
lazily or that a running client has reloaded its settings.
