# Configurable backend connections

`praetorctl clients connect` projects one private deployment profile into the
existing shared MCP registry and Responses provider configuration. Client,
container, bot and private GitOps callers can select their own profile and output
directory. No deployment host, token value or model placement is built into it.

```json
{
  "version": 1,
  "gateway": {
    "bridge_command": "/opt/agent/gateway-mcp-bridge",
    "token_file": "/private/agent-token",
    "endpoints": [
      {"name": "shared-tools", "url": "https://gateway.example/mcp/"},
      {"name": "knowledge", "url": "https://gateway.example/knowledge/mcp"}
    ]
  },
  "memory": {
    "project_root": "/work/example",
    "bank_id": "private::project::example"
  }
}
```

The bridge must already exist and accept `--endpoint URL --token-file PATH`.
Token contents are not read by the generator or placed in argv. The registry
contains references only. Optional `gateway.local_servers` uses the existing
explicit stdio `name`, `command`, `args` contract. All projected server names must
be unique. Optional `provider` uses the existing
[repair provider fields](dogfood-repair-execution.md), including the exact helper
digest and logical model selector; `provider.json` is that subsection, not a
complete repair execution configuration. The generator does not execute helpers,
dispatch models, reserve quotas or grant tool trust.

```bash
praetorctl clients connect --profile /private/connections.json --out /private/generated
praetorctl clients prepare --registry /private/generated/registry.json \
  --client codex --out /private/codex-plan
praetorctl clients apply --registry /private/generated/registry.json \
  --client gemini --target /private/gemini/settings.json --out /private/gemini-backup
```

Use [client bootstrap](client-bootstrap.md) for each adapter's configuration and
native workflow. The workflow differs by client: some merge a reviewed file,
some export a profile for the caller, and Codex/AGY expose native commands.
Select the exact client profile destination; preparation does not infer global
versus repository scope. Existing unrelated settings and access rules are retained.
Conflicting server definitions fail before publication. The gateway exposes its
runtime tool inventory; the generated registry does not enumerate or freeze tools.

`clients bind-memory` adds only the selected root's `mapPathToBank` entry in an
existing Hindsight coding-agent JSON configuration:

```bash
praetorctl clients bind-memory --profile /private/connections.json \
  --target /private/hindsight/coding-agent.json --out /private/memory-backup
```

Publication retains the exact original and candidate in a new private directory,
compares the expected snapshot before replacement and reads the result back.
Existing exact mappings to another bank are conflicts. Other mappings, defaults,
credentials and unknown fields survive. A repeated identical mapping preserves
the input bytes. Restart/reconnect existing MCP processes to read the new binding.

Hindsight owns Git/worktree resolution. Verify the effective bank with fresh
`hindsight_diagnose` calls from both main and linked worktrees. A more specific
worktree mapping or `resolveWorktrees: false` can override the intended shared
bank; inserting a main-root mapping does not establish runtime equality. This
command neither ingests data nor repairs model serving.

Profiles are strict JSON objects bounded to 1 MiB and 32 nesting levels, with
1–32 total servers, literal absolute executable/token paths and 4096-byte values.
URLs require canonical HTTPS DNS/IP authorities and clean literal paths, without
credentials, query strings, fragments or escapes. MCP paths may end in one slash;
provider base URLs must omit it before the transport appends `/responses`.
An HTTP-only in-cluster endpoint requires a separately designed transport policy;
this command does not silently downgrade HTTPS. Bank IDs are 1–256 bytes.

Store profiles, backups and connection evidence in ignored private storage.
After preparation, check native registration, fresh MCP initialization, tool
discovery and a harmless real read separately. A transport failure remains an
unavailable connection; successful configuration is not proof of a working model,
shared quota enforcement or IDE lifecycle interception.
