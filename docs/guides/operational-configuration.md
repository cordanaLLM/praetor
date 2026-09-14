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
| `deploy/arc/` | Actions Runner Controller scale sets for the operator's cluster |
| `deploy/k8s/` | GitOps application and kustomization targeting the operator's cluster |

`deploy/helm/` stays in the engine: the chart packages the product itself, not an operator's cluster.

## Supplying it

Place the files at the paths above in your checkout, or have the operational fork carry them. They are listed
in `.gitignore`, so a local copy cannot be committed to the public engine by accident.

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
        - "macos-14"
      ephemeral: true
```

Fleet defaults merge first, then the organisation file for the organisation being resolved.

## Related decisions

- The boundary and its class list: decision Q-031 in the private questions ledger.
- The reverse-dogfooding topology that names the engine and the operational fork: ADR-0003.
- The runner matrix tiers this configuration feeds: ADR-0006.
