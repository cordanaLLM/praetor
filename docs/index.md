---
title: Praetor - Fleet Governance Engine
---

<div align="center">
  <img src="assets/praetor-readme-banner.svg" alt="Praetor Architecture Banner" width="100%" />
</div>

<div align="center">

[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![HISS-16 Verified](https://img.shields.io/badge/HISS--16-Verified-0e8a16?style=flat-square&logo=shield)](standards/hiss-16.md)
[![License: EUPL 1.2](https://img.shields.io/badge/License-EUPL_1.2-blue?style=flat-square)](https://joinup.ec.europa.eu/collection/eupl/eupl-text-eupl-12)
[![Polar.sh](https://img.shields.io/badge/Polar.sh-Feature_Bounties-000000?style=flat-square&logo=polar)](sponsoring.md)
[![llms.txt Enabled](https://img.shields.io/badge/llms.txt-Enabled-0075ca?style=flat-square)](llms.txt)

</div>

# Praetor Enterprise Fleet Governance

**Praetor** is the high-integrity autonomous fleet governance and universal AI agent engineering engine powering the CordanaLLM ecosystem. It enforces strict determinism, hermetic supply chain provenance (SLSA Level 3), bounded complexity, and cross-agent instruction synchronization across diverse software engineering organizations.

<div align="center">
  <img src="assets/praetor-mkdocs-icons.svg" alt="Praetor Architecture Pillars" width="100%" />
</div>

---

## 🏛️ Core Capabilities

| Pillar | Subsystem | Responsibility |
| :--- | :--- | :--- |
| **Pillar I** | `standardsctl compile-context` | Canonical single-source-of-truth context transpiler compiling `AGENTS.md` into Claude, Cursor, Copilot, Windsurf, Codex, and Gemini formats (< 300 LOC budget). |
| **Pillar II** | `standardsctl audit` | Comprehensive static analyzer sweeping source code, AST, rulesets, and baselines against High-Integrity Systems Standards (**HISS-01** through **HISS-16**). |
| **Pillar III** | `standards-mcp` | Model Context Protocol server exposing multi-transport tooling (`stdio`, `http`, `sse`) for autonomous agents. |
| **Pillar IV** | `standardsctl sync` & `plan` | Declarative repository-as-code reconciler for GitHub branch rulesets, merge policies, and label taxonomies. |
| **Pillar V** | `internal/sentinel` | Workstation resource guardian monitoring RAM, VRAM, and disk pressure before triggering local frontier model inference. |
| **Pillar VI** | `internal/bump` | Proactive prerelease upgrade train testing canary dependencies in isolated ephemeral git worktrees. |

---

## 🚀 Quickstart

Verify compliance and run local verification gates:

```bash
# Verify canonical agent context synchronization (HISS-16)
go run ./cmd/standardsctl compile-context --verify

# Audit repository against declared invariants and branch rulesets
go run ./cmd/standardsctl audit

# Execute full race-detected test harness
go test -v -race ./...

# Run all verification gates
make verify-all
```

---

## 📖 Dual-Surface AI Documentation

Praetor provides machine-readable documentation endpoints for autonomous agents:
- [`/llms.txt`](llms.txt): Concise, structured index of all architectural standards and API contracts.
- [`/llms-full.txt`](llms-full.txt): Complete unrolled technical specifications and invariant matrices.
