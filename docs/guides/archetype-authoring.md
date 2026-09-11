# Archetype and Facet Authoring Guide

Learn how to define new composable profiles and cross-cutting security/operational facets in `cordanaLLM/praetor`.

```mermaid
flowchart TD
    NEW["New Technology Stack\n(e.g., zig-systems, ml-training)"] --> PROFILE[".config/archetypes/{id}.yaml"]
    CROSS["Cross-Cutting Invariant\n(e.g., zero-trust-network)"] --> FACET[".config/archetypes/facets/{id}.yaml"]
    
    PROFILE & FACET --> LATTICE["Lattice Engine (internal/config)"]
    LATTICE --> RESOLVE["Evaluates Supremum (Join)\nHighest Standard Wins"]
    RESOLVE --> CI["Tailored CI & Invariants"]
```

---

## 1. Profile Definition Anatomy
Profiles represent the primary technology stack or architecture. Create `.config/archetypes/{profile-id}.yaml`:

```yaml
id: "native-gpu-systems"
name: "Native GPU & Compute Systems"
description: "High-performance C/C++/Rust/Vulkan systems with deterministic memory bounds"
runtime: "native"

complexity:
  max_cyclomatic: 10
  max_cognitive: 12
  max_func_loc: 75
  max_statements: 40

memory:
  zero_frame_malloc: true
  banned_alloc_in_ticks: true

linters:
  - "clang-tidy"
  - "clippy"
  - "semgrep"

devcontainer_features:
  - "ghcr.io/devcontainers/features/rust:1"
  - "ghcr.io/devcontainers/features/common-utils:2"
```

---

## 2. Facet Definition Anatomy
Facets are cross-cutting policy modifiers. Create `.config/archetypes/facets/{facet-id}.yaml`:

```yaml
id: "security:high"
name: "High-Security Provenance & Hardening"
description: "SLSA Level 3 attestations, keyless Cosign signatures, and non-root execution"

supply_chain:
  slsa_level: 3
  enforce_cosign: true
  require_sbom: true

branch_protection:
  enforce_linear_history: true
  require_signed_commits: true
  required_approving_reviewers: 2
  dismiss_stale_reviews: true
```

---

## 3. The Strictness Lattice ("Highest Standard Wins")
When two profiles or facets define conflicting parameters, the monotonic supremum is calculated:
$$\mathcal{P}_{\text{resolved}} = \mathcal{P}_1 \sqcup \mathcal{P}_2 \sqcup \dots \sqcup \mathcal{F}_n$$
- Lower complexity limits win ($\min$).
- Greater security reviews and higher SLSA levels win ($\max$).
- Linters and container features form a deduplicated set union ($\cup$).

