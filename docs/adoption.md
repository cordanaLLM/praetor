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

### What Adoption Scaffolds Automatically

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
    runs-on: ubuntu-26.04
    steps:
      - uses: actions/checkout@v4
      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@main
        with:
          mode: adopt
          force: true
```

Comment `/adopt` on any PR to have `cordana-standards[bot]` automatically scaffold Praetor governance and commit the baseline.

### Inputs, the binary, and the `report` output

| Input | Reaches | Effect |
| :-- | :-- | :-- |
| `path` | `PRAETOR_PATH` | `--path=<value>`, and the `--source`/`--target-dir` of the `compile-context --verify` that follows an adopt run; an empty value is refused before anything runs |
| `mode` | `PRAETOR_MODE` | selects the subcommand, `adopt` or `dogfood`; any other value is refused |
| `dry-run` | `PRAETOR_DRY_RUN` | `--dry-run=<value>`; in `dogfood` mode it changes nothing, because `dogfood` applies adoptions only to `--targets` repositories (`testTargetAdoptions` in `internal/dogfood/dogfood.go`) and the action passes none, so the host is audited either way |
| `force` | `PRAETOR_FORCE` | `--force=<value>`, adopt only |
| `record-baseline` | `PRAETOR_RECORD_BASELINE` | `--record-baseline=<value>`, adopt only |
| `go-version` | `actions/setup-go` | the toolchain the step compiles `standardsctl` with; it never reaches `standardsctl`, and it has to satisfy the `go` directive of praetor's `go.mod` |

All five runtime inputs reach the run step as environment variables rather than as expressions
spliced into its script, so a value carrying a shell metacharacter or a newline is data rather
than script. `path` and the three booleans are then passed to `standardsctl` as single arguments;
`mode` is not passed at all, it picks the subcommand. `mode` and the booleans are validated
first: `mode: Dogfod` is a failure, not a silent `adopt` run. Each boolean is passed as
`--flag=<value>` rather than added when it is `true`, because `dogfood --dry-run` and
`adopt --record-baseline` default to true in `standardsctl` — an omitted flag would be an opt-in,
so `record-baseline: "false"` would have had no effect.

The `standardsctl` that runs is the one the action's own ref carries: the step builds
`cmd/standardsctl` out of the praetor checkout that `GITHUB_ACTION_PATH` points into, so
`praetor-adopt@<tag>` and `@main` differ, and `@latest` builds the commit this repository's moving
`latest` tag points at ([releasing](guides/releasing.md)). `standardsctl` itself is never
installed from the module proxy.

Outside praetor's own repository, reference the action as
`cordanaLLM/praetor/.github/actions/praetor-adopt@<ref>`. The directory three levels above the
action has to declare
`module github.com/cordanaLLM/praetor` and hold `cmd/standardsctl`, and the step fails before
anything runs when it does not:

- `uses: ./.github/actions/praetor-adopt` in an adopter repository — that directory is the
  adopter's own workspace.
- A copy of the action kept in another repository, such as `acme/ci/...@main` — the ref GitHub
  resolved names a commit of `acme/ci`, not of praetor. Installing praetor under that name would
  run a branch tip for `@main`, the newest `v1.x.x` tag for `@v1`, and the newest release for
  `@latest` ([Go modules reference, version queries](https://go.dev/ref/mod#version-queries)),
  none of which the caller pinned.

The error names the `uses:` form to switch to.

The `report` output carries the combined output of the run. The step writes it — and, when the
runner provides `GITHUB_STEP_SUMMARY`, appends it to the job summary — before re-raising the
command's exit status, and it does so for a refused input as well as for a failed run. Read it
from the job summary after a failure: whether a composite action's declared output still reaches
the caller once one of its steps has exited nonzero is not something GitHub documents. The append
itself is executed by the tests below; the survival of the output is what stays unasserted.

```yaml
      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@main
        id: praetor
        with:
          mode: dogfood
          dry-run: "true"
      - run: echo "$REPORT" >> "$GITHUB_STEP_SUMMARY"
        env:
          REPORT: ${{ steps.praetor.outputs.report }}
```

Everything above is pinned by `internal/forge/adopt_action_test.go`, which executes the action's own
shell bodies against a stub binary and reads the input defaults out of `action.yml`.

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

## Lock verification outcomes

Every command that reads `.standards.lock` uses one validator,
`ValidateLockfileWithOptions` in `internal/config/lock.go`. It recomputes each
declared profile and facet digest from a catalog. The catalog is the `.config/archetypes`
directory under `--catalog-root` (MCP: `catalog_root`), or under the repository when
no catalog root is given. The effective policy gate resolves the same catalog, so
both gates hash the same bytes.

| Outcome | Meaning |
| :-- | :-- |
| verified | Every declared entry's catalog file hashed to its pin. |
| unverifiable | Pins and the aggregate digest are valid, but the catalog has no `.config/archetypes` directory. |
| invalid | A version, pin, digest or catalog defect. Invalid locks are errors, never a status. |

A catalog that exists must define every declared id. An archetype whose `id:` field
was changed, or whose file was deleted, fails with `ErrLockSourceMissing` instead of
skipping the digest comparison.

Each command handles `unverifiable` as follows:

- `praetorctl audit` and the MCP `standards_audit` tool fail with `ErrLockUnverifiable`.
- `praetorctl sync` prints `[UNVERIFIED]` and exits incomplete; `--catalog-root` selects a catalog.
- `praetorctl adopt` records the outcome with a warning; `--lock-source-root` verifies against that bundle.
- `praetorctl harvest onboard` completes with `lock_verified: false` and `lock_status: unverifiable`.
- Lock generation refuses a source bundle without a catalog.

Generated locks omit `generated_at`; a lock that sets it still validates. The
behavior is pinned by `internal/config/lock_test.go`,
`cmd/standardsctl/lockdigest_test.go`, `cmd/standardsctl/sync_validation_test.go`,
`internal/adopt/lock_test.go` and `internal/harvester/onboard_safety_test.go`.
