# Effective audit policy

CLI `praetorctl audit` and MCP `standards_audit` resolve complexity from the same
`internal/config` implementation. The existing HISS scanner consumes the resolved
`max_func_loc`. Both surfaces report the same effective digest for the same input
snapshots and show the sources that impose the limit.

The first migration covers audit function length. Cyclomatic complexity, cognitive
complexity and statement limits are resolved and retained in the typed policy;
this change does not inject them into separate linters or the LSP. Branch and
supply-chain checks retain their existing defaults plus repository overrides.
Routing, budgets, credentials and deployment activation are separate consumers.

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

An explicitly selected external file must contain a root `complexity` mapping:

```yaml
complexity:
  max_func_loc: 50
```

An empty mapping is valid and contributes no limit. Other existing sections may
coexist, but this resolver does not activate them. A misspelled root key or an
unsupported-only document fails instead of pretending a policy was applied.

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
