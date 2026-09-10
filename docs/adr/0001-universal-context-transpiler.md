# ADR-0001: Universal Cross-Agent Context Transpiler from Canonical `AGENTS.md`

## Status
Accepted

## Context
Autonomous AI coding agents (Anthropic Claude Code, Cursor, GitHub Copilot, Codeium Windsurf, Google Antigravity / Gemini CLI, Aider) each rely on vendor-specific instruction formats (`CLAUDE.md`, `.cursor/rules/*.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md`).
Maintaining separate configuration files by hand inevitably leads to configuration drift, conflicting instructions, and token window exhaustion.

## Decision
We establish a single canonical source of truth: `AGENTS.md`.
All vendor-specific configurations are deterministically compiled using `standardsctl compile-context`.
The compiler enforces a strict line budget constraint ($< 300$ lines) on every compiled artifact to prevent context window bloat and maximize KV-cache reuse.
Manual editing of compiled vendor files is strictly prohibited and blocked by pre-commit hooks and CI gates (`standardsctl compile-context --verify`).

## Consequences
- **Positive**: Single file to maintain for all agent instructions; zero configuration drift across IDEs and CLIs.
- **Positive**: Strict token and line budgets protect agent reasoning context.
- **Negative**: Developers must run `standardsctl compile-context` or rely on Lefthook pre-commit automation when updating instructions.

