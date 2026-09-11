# cordanaLLM/praetor Wiki Portal

Welcome to the official repository governance wiki for cordanaLLM.

## Governance Lifecycle Architecture

```mermaid
flowchart LR
    MANIFEST[".standards.yaml"] --> TRANSPILER["standardsctl compile-context"]
    TRANSPILER --> AGENTS["AGENTS.md\n(Canonical Truth)"]
    AGENTS --> GATES["Verification Cascade\n(make verify-all)"]
    GATES --> RECEIPT["Ed25519 Exit-0 Receipt"]
```

## Quick Navigation

| Document | Description |
| :--- | :--- |
| [[HISS-16-Invariants]] | Formal specification for all 16 aerospace-derived software invariants. |
| [[Architecture-Lattice]] | Mathematical join-semilattice and Highest Standard Wins resolution. |
| [[API-Reference]] | CLI commands, MCP tools, and multi-forge driver specifications. |
