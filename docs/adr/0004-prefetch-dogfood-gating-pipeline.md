# ADR-0004: Anti-Direct-Merge Prefetch & Dogfood Gating Pipeline

## Status

Accepted

## Context

Direct commits to `main` without isolated sandbox verification bypass supply chain integrity, static analysis, and race condition tests. In multi-agent autonomous engineering environments, unverified merges can rapidly break CI pipelines and cascade across downstream forks.

## Decision

We enforce a mandatory 4-stage anti-direct-merge gating pipeline (`standardsctl gate`):

1. **Stage 1 (Prefetch & Lockfiles)**: Verifies cryptographic hashes in `go.sum`, downloads dependencies with context timeouts, and ensures `.standards.yaml` and `.standards.lock` exist and are valid.
2. **Stage 2 (HISS Invariant Scan)**: Performs static AST inspection enforcing HISS-01 through HISS-16 invariants, including NASA JPL Rule 4 ($\le 60$ LOC per function) and strict zero-debt against `.standards-baseline.json`.
3. **Stage 3 (Race-Detector Tests)**: Executes `go test -v -race` across critical components in an isolated worktree.
4. **Stage 4 (Exit-0 Receipt & Merge)**: Emits a deterministic verification report. Direct pushes to `main` are blocked via GitHub rulesets; merge requires a verified gate run.

## Consequences

- **Positive**: Zero broken main builds; deterministic supply-chain prefetching prevents compromised or dangling dependencies.
- **Positive**: Automated enforcement eliminates human review bottlenecks for mechanical invariants.
- **Negative**: Pre-merge verification requires local computational overhead and execution time before merging.
