# ADR-0006: Hierarchical Multi-Tier Action Runner Matrix (ARC / GitHub / Cloud)

## Status

Superseded by [ADR-0012](0012-current-delivery-runner-and-pipeline-state.md) — 2026-09-26 (previously Accepted).

## Context

Across the Cordana fleet, continuous integration and verification jobs execute across diverse target operating systems (Linux, Darwin/macOS), hardware architectures (`amd64`, `arm64`), and specialized accelerators (GPU/XPU).
Workstation clusters and private Kubernetes infrastructure running Actions Runner Controller (ARC) cannot execute Darwin/macOS workloads, while running heavy Linux compilation or GPU tests on GitHub-hosted public runners is cost-prohibitive and lacks customized toolchains.

## Decision

We implement a 4-tier cascading runner configuration and matrix routing engine:

1. **Tier 1 (Fleet Baseline)**: `.config/fleet.yaml` establishes universal runner defaults across all repositories.
2. **Tier 2 (Organization Overrides)**: `.config/orgs/<org>.yaml` provides organization-level routing policies (e.g. `cordanaLLM` vs `lusoris`).
3. **Tier 3 (Repository Overrides)**: `.standards.yaml` specifies repo-specific runner routing rules under the `runners:` key.
4. **Tier 4 (Target Routing & Platform Constraint Enforcement)**:
   - Darwin (`darwin/arm64`, `darwin/amd64`) targets route automatically to GitHub-hosted runners (`macos-14`, `macos-13`). Running Darwin on self-hosted Linux ARC is flagged as a platform constraint violation.
   - Linux targets (`linux/amd64`, `linux/arm64`) route to self-hosted Kubernetes ARC scale sets (`arc-runner-set-linux-amd64`, `arc-runner-set-linux-arm64`).
   - Hardware accelerator tasks (`linux/gpu`) route to dedicated GPU scale sets (`arc-runner-set-gpu-xpu`).

## Consequences

- **Positive**: Eliminates failed CI jobs caused by attempting to schedule macOS jobs on Linux ARC runners.
- **Positive**: Maximizes cost efficiency by offloading Linux/GPU workloads to self-hosted ephemeral Kubernetes runner scale sets.
- **Negative**: Requires maintaining ARC Kubernetes infrastructure alongside GitHub Action secrets.
