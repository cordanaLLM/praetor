# Composable Archetypes & Strictness Lattice

Repository configuration is modeled as a bounded Join-Semilattice:
$$\mathcal{P}_{\text{resolved}} = \mathcal{P}_1 \sqcup \mathcal{P}_2 \sqcup \dots \sqcup \mathcal{F}_n$$

## Lattice Resolution Rule: "Highest Standard Wins"

- **Complexity Limits**: Evaluated as the greatest lower bound ($\min$).
- **Review Approvals & SLSA**: Evaluated as the least upper bound ($\max$).
- **Linters & Features**: Cumulative deduplicated set union ($\cup$).
- **Memory & Error Unwraps**: The stricter setting wins (ZeroFrameMalloc, StrictBan).

## Layer Order

```figure
lattice-join
```

`ResolvePolicy` folds the layers in a fixed order: the built-in defaults, the profiles and
facets pinned in `.standards.lock`, the external fleet, organization, deployment and
workstation layers, the repository's `overrides.complexity`, and, for an audit, the
audit-compatibility ceiling. A profile or facet whose file no longer matches its lock digest is
rejected. The repository's branch-protection and supply-chain overrides apply after the join
(`ApplyOverrides`), so they can only tighten it; `review_mode` may relax to
`single_maintainer` there and nowhere else.
