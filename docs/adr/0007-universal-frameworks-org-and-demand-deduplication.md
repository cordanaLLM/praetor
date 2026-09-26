# ADR-0007: Universal Frameworks Org, Fleet Demand Deduplication & Cross-Repo Dependency Reconciliation

## Status

Accepted

## Context

As the fleet expanded across multiple programming languages (Go, Svelte/TypeScript, Python, Rust, Native C/GPU), disparate repositories independently adopted third-party dependencies, leading to duplicate libraries, inconsistent runtime hygiene, fractured agent context, and duplicated maintenance overhead.
Furthermore, the `golusoris` GitHub organization was historically coupled solely to Go-specific libraries, lacking an official fleet-wide home for cross-language builder kits, while multi-repo pre-migration epics lacked automated dependency unblocking when upstream issues were resolved.

## Decision

1. **Transition `golusoris` to the Universal Frameworks & Builder Kits Hub**:
   - `golusoris` is formally designated as the central organization hosting universal framework kits across all languages: Go (`golusoris`/`goenvoy`), Svelte (`sveltesentio`), Python (`pykit`), Rust (`rustkit`), and Native GPU (`template-native-gpu`).
2. **Polyglot Demand Extraction & Upstream Deduplication**:
   - Praetor's `internal/needs` engine is extended to analyze language manifests across Go (`go.mod`), Svelte/Node (`package.json`), Python (`requirements.txt`, `pyproject.toml`), Rust (`Cargo.toml`), and Native C/Meson (`meson.build`, `CMakeLists.txt`).
   - Demands across local and offline workstation inventories (`harvest/office-kcromm`) are aggregated and deduplicated into prioritized Upstream Framework Demand Requests targeting `golusoris`.
3. **Pre-Migration Epics & Cross-Repo Dependency Reconciler**:
   - Repositories preparing for migration receive structured pre-migration epics decomposing tasks into invariant hygiene, config decoupling, dependency substitution, and verification gating.
   - Praetor's forge layer provides `ReconcileEngine`, parsing tasklist checkboxes (`- [x]`) and cross-repo dependencies (`Depends-On: owner/repo#123`) to automatically transition blocked downstream issues to `status/ready-for-work`.
4. **Universal Builder & Pre-Build Optimizer**:
   - A single universal builder (`internal/builder`) compiles polyglot targets driven by `.framework-build.yaml`.
   - The Pre-Build Optimizer prunes unused dependencies, purges Tailwind CSS, and strips symbols based on declared `.needs.yaml` capabilities.
5. **Framework Kit Asset Compiler**:
   - The asset compiler (`internal/compiler/framework_assets.go`) generates dual-surface docs (`llms.txt`, `llms-full.txt`), agent rules (`.agents/rules/`), and starter templates for non-Go kits.

## Consequences

- **Positive**: Drastically cuts maintenance overhead by deduplicating third-party packages across the fleet into shared builder kits.
- **Positive**: Provides fully automated cross-repo issue tracking and dependency unblocking for multi-repo migrations.
- **Positive**: Unifies build toolchains and pre-build capability pruning across Go, Svelte, Python, Rust, and Native GPU.
- **Negative**: Requires downstream repositories to define `.needs.yaml` and participate in periodic fleet demand sweeps.
