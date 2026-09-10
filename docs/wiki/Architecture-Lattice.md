# Composable Archetypes & Strictness Lattice

Repository configuration is modeled as a bounded Join-Semilattice:
$$\mathcal{P}_{\text{resolved}} = \mathcal{P}_1 \sqcup \mathcal{P}_2 \sqcup \dots \sqcup \mathcal{F}_n$$

## Lattice Resolution Rule: "Highest Standard Wins"

- **Complexity Limits**: Evaluated as the greatest lower bound ($\min$).
- **Review Approvals & SLSA**: Evaluated as the least upper bound ($\max$).
- **Linters & Features**: Cumulative deduplicated set union ($\cup$).

```mermaid
flowchart TD
    PROFILE["Profile: framework\n(Cyclomatic <= 10, Approvals: 1)"] --> LATTICE["Lattice Join Engine\n(internal/config)"]
    FACET["Facet: security:high\n(SLSA Level 3, Approvals: 2)"] --> LATTICE
    LATTICE --> RESOLVED["Resolved Policy\n(Cyclomatic <= 10, Approvals: 2, SLSA 3)"]
```
