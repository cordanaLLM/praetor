# cordanaLLM/praetor Wiki Portal

Welcome to the official repository governance wiki for cordanaLLM/praetor.

## Governance Lifecycle Architecture

```figure
governance-lifecycle
```

The lifecycle has three flows. `praetorctl compile-context` reads `AGENTS.md` and writes
the vendor instruction files. `.standards.yaml` and `.standards.lock` resolve to one
effective policy, which the devcontainer toolchain, `praetorctl audit` and `praetorctl plan`
apply. `praetorctl gate run` runs the gate stages, and a passing run mints the Ed25519
Exit-0 receipt.

## Quick Navigation

| Document | Description |
| :--- | :--- |
| [HISS-Invariants](HISS-Invariants.md) | The High-Integrity Systems Standard (HISS) and the invariants AGENTS.md gates, with the rule and verification for each. |
| [HISS-Matrix](HISS-Matrix.md) | The full HISS catalog of 21 invariants, HISS-01 through HISS-21, with the enforcement and failure action of each. |
| [Architecture-Lattice](Architecture-Lattice.md) | Mathematical join-semilattice and Highest Standard Wins resolution. |
| [API-Reference](API-Reference.md) | CLI commands, MCP tools, and multi-forge driver specifications. |
