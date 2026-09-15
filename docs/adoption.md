# Praetor Fast Adoption Guide

Apply Praetor governance scaffolding to a legacy or greenfield repository and retain explicit errors for incomplete steps. Verify the resulting repository before treating adoption as complete.

---

## 🚀 1-Step CLI Adoption

Run `standardsctl adopt` (or `praetorctl adopt`):

```bash
# Adopt current repository (auto-detects language and frameworks)
praetorctl adopt --lock-source-root=/path/to/praetor

# Dry-run simulation: inspect proposed changes without writing files
standardsctl adopt --dry-run

# Force overwrite existing configurations & record technical debt
praetorctl adopt --force --record-baseline --lock-source-root=/path/to/praetor
```

### Large repositories

Adoption discovers verification inputs (Makefiles, manifests, scripts) through a bounded
walk of the target: 4096 directory entries, 512 files and a fixed depth by default. A
repository above those bounds fails with `verification discovery exceeds 4096 entries`.
Raise a bound explicitly instead of trimming the tree:

```bash
standardsctl adopt --dry-run --path /path/to/large-repo \
  --lock-source-root=/path/to/praetor --verification-max-entries=32768
```

`--verification-max-files` and `--verification-max-depth` raise the other two bounds.
Each value is validated against its ceiling (200000 entries, 512 files, 64 levels); an
unset flag keeps the default. The flags apply to single-repository adoption; batch
`--all-missing` keeps the defaults.

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
    "record_baseline": true,
    "source_root": "/path/to/praetor"
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

## Migration: explicit sources for missing lockfiles

Live adoption no longer creates the old placeholder lock. Pass
`--lock-source-root=/path/to/praetor` (MCP: `source_root`) when a target has no
valid lock. The source must have a valid manifest/lock and every selected local
archetype source. Version pins come from that validated bundle, and digests come
from actual source bytes. The MCP source path obeys server root confinement.

An existing valid target lock is preserved. An invalid lock fails unless both
`--force` and an explicit source permit rebuilding it. A dry run without a source
reports lock generation as skipped; it cannot promise a complete adoption. Live
errors retain the partial report, since earlier scaffolding may already exist.
The same source option applies to `adopt --all-missing`; integrations invoking
adoption must supply it or arrange an already valid target lock.
