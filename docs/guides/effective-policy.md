# Effective audit policy

CLI `praetorctl audit`, MCP `standards_audit` and retained public adoption loops
resolve complexity from the same `internal/config` implementation. The existing
HISS scanner consumes the resolved `max_func_loc`. CLI/MCP audits report the same
effective digest for the same input snapshots and show the sources that impose
the limit. Public-loop reports retain the planned policy and each verification's
policy digest, applied limit and baseline evidence.

The first migration covers audit function length. Cyclomatic complexity, cognitive
complexity and statement limits are resolved and retained in the typed policy;
this change does not inject them into separate linters or the LSP. Branch and
supply-chain checks retain their existing defaults plus repository overrides.
Routing, budgets, credentials and deployment activation are separate consumers.

Public adoption loops resolve their policy during the dry run, then independently
scan the original source under that policy before applying changes. Applied
policy must match the plan. Verification compares the saved baseline's entries
with the independent original scan and evaluates current violations against that
anchor. Unchanged legacy debt is retained; new violations and violations in
touched files fail. Old public-loop reports produced with a fixed scanner limit
do not establish verification under a stricter policy; rerun those cases.
Public loops currently select repository and pinned-catalog constraints;
external fleet/organization/deployment/workstation paths are audit arguments,
not public-loop options.

## Sources and strictness

Resolution uses these sources in a fixed order:

1. Built-in defaults.
2. The repository's `.standards.lock` and its pinned, materialized profiles/facets.
3. Explicit fleet, organization, deployment and workstation files, in that order.
4. Repository `overrides.complexity` from `.standards.yaml`.
5. The audit compatibility constraint: `max_func_loc: 60`.

Every complexity constraint can only tighten an earlier limit. A later limit of
`90` cannot override an existing `50`. The compatibility constraint preserves the
previous scanner ceiling; it can be tightened further. Omitted fields do not
contribute. Explicit zero, negative, null, noninteger and unknown complexity fields
are errors.

### What `plan` reports

`praetorctl plan` resolves this same policy, with the audit compatibility constraint applied, so a
dry run previews what the audit will enforce. It previously reported the built-in defaults with only
the repository's own overrides applied, which meant it never read the pinned profiles: one
repository's archetype declared `max_func_loc: 75`, `plan` printed `100` and `audit` enforced `60`.

Resolving the policy needs a lockfile. In a repository that has not been adopted there are no pinned
profiles, so defaults plus the repository's overrides is the whole policy, and `plan` says so:

```
[INFO] no .standards.lock: built-in defaults and repository overrides only
```

### Consequence of the compatibility constraint

Because strictness only tightens, `max_func_loc: 60` wins against any archetype declaring more.
Ten of the fourteen shipped archetypes declare a looser bound -- `template-seed` 100,
`app-service` 80, seven at 75, `library-client` 70 -- and every one of them is enforced at 60 while
that constraint is in place.

An archetype's declared `max_func_loc` above 60 is therefore documentation of intent rather than an
effective limit. Read the `audit` output, not the archetype file, to learn what a repository is held
to.

