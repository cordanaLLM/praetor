# ADR-0003: Canonical Engine and Reverse-Dogfooding Fork Topology

## Status
Accepted

## Context
Praetor serves as the universal governance engine and repository-as-code standard for the Cordana ecosystem.
To maintain high velocity while guaranteeing system integrity, we require a clear separation between core framework development and live workstation deployment.
Directly committing unverified experimental code to the core engine introduces regressions, while developing entirely in isolation causes divergence from real-world developer workflows.

## Decision
We establish a two-tiered repository topology:
1. **Canonical Engine (`cordanaLLM/praetor`)**: The authoritative upstream repository containing core compilers, AST parsers, HISS invariants, container definitions, and runner matrices. All changes must satisfy the anti-direct-merge gating pipeline and 100% zero-debt baseline.
2. **Managing & Dogfooding Fork (`lusoris/praetor`)**: The active downstream fork deployed on real workstations. It harvests developer requirements, tests pre-release features, and reports capability demand to upstream via `.needs.yaml`.
3. **Reverse-Dogfooding Flow**: Requirements and bug discoveries flow from `lusoris/praetor` $\rightarrow$ upstream `cordanaLLM/praetor`. Verified releases and signed tags flow downstream from `cordanaLLM/praetor` $\rightarrow$ `lusoris/praetor`.

## Consequences
- **Positive**: Core engine remains pristine, stable, and hermetic with 100% test coverage and zero regressions.
- **Positive**: Downstream operations have a dedicated sandbox to discover edge cases and prototype real workflows before standardization.
- **Negative**: Requires synchronization discipline and automated prefetch/gating checks before promoting code upstream.
