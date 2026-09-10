---
title: Welcome to cordanaLLM/standards
description: Enterprise Fleet Governance, Repository-as-Code & Universal AI Agent Engineering Engine
---

# cordanaLLM/standards Documentation

Welcome to the official documentation portal for `cordanaLLM/standards`.

## Architecture Overview

```mermaid
flowchart TD
    CONFIG[".standards.yaml"] --> COMPILER["standardsctl compile-context"]
    COMPILER --> AGENTS["AGENTS.md (Canonical)"]
    AGENTS --> VENDORS["CLAUDE.md / Cursor / Copilot"]
    CONFIG --> CI["CI Status Checks (HISS-16)"]
```

## Highlights
- **High-Integrity Systems Standard (HISS-16)**: Aerospace-derived software invariants.
- **Composable Lattice Architecture**: Highest standard wins deterministic join-semilattice.
- **SEO & Search Optimized**: Automated sitemaps, JSON-LD Schema.org metadata, and zero CLS styling.
