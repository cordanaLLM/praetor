# Operational configuration

Praetor separates the **engine** from the **operational configuration** it acts on. This guide states which
side owns what, and how an operator supplies the operational half.

## Why the split exists

The engine is public. Organisation names, repository names, runner routing and cluster manifests are operator
data: publishing them discloses private repositories and pins the engine to one operator's infrastructure.
Before this split the engine shipped a fleet map naming private repositories, and it drifted from reality the
moment the fleet changed, because nothing could update it.

The rule that keeps the two apart: **the engine reads operational configuration, it never embeds it.** When a
capability has no configuration, it reports that state — `not configured` — instead of falling back to a
built-in default that looks like fact.

## What the engine owns

Everything needed to govern a repository without knowing whose repository it is:

- source code and its tests;
- archetypes and facets (`.config/archetypes/`), linter and Semgrep policy, supply-chain policy;
- the default label taxonomy, rulesets and GitHub App manifest;
- schemas, templates and anonymised fixtures.

## What the operator owns

Supplied locally or from the operational fork, and ignored by this repository:

| Path | Holds |
| :-- | :-- |
| `.config/fleet-topology.yaml` | organisations governed and archetype membership, reported by `harvest fleet` |
| `.config/fleet.yaml` | fleet-wide runner defaults (tier 1 of the runner matrix) |
| `.config/orgs/<org>.yaml` | organisation runner overrides (tier 2) |
| `.config/operator/` | operator settings the fork carries, such as fleet-wide and per-workstation settings files; explicitly selected by hook, client and workstation commands |
| `deploy/arc/` | Actions Runner Controller scale sets for the operator's cluster |
| `deploy/k8s/` | GitOps application and kustomization targeting the operator's cluster |

`deploy/helm/` stays in the engine: the chart packages the product itself, not an operator's cluster.

## Supplying it

Place the files at the paths above in your checkout, or have the operational fork carry them. They are listed
in `.gitignore`, so a local copy cannot be committed to the public engine by accident.

### Carrying it in the operational fork

Because the engine ignores these paths, the fork tracks them with `git add -f`. Operational sync accepts
operator files only under the paths in the table above, only as regular non-executable files of at most
1 MiB, at most 256 of them, and only when the public source has no file at the same path; anything else
outside the four identity overlay files still stops `plan` and `prepare`. The accepted files are reported as
`owner_only_paths`. The path list is engine schema, and an engine test proves that `.gitignore` ignores every
entry, so a prefix cannot be added that the public source could also track. See [owner-only operator paths](operational-sync.md#owner-only-operator-paths).

A new fork gets its identity overlay from `praetorctl operational sync init`, not from a hand edit; see
[apply the first overlay](operational-sync.md#apply-the-first-overlay). Review every operator file for
secrets before committing it: the fork is private, but credentials belong in a secret store, not in Git.

### Fleet topology example

```yaml
orgs:
  - exampleOrg
  - partnerOrg
archetypes:
  framework:
    - example-framework
  library-client:
    - example-client
    - second-client
```

`harvest fleet` prints the configured topology. Without the file it reports `not configured` and names the
path it expected, rather than printing a fleet of its own.

### Runner routing example

```yaml
runners:
  default: "example-runner-set-linux-amd64"
  routing:
    darwin/arm64:
      type: "github-hosted"
      runs_on:
        - "macos-26"
      ephemeral: true
```

Fleet defaults merge first, then the organisation file for the organisation being resolved.

## Related decisions

- The boundary and its class list: decision Q-031 in the private questions ledger.
- The reverse-dogfooding topology that names the engine and the operational fork: ADR-0003.
- The runner matrix tiers this configuration feeds: ADR-0006. Its Tier 4 text still names
  `macos-14` and `macos-13` as the Darwin defaults. Both are stale: `macos-14` carries a
  deprecated badge in actions/runner-images and `macos-13` is no longer published, so the
  defaults moved to `macos-26` and `macos-26-intel` (`internal/config/hierarchy.go`, which
  cites the evidence). ADR-0006 is Accepted and therefore frozen; restating the tier needs
  a superseding ADR, not an edit. The tiering decision itself is unaffected.
