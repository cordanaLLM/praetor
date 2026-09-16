---
name: understand-codebase
description: Systematically inspect, analyze, and build an authoritative mental model of a codebase using declarative manifests, lattice configurations, entry points, and HISS invariants.
---

# Codebase Comprehension (`understand-codebase`)

Quickly and authoritatively map any repository's architecture, operational contracts, and invariant enforcement surfaces.

## 4-Step Systematic Exploration Ladder

1. **Declarative Ground Truth Discovery**:
   - Inspect `.standards.yaml` to identify assigned **Profiles** (`native-gpu-systems`, `framework`, etc.) and **Facets** (`security:high`, `api:public-contract`, etc.).
   - Read `AGENTS.md` to internalize the active operational rules and required verification commands.
   - Inspect `.standards-baseline.json` to understand existing legacy technical debt baselines.

2. **Entry Point & Command Mapping**:
   - Locate CLI entrypoints in `cmd/` or package definitions in `package.json` / `Cargo.toml`.
   - Inspect `Makefile` to discover primary build, test, and verification pipelines (`make verify-all`).

3. **Subsystem & Dependency Graph Analysis**:
   - Trace internal domain packages (`internal/` or `src/lib/`).
   - Confirm control flow adheres to **HISS-01**: call graphs must form a Directed Acyclic Graph (DAG) with zero recursion.
   - Identify shared interfaces, data models, and storage boundaries.

4. **Live Verification & Sanity Check**:
   - Run verification tools to confirm current local build health before proposing edits:
     ```bash
     go test -v -race ./...
     go run ./cmd/standardsctl audit
     ```

## Output Deliverable: The Repository Infocard

When concluding a comprehension sweep, synthesize the architecture into this structured schema:

| Attribute | Declared Value |
| :--- | :--- |
| **Repository** | `<owner>/<name>` |
| **Active Profiles** | `[profile-1, profile-2]` |
| **Composed Facets** | `[facet-1, facet-2]` |
| **Strictness Lattice** | Cyclomatic $\le X$, SLSA Level $Y$ |
| **Primary Entrypoints** | `cmd/standardsctl`, `cmd/standards-mcp` |
| **Key Invariants** | HISS-01 (DAG), HISS-04 (McCabe $\le 10$), HISS-16 (Context Integrity) |
