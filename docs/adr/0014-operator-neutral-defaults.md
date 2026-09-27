# ADR-0014: Operator-Neutral Defaults — Deployment Data Becomes Operator Configuration

## Status

Proposed — 2026-09-26. On acceptance this record supersedes ADR-0007 (the designation of one
framework organisation as the fleet-wide framework hub) and amends rule 4 of
`docs/adr/README.md` (see Decision §7). It implements stage 1 ("Shared package policy") of
`docs/plans/package-development-pipeline.md`.

## Context

Praetor is a public governance engine. Its CLI help, MCP tool descriptions, defaults, emitted
templates, generated artifacts and documentation are read by adopters, who run it against their
own repositories or their own fork. The maintainer directed that every one of these surfaces
address the adopter and the adopter's repositories, and that no surface present the maintainer's
personal repositories, organisations, endpoints or accounts as defaults or examples.

Today several surfaces carry the maintainer's deployment instead (all at `origin/main`
`ac3d27c2`):

| Surface | Where | What an adopter gets |
| :--- | :--- | :--- |
| Default forge owner | `cmd/standardsctl/issue.go:57`, `milestone.go:91`, `project.go:50,81,131`, `init.go:85`, `internal/adopt/adopt.go:40`, `internal/forge/wiki.go:20`, `internal/paperclip/harness.go:90` | operations aimed at the maintainer's organisation when no owner resolves |
| Default reconcile set | `cmd/standardsctl/issue.go:58` | `issue reconcile` walks the maintainer's repositories |
| Target framework | `internal/needs/framework.go:16-18`, `analyzer_{go,node,python,rust,native}.go`, `requests.go:66,78-90`, `harvester.go:186-212`, `migrate.go:21`, `catalog.go` | needs reports, migrations and demand requests routed to the maintainer's framework kits |
| Report and help text | `cmd/standardsctl/main.go:168`, `needs.go:55,61,124`, `cmd/standards-mcp/server.go:677,703,711`, `internal/needs/aggregate.go:440,547` | the maintainer's framework named in headers and tool descriptions |
| Serialized key | `internal/needs/types.go:58` (a replacement key named after the maintainer's framework) | a maintainer-specific key written into every adopter's `.needs.yaml` |
| Review bot and labels | `internal/forge/pr.go:23`, `internal/adopt/ruleset.go:95`, `cmd/standardsctl/sync.go:346`, `internal/adopt/harness.go:147` | an unprovisioned private GitHub App requested as reviewer and credited in emitted labels and AGENTS.md (`.config/github-app/permissions.md:9-12` states nothing provisions it) |
| Organisation folders | `internal/topology/topology.go:33-43`, `internal/adopt/validate.go:27-37`, `.config/agent/hooks/block_evasion.py:23` | the maintainer's organisation names hard-coded as the only recognised containers, in three copies |
| Endpoints, paths, identities | `tribunus/internal/sources/litellmgateway/source.go:4-5`, `internal/harvester/bundle.go:783`, `internal/devsync/devsync.go:27`, `internal/supplychain/slsa.go:134`, `.github/workflows/adopt.yml:79-80`, `scripts/sync_github_wiki.sh:118-119`, `.github/workflows/release-binaries.yml:66-89` | a private gateway, a personal script name, a personal Drive base, a private build-type domain, a private committer identity and legacy asset names |

The engine already has the machinery to carry operator data. Operator settings are layered
(fleet, organisation, deployment, workstation) and merged by `internal/config/operator_merge.go`;
documents are selected by flag, environment and install manifest through
`config.SelectOperatorSettings` (`internal/config/install_manifest.go:271`); and one precedent
already removes an organisation list from code: `builtinDevRoot` in
`internal/agenthook/policy.go:47` names no organisation and takes organisation names from
`hooks.command_policy.deny`.

A census produced for this record over-counted by treating project identity and the generic
LiteLLM product as private, and proposed an `operator.defaults.<name>` namespace. That namespace would publish the very names being removed
and add a second configuration system beside the operator settings, so it is not used here.

## Decision

### 1. Layering rule

| Class | Where it lives | Examples |
| :--- | :--- | :--- |
| Project identity | code, unchanged | `github.com/cordanaLLM/praetor`, the praetor name, SPDX headers, `https://cordanallm.github.io/praetor/`, funding links (#222) |
| Maintainer's configuration of this repository | this repository's own files | `.standards.yaml`, `mkdocs.yml` `site_author` |
| Operator data (a host's or fleet's choices) | operator settings layers | target framework, default owner, reconcile set, review bot, organisation folders, framework checkout |
| Text in committed adopter artifacts or gate verdicts | no configuration value; the text itself becomes neutral | emitted AGENTS.md harness, label taxonomy, SBOM names, SLSA build type |

The last row has no configuration value because CI runs without the workstation layer: an
artifact rendered from an operator value would drift between a local render and a CI re-render
(HISS-21).

### 2. Schema additions

`operatorSectionNames` (`internal/config/operator_decode.go:24`) gains `framework`, `forge` and
`topology`. The wildcard lookup in `internal/config/operator_schema.go` (`genericSettingPath`,
`settingClient`) becomes a table of wildcard prefixes so `framework.targets.<language>` is
validated like `clients.selected.<client>`. `FrameworkLanguages = go, typescript, python, rust,
native` is defined once in `internal/config` and referenced by the needs analyzers and the
harvester (HISS-19).

| Key | Kind | Merge | Default | Validation | Layer | Read by |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| `framework.targets.<lang>` | entry | – | absent | `<lang>` in `FrameworkLanguages` | any | needs |
| `framework.targets.<lang>.module` | text | replace | `""` | module-path shaped (first element contains a dot), at most 256 bytes; the check moves from `needs.isModulePathShaped` into `internal/config` | any | analyzers, report, epic, migrate |
| `framework.targets.<lang>.builder_kits` | list | replace | `[]` | at most 8, each `owner/repo`; entry 0 is the demand-request routing target | any | `needs requests`, harvester |
| `framework.targets.<lang>.contract` | document path | replace | `""` | clean path, resolved against the declaring layer's file (`ResolveOperatorPath`); parsed as capability contract v1 when used | any | declared framework index |
| `framework.targets.<lang>.checkout` | host path | replace | `""` | clean absolute path; accepted for `go` only | workstation | framework observation |
| `framework.migration_branch` | text | replace | `""` (built-in `refactor/framework-adoption`) | branch grammar | any | `needs migrate` |
| `forge.default_owner` | text | replace | `""` | GitHub owner grammar | any | owner resolution, last step |
| `forge.reconcile_repos` | list | replace | `[]` | at most 256, each `owner/repo` | any | `issue reconcile` default |
| `forge.review_bot` | text | replace | `""` | owner grammar with optional `[bot]` suffix | any | `forge.AssignReviewers` |
| `topology.org_containers` | list | append | `[]` | at most 64, `^[a-z0-9][a-z0-9._-]{0,63}$`, compared lowercased | any | topology audit, cleanup guard |

The new `OperatorSettings` fields are tagged `json:",omitzero"`, so the sealed digest of every
existing operator document (`EffectivePolicy.SHA256`) is byte-identical; a golden test pins this.
`DefaultOperatorSettings()` leaves every new value empty. One loader,
`cmd/standardsctl/operator_settings.go`, replaces the copies in `hook.go` and
`client_permissions.go`; every command that reads a new key accepts `--fleet-config`,
`--workstation-config` and `--manifest`, the flags `clients permissions` already uses. MCP tools
select settings from the environment and the install manifest at call time, as `hook` does.

Capability contract v1 gains optional fields; unknown fields are already ignored
(`internal/needs/framework_contract.go:33-35`), so the schema version stays 1: `ecosystem`
(`go`, `npm`, `pypi`, `cargo`, `system`; default `go`; selects the grammar `replaces` is checked
against), `adapts`, `wraps`, `tooling_for` and top-level `foundations`. A new command,
`praetorctl needs contract export --language=<lang> --out=<file>`, snapshots a resolved framework
index as a contract, which also serves offline CI.

Example workstation document (placeholders only):

```yaml
framework:
  targets:
    go: {module: example.com/acme/kit, builder_kits: [acme/kit], contract: frameworks/kit.capabilities.yaml, checkout: /home/operator/dev/acme/kit}
forge: {default_owner: acme, reconcile_repos: [acme/kit, acme/app], review_bot: "acme-review[bot]"}
topology: {org_containers: [acme, acme-labs]}
```

### 3. Resolution order

- **Owner**: `--owner` flag, then `.standards.yaml` `repository.owner`, then the origin remote
  (`util.ResolveRemoteIdentity`, which never reads the checkout path), then
  `forge.default_owner`, else the error
  `owner unknown: pass --owner, set repository.owner, or set forge.default_owner`. The repository
  name comes from `repository.name`, then the origin remote, and is never guessed from the
  directory. One function, `config.ResolveRepositoryIdentity`, replaces the three resolvers that
  exist today (`paperclip.resolvePlatform`, `adopt.resolveOwner`, `cmd/standardsctl`
  `resolveRepoCoordinates`).
- **Reconcile set**: `--repos`, then `forge.reconcile_repos`, then the current repository, then
  the existing refusal.
- **Framework checkout**: `--framework`, then `$PRAETOR_FRAMEWORK_DIR`, then
  `framework.targets.go.checkout`, then the declared contract; the `<dev root>/<org>/<repo>`
  fallback in `cmd/standardsctl/devroot.go` is removed.

### 4. Behaviour when a value is unset

| Feature | Unset behaviour |
| :--- | :--- |
| `needs scan` | classifies dependencies into capabilities; `framework` is omitted; readiness basis `not-configured`; the CLI prints `Mapping availability: n/a (no target framework configured)`, never 0% |
| `needs report`, `aggregate`, MCP `standards_needs_report` | classification works; header `Framework: not configured (set framework.targets.<lang>.module and .contract, or pass --framework)`; exit 0 |
| `needs epic` | preview works with `Target framework: not configured`; `--publish` refuses with the same hint |
| `needs migrate` | dry run prints `nothing to rewrite: no target framework configured`; `--apply` refuses |
| `needs requests` | requests are synthesized; unrouted ones carry `target_builder_kit: ""` and `target_org: ""`, the spec line `Target: unrouted (framework.targets.<lang>.builder_kits not set)`, and a summary `N of M requests unrouted` |
| harvester framework scoring | row scored not-configured; builder kits empty |
| Owner-dependent commands (`issue`, `milestone`, `project`, `init`, `adopt`, `forge wiki`, paperclip harness) | resolution order of §3; no invented owner; `init` writes an empty owner and prints `repository.owner not detected; set it in .standards.yaml` |
| `issue reconcile` | §3 order, then the existing refusal |
| `forge.AssignReviewers` | no bot reviewer |
| Topology audit and cleanup guard | built-in set (`upstream`, `local`, `stacks`, `worktrees`, `scratch`) plus `topology.org_containers` plus structural detection (a non-repository directory in the dev root holding at least one child Git repository); cleanup never deletes a directory that holds a repository |
| Adoption target validation | built-in set plus structural detection (`checkForChildRepositories` already refuses containers) |
| Agent hooks | the generic dev-root rule of `builtinDevRoot`; organisation names only through `hooks.command_policy.deny` |
| `devsync init` | `--base` is required; there is no default remote folder |
| `supplychain provenance` | `--builder` defaults to the GitHub Actions workflow identity when running in Actions, and is required elsewhere |

### 5. Compatibility policy

| Old | New | Compatibility |
| :--- | :--- | :--- |
| `.needs.yaml` / JSON replacement key named after the former built-in framework (`legacyReplacementKey`, `internal/needs/demand_alias.go`) | `framework_replacement` | read as a deprecated alias when the new key is empty; both set and different is an error; each alias read adds a warning to `RepoNeeds.Deprecations`, printed by CLI and MCP; writes use the new key only; the alias is removed two minor releases after this record is accepted |
| The Go field behind that key (`DependencyDemand`, `CatalogEntry`) | `FrameworkReplacement` | none needed: `internal/` packages cannot be imported from outside the module |
| built-in framework module, builder kits, catalog replacements | operator configuration and contracts | no built-in fallback after the data unit lands; see §6 |
| `migrate` branch (former framework-specific name, `internal/needs/migrate.go:21`) | `refactor/framework-adoption` | adopters with an open legacy branch set `framework.migration_branch` to the old name, which the release note states |
| `FRAMEWORK_DEMAND.yaml` `target_org` | unchanged key | value is the owner part of the routing builder kit; empty when unrouted |
| `.needs.yaml` `framework:` written by an old scan | – | a scan record, never an input; the next `--write` replaces it |
| `forge.StandardReviewBot` constant | `AssignReviewers(paths, codeowners, bots)` | no production caller today (tests only) |
| `topology.KnownOrgContainers`, `adopt.knownOrgNames` | `topology.BuiltinOrgContainers` plus configured and structural containers | one implementation |
| Report header naming the former built-in framework (`cmd/standardsctl/needs.go:124`) | `Framework Migration Report` | text consumers change once |
| SBOM assets named after a former private project name (`<former>-{cyclonedx,spdx}.json`) | `praetor-{cyclonedx,spdx}.json` | renamed without alias; release note |
| SLSA `buildType` URI on an unprovisioned private domain | a project-owned URI under `https://cordanallm.github.io/praetor/` | verifiers pinning the old value update once; release note |
| `$PRAETOR_FRAMEWORK_DIR`, MCP tool names | unchanged | – |

Every breaking row is flagged `!` in its commit subject with a `Migration:` footer.

### 6. Operator data is moved, never deleted

A private value becomes an operator configuration value whose default is unset; an operator who
configures it keeps the feature. Before the built-in framework tables are removed, the data unit
exports them with `praetorctl needs contract export` into contract files outside the repository,
and proves that nothing is lost: every demand the built-in tables covered is still covered once the
exported contracts are configured. `needs scan` covers the same demands either way. `needs report`
can cover more: the built-in report path reconciled every language against the go framework, so
non-go demands its own language's contract maps were reported as gaps. A strict superset
therefore passes; a demand covered before and a gap after fails. The maintainer then records the exports and the former
defaults (owner, reconcile set, review bot, organisation folders, hook deny patterns, GitHub App
manifest values) in their own operator documents. For a maintainer running an operational fork,
`.config/fleet.yaml` and `.config/operator/` are owner-only paths
(`internal/operationalsync/overlay.go:23`) and are the intended home.

### 7. Identifier redaction in decision records

Rule 4 of `docs/adr/README.md` gains one exception: replacing an operator-private identifier (a
repository, organisation, host, path or account) with a neutral placeholder does not change a
decision, and is allowed in an Accepted or Superseded body. Each redaction is listed in a
`Redactions` note at the end of the record with its date. Decision content, rationale and
evidence bindings stay unchanged.

### 8. Upstream attribution

Artifacts derived from third-party projects are credited: `docs/credits.md` lists each derived
artifact with its upstream URL, licence and what changed; `REUSE.toml` carries an
`[[annotations]]` entry with the upstream copyright and licence wherever text was copied; each
derived skill records `derived_from: <url> (<licence>)` in its front matter. Provenance is taken
from the upstream repository itself, never from memory. The maintainer's own projects are not
listed as upstreams.

## Consequences

### Positive

- An adopter's first run speaks about the adopter's repositories; nothing points at the
  maintainer's organisation, framework, bot, gateway or folders.
- One owner resolver, one organisation-container list, one label taxonomy, one report header and
  one settings loader replace duplicated code (HISS-19).
- A fork's framework relationships are matched against its own module prefix; today
  `internal/needs/framework_observe.go:141` and `library_relationships.go:40,66` strip the built-in
  module prefix even when a fork is observed.

### Negative / Trade-offs

- Until an operator configures `framework.targets`, needs reports classify only; the maintainer
  loses framework coverage between the data unit landing and their configuration existing.
- Generated artifacts change once: the emitted AGENTS.md harness block, the label description,
  the `.needs.yaml` key, `FRAMEWORK_DEMAND.yaml` `target_org`, SBOM names and the SLSA build type.
  `adopt` and `sync` report drift once.
- Scripts that ran owner-dependent commands outside a checkout without `--owner` now fail loudly.
- Structural organisation detection reclassifies directories in topology audit output.
- About 245 test assertions move to `example.com/acme` fixtures through injected targets.

### Neutral

- Contract v1 stays at version 1. A praetor release older than this record reading a non-Go
  contract rejects its `replaces` entries; Go contracts are unaffected.

## Verification & Compliance

- `internal/config` replays operator documents with and without the new sections in both
  directions (HISS-20); the digest golden test proves existing documents keep their digest.
- Each consumer has a test showing that an unset value produces the behaviour in §4.
- The data unit's parity check compares reports and scans from the built-in tables and from the
  exported contracts before the tables are removed: no covered demand may become a gap.
- `git grep` for the maintainer's identifiers over code, help text, templates, generated files and
  `docs/` returns only project identity, SPDX headers and this repository's own configuration.

## Alternatives considered

- **`operator.defaults.<name>` keys (census proposal).** Rejected: the key names publish the
  identifiers being removed, and a second configuration system violates HISS-19.
- **Keep the built-in tables, active only when the configured module matches.** Rejected: the
  maintainer's data stays published in source.
- **Delete the built-in data outright.** Rejected: operator data is configured, never deleted
  (#222); the export in §6 preserves it.
- **Exclude superseded records from the documentation site instead of redacting.** Rejected: it
  hides the decision history to hide a handful of identifiers.
- **A default owner derived from the praetor module path.** Rejected: that is the project's
  identity, not the adopter's.

## Open questions

- Whether the domains named in `.standards.yaml` (`repository.homepage`) and, when this record
  was written, in `.config/github-app/manifest.json` and `docs/presets/**` are project identity
  or private infrastructure. This record treats them as unprovisioned private domains in emitted
  and generated surfaces.
- Whether this repository's own `.needs.yaml` keeps a framework target. This record rescans it
  without one, so committed output does not depend on operator configuration.
- Resolved 2026-09-27: pull request #193 first added a dependency on the maintainer's framework to
  praetor itself, a direction that conflicts with this record. The operator chose a neutral
  rewrite instead; #193 now reads `go.mod` through `internal/gomanifest` and adds no module.

## Checkable clauses

This record declares no `adr-constraint` block. A forbidden-token clause would have to list the
identifiers it forbids, which publishes them.

## References

- `docs/plans/package-development-pipeline.md` (stage 1, and its note that the generic policy
  supersedes ADR-0007).
- #222 (operator data is configuration, never deleted).
- ADR-0007 (superseded on acceptance), ADR-0011 (schedules deletion of
  `.config/agent/hooks/block_evasion.py`), ADR-0012 (owner-only paths of the operational fork).
- `internal/config/operator_decode.go`, `operator_schema.go`, `operator_sections.go`,
  `operator_merge.go`, `install_manifest.go`; `internal/agenthook/policy.go:47`.

## Redactions

- 2026-09-27: operator-private identifiers replaced with neutral descriptions, in the form
  Decision §7 prescribes; no decision content changed. Context table, serialized-key row: the literal key
  name became a description. Decision §5, first row: the literal key name became a description
  pointing at `legacyReplacementKey` in `internal/needs/demand_alias.go`, where the alias is
  still read. Decision §5, SBOM row: the former asset name prefix became `<former>`. Decision
  §5, SLSA row: the former build-type URI became a description. Open questions, first item: the
  domain names became pointers to the files that declare them.
