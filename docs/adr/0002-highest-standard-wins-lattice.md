# ADR-0002: Composable Archetype Facets and "Highest Standard Wins" Lattice Resolution

## Status

Accepted

## Context

Production codebases frequently span multiple functional domains (e.g. high-performance GPU kernels embedded within a Go or Python microservice). Single-inheritance archetype taxonomies fail because projects cannot be pigeonholed into a single type.
When multiple archetypes and security facets are composed together, conflicting rule parameters must be resolved deterministically without human adjudication.

## Decision

We model repository configuration as a bounded **Join-Semilattice** $(\mathcal{C}, \sqsubseteq, \sqcup)$ where strictness forms a partial order.
When multiple profiles or facets declare conflicting invariant thresholds, the engine computes the monotonic supremum (least upper bound):

- Complexity bounds: Greatest lower bound $\min(\text{Complexity}_A, \text{Complexity}_B)$.
- Max function length: Greatest lower bound $\min(\text{LOC}_A, \text{LOC}_B)$.
- Error unwraps: $\text{StrictBan} \sqcup \text{AllowWithComment} = \text{StrictBan}$.
- Memory allocation: $\text{ZeroFrameMalloc} \sqcup \text{StandardHeap} = \text{ZeroFrameMalloc}$.
- Review approvals: Least upper bound $\max(\text{Approvals}_A, \text{Approvals}_B)$.
- Linters & Features: Cumulative deduplicated union.

## Consequences

- **Positive**: Composable, multi-faceted repository configuration matching modern hybrid architectures.
- **Positive**: Mathematical guarantee that combining a security facet with a framework profile never weakens security ("Highest Standard Wins").
- **Negative**: Projects composing many facets inherit cumulative linter sweeps that must all be satisfied.
