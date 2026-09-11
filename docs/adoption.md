# Praetor Fast Adoption Guide

Transform any legacy or greenfield repository into a 100% compliant Praetor-governed codebase in a single command or AI chat turn.

---

## 🚀 1-Step CLI Adoption

Run `standardsctl adopt` (or `praetorctl adopt`):

```bash
# Adopt current repository (auto-detects language and frameworks)
standardsctl adopt

# Dry-run simulation: inspect proposed changes without writing files
standardsctl adopt --dry-run

# Force overwrite existing configurations & record technical debt
standardsctl adopt --force --record-baseline
```

### What Adoption Scaffolds Automatically:
1. **`.standards.yaml`**: Declarative repository manifest containing profile, facets, tool versions, and policy locks.
2. **`.standards.lock`**: Cryptographic SemVer lockfile binding your repo to exact governance standard releases.
3. **`.standards-baseline.json`**: Technical debt ratcheting baseline. Existing infractions (e.g. legacy loop bounds, unwrapped errors) are recorded so legacy code compiles while new code is strictly gated.
4. **`AGENTS.md` + 6 Vendor Targets**: Canonical agent operating harness transpiled to `CLAUDE.md`, `.cursor/rules/*.mdc`, `.windsurfrules`, and `.github/copilot-instructions.md`.
5. **`.devcontainer/devcontainer.json`**: Multi-architecture container configuration pinned to verified base images.
6. **Multi-IDE Configs**: Workspace settings for VSCode, Cursor, JetBrains, and Neovim.
7. **Makefile & LeftHook**: Automated pre-commit hooks and standard verification targets (`make verify-all`).

---

## 🤖 AI Agent Adoption via MCP (`standards_adopt`)

If you are using Claude Code, Cursor, Gemini CLI, Antigravity IDE, or any MCP-compatible coding agent, you can onboard any repository by asking:

> *"Adopt this repository into Praetor governance."*

Under the hood, the agent executes the `standards_adopt` tool:

```json
{
  "name": "standards_adopt",
  "arguments": {
    "path": ".",
    "dry_run": false,
    "force": false,
    "record_baseline": true
  }
}
```

The tool returns a detailed summary of created and reconciled files, detected archetypes, and recorded legacy debt.

---

## ⚡ GitHub Action Bot & PR Automation

Add Praetor fast adoption to your GitHub repository using our composite action:

```yaml
name: Praetor Adoption Bot

on:
  issue_comment:
    types: [created]

jobs:
  adopt:
    if: github.event.issue.pull_request && contains(github.event.comment.body, '/adopt')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@main
        with:
          mode: adopt
          force: true
```

Comment `/adopt` on any PR to have `cordana-standards[bot]` automatically scaffold Praetor governance and commit the baseline.
