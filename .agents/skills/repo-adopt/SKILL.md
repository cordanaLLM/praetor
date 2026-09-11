---
name: repo-adopt
description: Adopt, conform, and bootstrap any repository into 100% Praetor governance compliance in a single command, automatically ratcheting technical debt for brownfield codebases.
---

# Repository Adoption & Template Conformance (`repo-adopt`)

Adopt any codebase—greenfield, partially setup, or legacy brownfield—into the `cordanaLLM/praetor` governance lattice in a single deterministic command or AI chat turn.

## 4-Step Adoption Workflow

1. **Dry-Run State Assessment**:
   - Preview planned scaffolding actions and archetype classification via CLI:
     ```bash
     praetorctl adopt /path/to/repo --dry-run
     ```
   - Or execute via MCP:
     ```json
     { "name": "standards_adopt", "arguments": { "path": ".", "dry_run": true } }
     ```
   - Verify state (`greenfield`, `partial`, or `brownfield`) and auto-detected archetype (`framework`, `native-gpu-systems`, `app-service`, etc.).

2. **Execute Full Repository Adoption**:
   - Run adoption to scaffold or reconcile all 7 governance layers:
     ```bash
     praetorctl adopt /path/to/repo --profile=framework --facets=security:high,api:public-contract
     ```
   - For brownfield legacy codebases, ensure `--record-baseline` is active to ratchet existing debt into `.standards-baseline.json`.

3. **Verify Scaffolding & Context Transpilation**:
   - Check generated files:
     - `.standards.yaml` & `.standards.lock`
     - `.standards-baseline.json`
     - `AGENTS.md` -> `CLAUDE.md`, `.cursor/rules/*.mdc`, `.windsurfrules`, `.github/copilot-instructions.md`, `.gemini/GEMINI.md`
     - `.devcontainer/devcontainer.json`
     - `.vscode/`, `.idea/`, `.nvim.lua`
     - `Makefile` & `.gitignore`

4. **Dogfood & Verification Gate**:
   - Validate adoption locally:
     ```bash
     praetorctl dogfood --path=/path/to/repo
     ```
   - Or test remote non-owned public repositories in dry-run mode:
     ```bash
     praetorctl dogfood --remote=https://github.com/org/repo --dry-run
     ```
   - Ensure the newly adopted repository builds and passes audit:
     ```bash
     cd /path/to/repo && make verify-all
     ```
