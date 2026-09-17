# Architectural Decision Records (ADRs)

This directory documents all significant architectural and design decisions governing `cordanaLLM/praetor`. Each decision record captures the context, options considered, decisions taken, and lasting consequences for fleet engineering.

---

## Decision Lattice Index

| ADR ID | Title | Status | Date |
| :--- | :--- | :--- | :--- |
| **[ADR-0001](0001-universal-context-transpiler.md)** | Universal Multi-Agent Context Transpiler | **Accepted** | 2026-09-01 |
| **[ADR-0002](0002-highest-standard-wins-lattice.md)** | Highest-Standard-Wins Fleet Governance Lattice | **Accepted** | 2026-09-02 |
| **[ADR-0003](0003-canonical-engine-and-reverse-dogfooding-topology.md)** | Canonical Engine & Reverse-Dogfooding Topology | **Accepted** | 2026-09-03 |
| **[ADR-0004](0004-prefetch-dogfood-gating-pipeline.md)** | Ephemeral Worktree Gating & Ed25519 Exit-0 Receipts | **Accepted** | 2026-09-04 |
| **[ADR-0005](0005-cloudnative-oci-distroless-containers.md)** | Cloud-Native OCI Distroless Containerization | **Accepted** | 2026-09-05 |
| **[ADR-0006](0006-hierarchical-multi-tier-runner-matrix.md)** | Hierarchical Multi-Tier Runner Matrix | **Accepted** | 2026-09-06 |
| **[ADR-0007](0007-universal-frameworks-org-and-demand-deduplication.md)** | Universal Frameworks Hub & Demand Deduplication | **Accepted** | 2026-09-07 |
| **[ADR-0008](0008-spec-driven-provider-integration.md)** | Spec-Driven Provider Integration | **Proposed** | 2026-09-12 |
| **[ADR-0009](0009-structural-unification.md)** | Structural Unification Before Further Fix Waves | **Proposed** | 2026-09-12 |
| **[ADR-0010](0010-text-register-per-task.md)** | Text Register per Audience and Task Class | **Proposed** | 2026-09-17 |

---

## Lifecycle & Governance

1. **Immutable Numbering**: ADR identifiers are strictly monotonically increasing (`0001`, `0002`, ...).
2. **Deterministic Statuses**: Records transition through `Draft` $\rightarrow$ `Proposed` $\rightarrow$ `Accepted` $\rightarrow$ `Superseded`.
3. **Receipt Binding**: Any PR modifying or proposing an ADR requires an Ed25519 Exit-0 receipt signed by `standardsctl gate`.
