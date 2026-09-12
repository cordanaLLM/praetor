# Shared client configuration

`praetorctl clients` projects an explicit server registry into client settings.
The same registry can be supplied from a workstation directory, container mount,
plugin host or private GitOps checkout. No account, endpoint, organization or home
directory is assumed by the registry or its adapters.

This implements configuration preparation and explicit file application. It does
not yet implement a shared runtime tool pool, global policy resolution, automatic
trust, dispatch budgets or an independently updating data service. Follow the
[management data design](../research/compact-management-data.md) for those tracked
requirements.

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
are in the [adapter reference](../../internal/clientsetup/README.md).

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

Configuration is only the first acceptance stage. Verify native discovery,
connection, user trust and a named tool call in each client. A configured server
does not establish that every tool is authorized, that all schemas are loaded
lazily or that a running client has reloaded its settings.
