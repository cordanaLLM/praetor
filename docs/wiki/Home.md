# cordanaLLM/praetor Wiki Portal

Welcome to the official repository governance wiki for cordanaLLM.

## Governance Lifecycle Architecture

```mermaid
flowchart LR
    AGENTS["AGENTS.md\n(canonical source)"] --> TRANSPILER["praetorctl compile-context"]
    TRANSPILER --> VENDORS["CLAUDE.md, .cursor/rules, copilot-instructions,\n.windsurfrules, GEMINI.md, .codex/rules.md"]
    MANIFEST[".standards.yaml\n+ .standards.lock"] --> AUDIT["praetorctl audit"]
    VENDORS --> GATES["Verification Cascade\n(make verify-all)"]
    AUDIT --> GATES
    GATES --> RECEIPT["Ed25519 Exit-0 Receipt"]
```

## Quick Navigation

| Document | Description |
| :--- | :--- |
| [[HISS-16-Invariants]] | The invariants AGENTS.md gates, with the rule and verification for each. |
| [[Architecture-Lattice]] | Mathematical join-semilattice and Highest Standard Wins resolution. |
| [[API-Reference]] | CLI commands, MCP tools, and multi-forge driver specifications. |
