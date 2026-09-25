---
name: adr-scaffold
description: Scaffold and manage Architectural Decision Records (ADRs) adhering to immutable numbering, status lifecycle, context, decision lattice, and consequences.
---

# Architectural Decision Record Scaffolder (`adr-scaffold`)

Author and record significant architectural choices, invariant trade-offs, and governance policies in `docs/adr/`.

## ADR Lifecycle & Invariants

- **Sequential Numbering**: Identify highest existing number in `docs/adr/` and increment (`NNNN-kebab-title.md`).
- **Immutability**: Once ADR marked `Accepted`, decision text = immutable. Any change requires new ADR that supersedes prior one (`Superseded by ADR-NNNN`).
- **Mathematical / Formal Rigor**: Frame trade-offs against HISS invariants and strictness lattice.

## Standard ADR Template

Create `docs/adr/{NNNN}-{title}.md`:

```markdown
# ADR-NNNN: [Descriptive Decision Title]

## Status
[Draft | Proposed | Accepted | Superseded by ADR-XXXX]

## Context
[What problem are we trying to solve? What are the technological, operational, or fleet constraints? Which HISS invariants from the AGENTS.md "Core Directives & Invariants" table are impacted?]

## Decision
[What is the architectural or algorithmic choice? State the specific mechanisms, libraries, schemas, or protocols adopted. If resolving conflicts across profiles, demonstrate how the join-semilattice computes the supremum.]

## Consequences

### Positive
- [Benefit 1: Invariant guarantee, performance improvement, or simplification.]
- [Benefit 2: Agent clarity or determinism.]

### Negative / Trade-offs
- [Trade-off 1: Maintenance burden, compilation overhead, or migration cost.]

### Neutral
- [Observed neutral changes in workflow or tooling.]

## Verification & Compliance
[What automated test or lint sweep enforces this decision?]
```

## Checklist Before Submitting
1. [ ] Correct 4-digit zero-padded index (`0003`, `0004`, etc.).
2. [ ] Valid markdown with English prose in neutral professional register.
3. [ ] Row added to `docs/adr/README.md` "Decision Lattice Index" (every ADR: link, title, status, date) and entry added under "Decision Lattice Index" in `mkdocs.yml` nav.
4. [ ] Status follows `docs/adr/README.md` lifecycle: `Draft` -> `Proposed` -> `Accepted` -> `Superseded`; no other value.
5. [ ] PR proposing or modifying ADR carries Ed25519 Exit-0 receipt signed by `standardsctl gate` (`docs/adr/README.md` Receipt Binding).
