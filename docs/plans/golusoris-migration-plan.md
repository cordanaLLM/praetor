# golusoris migration plan

Status of praetor's code migration onto `github.com/golusoris/golusoris/core`. Ported from the
closed PR #18 branch `feat/golusoris-core-onboarding` and re-derived against `main` (362ef66)
on 2026-09-15. Refresh the machine-checked block with
`praetorctl needs migrate --path=. --framework=<golusoris checkout>`.

## Already landed on main

- The needs capability contract resolves against `<framework>/capabilities.yaml` through
  `internal/needs/extract.go`, `framework.go` and `framework_observe.go`. PR #18 was closed as
  superseded; its needs work reached `main` directly, and the remainder is in open PR #51.
- The module path is `github.com/cordanaLLM/praetor`. PR #18's rename to the lower-cased
  `github.com/cordanallm/praetor` was not taken and is not planned here.
- `github.com/golusoris/golusoris/core` is published on the module proxy (v0.9.0, v0.9.1,
  v0.9.2). The earlier "apply once core/v0.9.0 is tagged" precondition is met; a local
  `replace` directive is no longer required.

## Machine-checked state

`praetorctl needs migrate --path=.` against a local golusoris checkout, on 362ef66:

```text
=== Migration Candidate: github.com/cordanaLLM/praetor -> github.com/golusoris/golusoris ===
Status: candidate | Framework version: unverified
Mapping availability: 0.0% | Coverage basis: source-observed; builds and tests not run
Blocker: No verified module version is bound to the selected framework source.
Blocker: Replacement API compatibility and consumer compilation/tests are unverified.
Blocker: Executable migration admission is unavailable until a real evidence validator exists.
Blocker: 1 dependencies have no selected framework mapping.
Added:
Proposed removals:
Proposed file import replacements: 0

[INFO] Dry-run complete. Application is blocked pending verified module version and API compatibility evidence.
```

This is a correction, not a regression: the plan PR #18 recorded ("Added: core v0.7.0, dropped
`gopkg.in/yaml.v3`, 14 file import replacements") came from a generator that emitted a plan
without binding a verified module version. `main` refuses to produce one, so the import rewrite
is driven by hand until an evidence validator exists. The file count is also stale: 50 files
under `main` import `gopkg.in/yaml.v3` (36 of them non-test), not 14.

The one unmapped dependency is `gopkg.in/yaml.v3` (capability `config.yaml`). The shipped
catalog in `internal/needs/catalog.go` still points `config.yaml` at
`github.com/golusoris/golusoris/config`, which predates golusoris's `core/` move; the real
replacement is `github.com/golusoris/golusoris/core/codec/yaml`. Correcting the catalog to the
`core/*` paths is tracked in PR #51.

## Optional capabilities declared in `.needs.yaml`

Package paths below are the `core/` layout verified against `core@v0.9.0`.

| Capability | golusoris package | Praetor code it replaces |
| :--- | :--- | :--- |
| `config.yaml` | `core/codec/yaml` | `gopkg.in/yaml.v3` across 50 files (36 non-test) |
| `clikit.cli` | `core/clikit` | hand-rolled flag dispatch in `cmd/standardsctl/main.go` |
| `mcp.server` | `core/mcp` | JSON-RPC transport in `cmd/standards-mcp/server.go` (keep the `internal/mcp` tool bridge) |
| `crypto.receipt` | `core/crypto/receipt` | `internal/lockdown/receipts.go` (payload wire-compatible) |
| `git.worktree` | `core/gitx/worktree` | `internal/worktree` (use `WithDir(".standards/worktrees")`) |
| `ast.analyzer` | `core/astx` | go.mod line parser and import string replacement in `internal/needs`, func-LOC scan in `internal/hiss` |
| `telemetry.logging` | `core/log` | `fmt.Println` / `os.Stderr` prints across `cmd/` |

## Supply-chain note

`core@v0.9.0` declares cobra, fx, koanf, validator, zap and the MCP SDK. Module graph pruning
keeps that out of praetor: importing only `core/codec/yaml` and `core/astx` adds
`go.yaml.in/yaml/v3` and `golang.org/x/mod` to `go.sum` and nothing else. Adopting `core/clikit`,
`core/mcp` or `core/log` would pull their own trees in, so each capability is a separate
decision under HISS-11, not one migration.