An explicitly selected external file must contain at least one owned root section:
`complexity`, or one of the operator sections `clients`, `hooks` and `update`
described in [Operator settings](#operator-settings-clients-hooks-and-update).

```yaml
complexity:
  max_func_loc: 50
```

An empty mapping is valid and contributes nothing. Other existing sections may
coexist, but this resolver does not activate them. A misspelled root key or an
unsupported-only document fails instead of pretending a policy was applied.

## Operator settings: clients, hooks and update

The same external documents carry the operator's settings for agent clients, the
agent-hook command policy and workstation updates. There is no second settings
file: a fleet, organization, deployment or workstation document may hold these
sections beside `complexity`, or on their own. They go through the same loader and
bounds, and into the same effective digest.

`audit` resolves and seals these sections but does not act on them. Host commands
select the fleet and workstation documents through `config.SelectOperatorSettings`
and load the same schema through `config.LoadOperatorSettings`. `praetorctl hook`
consumes `hooks`; `praetorctl clients permissions` consumes the AGY permission
selection under `clients`. Repository policy and host activation therefore share
one schema without making `audit` inspect a user's home directory.

A workstation document, the layer that holds host paths:

```yaml
clients:
  selected:
    agy:
      binary: /home/operator/.local/bin/agy
      permissions:
        manage: true
        allow: ["mcp(hindsight/hindsight_list_knowledge_pages)"]
hooks:
  command_policy:
    deny: ['\bexample-org/']
update:
  checkout: /home/operator/dev/example/praetor
```

### Keys and defaults

Keys inside `clients`, `hooks` and `update` are strict. A misspelled key, an
unknown client or a value of the wrong type fails the document and names the key.
Every string passes the literal rule shared with the MCP registry
(`internal/util/literal.go`): no control byte, `$`, backtick, `{env:` or `{file:`.

| Key | Default | Accepted values |
| :-- | :-- | :-- |
| `clients.mode` | `advisory` | `advisory` reports an ungoverned client and lets work continue; `strict` makes the client commands fail instead: a launch below `discovered`, a verify of a required client below `observed` |
| `clients.verified_max_age` | `168h` | a duration from `1h` to `2160h` |
| `clients.govern` | `present` | `present` governs every known client installed on the host; `listed` governs only `clients.selected` |
| `clients.selected.<id>` | every known client | `<id>` is one of `agy`, `claude`, `cline`, `codex`, `continue`, `gemini`, `kilo`, `opencode-v1` (`internal/clientid`) |
| `clients.selected.<id>.required` | `true` | boolean |
| `clients.selected.<id>.scopes` | `[global]` | `global` and/or `workspace`, each at most once |
| `clients.selected.<id>.plugin` | `true` | boolean |
| `clients.selected.<id>.binary`, `.config_root` | empty (look up at run time) | clean absolute path; workstation layer only |
| `clients.selected.<id>.registry`, `.connection_profile` | empty | clean path, relative to the file that sets it, or absolute |
| `clients.selected.<id>.permissions.manage` | `false` | boolean; `true` appends the listed grants to the client's own list and never removes or widens one |
| `clients.selected.<id>.permissions.allow` | `[]` | at most 32 literals of up to 512 bytes, merged across layers |
| `hooks.scope` | `governed` | `governed` guards repositories that carry `.standards.yaml`; `all` guards every workspace |
| `hooks.command_policy.deny` | `[]` | at most 64 RE2 patterns of up to 512 bytes, compiled at load |
| `hooks.python` | `[python3, python, [py, -3]]` | up to 8 candidates: a bare command, or a list of a command and up to 8 arguments; an absolute path only in the workstation layer |
| `update.channel` | `push` | `push`; `release` is reserved until release artifacts ship |
| `update.source` | `checkout` | `checkout`; `artifact` is reserved |
| `update.checkout`, `update.bin_dir` | empty | clean absolute path; workstation layer only |
| `update.remote` | `origin` | 1 to 64 letters, digits, `.`, `_` or `-`, starting with a letter or digit |
| `update.branch` | `main` | 1 to 128 of the same plus `/`, without `..` |
| `update.pin` | empty | a 40-character lowercase commit id |
| `update.interval` | `15m` | a duration from `5m` to `24h` |
| `update.require_signed` | `true` | boolean |
| `update.allowed_signers` | empty | clean path, relative to the file that sets it, or absolute |
| `update.receipt_public_key` | empty | 64 lowercase hex characters (Ed25519) |

The engine ships no deny pattern and manages no client's grant list. Both are
operator decisions: organisation names belong in `hooks.command_policy.deny`, and
an operator host opts in to grant management by setting `permissions.manage: true`
for a client in its own layer, as the example above does for `agy`. Adopters who
set neither get neither.
`config.ValidateCommandPolicyDeny` is the one check for those bounds; the hook
policy validates through it.

AGY 1.2.7 grants use exact `action(target)` rules. The managed adapter accepts
`read_file`, `write_file`, `read_url`, `execute_url`, `command` and `mcp`; an MCP
target names exact `server/tool`, `server/*`, or global `*`. File and URL rules
accept only the documented global `*`, not partial globs. The operator loader keeps the grant strings generic so
future clients can carry their own syntax, while `clients permissions` validates
the selected AGY rules before producing or changing a settings file.

An allow rule controls which matching call can proceed without approval. Do not
infer an active-workspace exception: an AGY 1.2.7 headless replay denied a native
read inside its active workspace when no matching `read_file(...)` grant was
loaded. A governed read-only review therefore starts AGY in a separate disposable
workspace and explicitly grants the reviewed repository through
`read_file(...)`. Starting AGY inside the reviewed repository does not make that
repository readable or read-only by policy.

### How layers merge

Layers apply in the order listed under [Sources and strictness](#sources-and-strictness):
fleet, organization, deployment, workstation.

- A later layer replaces a scalar. `EffectivePolicy.OperatorFields` records the
  layers that set each concrete key, for example
  `clients.selected.agy.permissions.allow`.
- Once a layer sets `clients.mode: strict`, `clients.govern: present`,
  `required: true`, `update.require_signed: true` or `hooks.scope: all`, a later
  layer cannot loosen it. The error names both layers. A built-in default is not a
  layer, so the first layer may choose either value.
- `hooks.command_policy.deny` and `permissions.allow` append without duplicates.
  The bound applies to the merged list, so 40 fleet patterns plus 25 new
  workstation patterns fail.
- Two layers giving one client different non-empty `registry` values fail.
- Client binaries, config roots, the fork checkout, `bin_dir` and absolute
  interpreter paths are host data. Any layer other than `workstation` that sets
  one fails.

A relative `registry`, `connection_profile` or `allowed_signers` value keeps its
spelling in the digest, so moving the documents does not change it.
`EffectivePolicy.ResolveOperatorPath` resolves it against the directory of the
file that set it.

A policy whose layers carry no operator section keeps the digest it had before
these sections existed. Retained plans and repair anchors therefore still verify.

### Which documents the hook, client and workstation commands use

`audit` and `gate` take the explicit flags above and read nothing else. For the
hook, client and workstation commands, `config.SelectOperatorSettings` in
`internal/config/install_manifest.go` resolves each document in this order:

1. The command's `--fleet-config` or `--workstation-config` flag.
2. `PRAETOR_FLEET_CONFIG` or `PRAETOR_WORKSTATION_CONFIG`.
3. The path the install manifest recorded. The manifest is
   `praetor/install.json` under the per-user configuration directory; the
   `workstation install` and `workstation update` commands write it when they
   ship. The recorded path is used only
   while the file's bytes still match the digest the manifest recorded. A changed
   or missing file is an error that says to rerun `workstation update` or pass the
   path explicitly.
4. Not configured: the built-in defaults.

There is no separate pointer file. The manifest reader rejects unknown and
duplicate members and checks every field.

Tests: `internal/config/operator_sections_test.go` (four-layer merge with
contributors, loosening, host data, bounds, digest) and
`internal/config/install_manifest_test.go` (manifest and selection order), with
fixtures under `internal/config/testdata/operator/`.

## Selecting a shared or private configuration

```sh
praetorctl audit --config /workspace/project/.standards.yaml \
  --catalog-root /opt/praetor-catalog \
  --fleet-config /configuration/fleet.yaml \
  --organization-config /configuration/organization.yaml \
  --deployment-config /configuration/container.yaml \
  --workstation-config /configuration/workstation.yaml
```

Each path is optional. Omission means no external layer: files in a home directory
or repository are not automatically discovered. An explicitly selected missing
file fails. The catalog root contains `.config/archetypes` and its `facets`
subdirectory; every declared profile/facet must match the repository lock digest.
The default catalog root is the audited repository.

The same explicit configuration can be mounted into a container, bot, plugin or
workstation. A private GitOps fork can own these files while consuming the public
application. These flags configure one audit invocation; they do not install a
global service or activate policy in other tools.

The corresponding optional MCP arguments are `catalog_root`, `fleet_config_path`,
`organization_config_path`, `deployment_config_path` and `workstation_config_path`.
They retain the server's existing path-confinement policy. Supplying a path does
not grant permission to read outside the server root. Operator-global server
configuration remains a separate integration step.

## Provenance and migration

Resolution reads bounded regular-file snapshots, rejects symlinks, and hashes the
exact bytes decoded. Unsupported YAML aliases, duplicate keys and multiple
documents fail. Pinned sources must exist locally in the selected catalog;
lock metadata alone cannot establish the policy that was applied.

The effective digest includes ordered source identities and hashes, effective
policy values and field contributors. Diagnostic filesystem paths are excluded,
so relocating an identical catalog or mounting the same configuration elsewhere
does not change its identity. CLI/MCP text includes at most 16 source hashes and
an explicit omitted count. The typed result retains every source and contributor.

Existing repositories with lock pins but no local archetypes must either select a
matching source bundle with `--catalog-root` or materialize the pinned catalog
during adoption. The resolver supplies exact validated `CatalogArtifacts` for
bootstrap; these contain only selected profiles/facets, never external fleet,
deployment or workstation configuration. Files with changed digests are rejected.

Adoption copies those exact pinned profile/facet bytes into the target catalog,
then verifies the target with the default local resolver. Subsequent adoption
can run without the original source bundle. It checks the complete prospective
catalog before writing: duplicate IDs, invalid entries, directory bounds and
symlinks fail. Existing differing files require explicit `--force`; matching
files remain unchanged. Baseline recording uses the same resolved audit limit.

Adoption keeps baseline recording enabled by default, including its dry-run debt
estimate. Both modes now require verified local pins and their catalog, or an
explicit `--lock-source-root` for missing inputs. A dry run without these sources
fails instead of estimating debt with an unrelated default limit. Planning with
`--record-baseline=false` can explicitly omit that estimate; its report identifies
any unavailable policy verification.

`LoadEffectivePolicyContext` and `ResolvePolicy` are additive APIs. Existing
manifest parsing and runner hierarchy APIs remain available. The audit's new
requirement for materialized pinned sources is a default-behavior change and must
ship with a breaking commit marker and a `Migration:` footer.
