# ADR-0012: Context Compilation, Gating, Delivery and Runner Routing as They Stand

## Status

Accepted — 2026-09-26. Supersedes ADR-0001, ADR-0003, ADR-0004, ADR-0005 and ADR-0006.

## Context

Five Accepted records describe behaviour the tree no longer has, or never had. An Accepted body is
immutable (`.agents/skills/adr-scaffold/SKILL.md`, "Immutability"), so the only allowed correction
is a superseding record. This record restates each decision as the code implements it. The five
older records keep their text; only their Status line changes, to `Superseded by ADR-0012`.

Measured at `1b9257b8` plus the fixes on the same branch as this record (the gating stage's
function-length resolution and the pre-commit context classification). The operational fork was
read at `lusoris/praetor` `main` `88a39dd9`.

| Record | Claim | Measured | Evidence |
| :-- | :-- | :-- | :-- |
| ADR-0001 | Aider is one of the covered vendors | `compile-context` writes six vendor files, none of them for Aider; the Codex target the record omits is one of the six | `internal/agentcontext/render.go:47-54` |
| ADR-0003 | the engine keeps "100% test coverage" and a "100% zero-debt baseline" | CI enforces a 65% total statement-coverage floor; existing debt is recorded in `.standards-baseline.json` and only new or touched-file debt fails | `.github/workflows/ci.yml:196`, `internal/gating/pipeline.go` (`runHissStage`), `cmd/standardsctl/audit.go` (`auditBaselineAndInvariants`) |
| ADR-0003 | verified releases and signed tags flow downstream | no `v*` tag exists, so the tag-triggered release workflow has never produced a release (#205) | `git tag -l 'v*'` is empty; `.github/workflows/release-binaries.yml:3-7` |
| ADR-0004 | a 4-stage pipeline scanning HISS-01 through HISS-16 at <= 60 lines per function | six stages; the HISS scan emits HISS-01, 02, 04, 07, 08 and 09; the function-length limit comes from the repository's resolved policy | `internal/gating/pipeline.go` (`executeStages`, `hissScanOptions`), `internal/hiss/rules.go`, `internal/hiss/go_ast.go` |
| ADR-0005 | GitOps delivery through ArgoCD at `deploy/k8s/application.yaml` | `deploy/` in this repository holds only the Helm chart; the ArgoCD Application is in the operational fork; no workflow builds or publishes an image | `git ls-files deploy`, `lusoris/praetor:deploy/k8s/application.yaml`, `.github/workflows/*.yml`, `.goreleaser.yaml` (no `dockers` section) |
| ADR-0005 | Praetor's own image builds from `docker/dev/Dockerfile` | that file is the development container; the production image is `build/package/Dockerfile` | `docker/dev/Dockerfile`, `build/package/Dockerfile:3,28,43` |
| ADR-0006 | Linux jobs run on ARC scale sets for amd64, arm64 and GPU | the routing policy still resolves those names, but no workflow routes through it: CI runs on GitHub-hosted `ubuntu-latest`, with a `ubuntu`/`macos`/`windows-latest` portability matrix; the operational fork defines `arc-runner-set-linux-amd64` and `arc-runner-set-gpu-xpu` and no arm64 set | `internal/config/hierarchy.go:38-49`, `.github/workflows/*.yml` (`runs-on`), `lusoris/praetor:deploy/arc/runner-scale-set.yaml` |

## Decision

### 1. Context compilation (supersedes ADR-0001)

`AGENTS.md` is the single source of agent instructions. `praetorctl compile-context` writes exactly
six vendor files, each within the 300-line budget (`internal/agentcontext/render.go:8`):
`CLAUDE.md`, `.cursor/rules/hiss-invariants.mdc`, `.github/copilot-instructions.md`,
`.windsurfrules`, `.gemini/GEMINI.md` and `.codex/rules.md`. It projects each persona in
`.agents/agents/` into `.claude/agents/`, `.codex/agents/`, `.github/agents/` and `.gemini/agents/`
(`internal/compiler/agents.go`), and into the plugin copy under `.agents/plugins/praetor/` when the
plugin manifest exists, together with the skills in `.agents/skills/`.

`compile-context --verify` checks every one of those outputs and fails on drift. The pre-commit hook
runs it for any change to a path it reads or writes (`CONTEXT` and `CONTEXT_PREFIXES` in
`.config/lefthook/scripts/checks.py`), and `praetorctl audit` verifies the six vendor files as well.
A vendor with no entry in `vendorTargets` is not covered; covering one means adding that entry.

### 2. Engine and operational fork (supersedes ADR-0003)

The two-repository topology stands. `cordanaLLM/praetor` is the engine; `lusoris/praetor` is the
operational fork that runs on workstations, carries operator data and reports demand through
`.needs.yaml`. Paths only the fork may carry are engine schema in
`internal/operationalsync/overlay.go` (`ownerOnlyPrefixes`), and the engine's `.gitignore` ignores
each of them.

Engine changes pass the gating pipeline (decision 3) and the debt ratchet: debt recorded in
`.standards-baseline.json` is tolerated, new debt and debt in a touched file fail. CI enforces a
total statement-coverage floor of 65% (`.github/workflows/ci.yml:196`), raised as coverage grows and
never lowered. Releases are cut by pushing a `v*` tag, which runs
`.github/workflows/release-binaries.yml`; none has been cut yet (#205), so nothing flows downstream
as a release today.

ADR-0003's checkable clause `engine-scope-has-no-exempt-tier` stays in force. `praetorctl adr verify`
replays it from ADR-0003, whose body is unchanged.

### 3. Gating pipeline (supersedes ADR-0004)

`praetorctl gate run` executes six stages in order and stops at the first failure
(`internal/gating/pipeline.go`, `executeStages`):

1. **Prefetch & Lockfiles**: `.standards.yaml` and `.standards.lock` must exist and be non-empty;
   `go mod verify` and `go mod download` run where there is a `go.mod`.
2. **HISS Invariant Scan**: the scanner reports HISS-01, 02, 04, 07, 08 and 09. The function-length
   limit is the one `config.ResolveRepositoryComplexity` resolves, the same limit `praetorctl audit`
   enforces for a locked repository. The stage fails on violations beyond `.standards-baseline.json`
   and on an incomplete scan.
3. **Security & SCA Scan**: `govulncheck` and `gosec -conf .gosec.json`; a missing scanner or
   configuration fails the stage.
4. **Flavor Conformance**: `flavor.AuditFlavor`; skipped, with the reason printed, where the declared
   profile has no flavor.
5. **Race-Detector Tests**: `go test -race` in an isolated worktree; skipped with a reason on a dry
   run, without `go.mod`, or without a working cgo toolchain.
6. **Ed25519 Exit-0 Receipt**: signs the concatenated stage output, skip reasons included, with the
   key `lockdown.LoadSigningKey` resolves; a missing key fails the stage and a dry run mints none.

The `main` ruleset requires a pull request and the CI status checks
(`.github/rulesets/main.json`), and CI's `forge validate-pr` step verifies the receipt in the pull
request body.

### 4. Container delivery (supersedes ADR-0005)

The production image is defined by `build/package/Dockerfile`: a `golang:` builder at the `go.mod`
toolchain version compiling with `CGO_ENABLED=0`, and a `gcr.io/distroless/static-debian12:nonroot`
runtime running as `65532:65532`. The Helm chart `deploy/helm/praetor` sets
`readOnlyRootFilesystem: true`, a memory-backed `emptyDir` for scratch space and probes on
`/livez` and `/readyz`; `praetorctl serve` answers `/healthz`, `/livez` and `/readyz` on `:8080`.

No workflow builds or publishes the image. The chart's default image reference
(`deploy/helm/praetor/values.yaml:3-6`) therefore points at a tag nothing has pushed; an operator
builds and pushes the image, or overrides the reference, until a publish workflow exists. GitOps
wiring such as an ArgoCD Application is operator data and lives in the operational fork under
`deploy/k8s/`, never in the engine.

### 5. Runner routing (supersedes ADR-0006)

Runner routing is policy resolution, not CI configuration. `config.LoadCascadingRunnerConfigContext`
starts from `DefaultRunnerPolicy` (`internal/config/hierarchy.go:38-49`) and applies, in order,
`.config/fleet.yaml`, `.config/orgs/<org>.yaml` and the `runners:` key of `.standards.yaml`.
`runner.ResolveRunner` maps a target to a runner and rejects Darwin on a self-hosted ARC runner, and
`praetorctl audit` checks that five targets resolve (`auditRunnerMatrix`). No workflow in this
repository consumes the result: CI runs on GitHub-hosted runners.

ARC scale sets are operator data (`deploy/arc/` is an owner-only prefix). The operational fork
defines `arc-runner-set-linux-amd64` and `arc-runner-set-gpu-xpu`. The default policy's
`arc-runner-set-linux-arm64` has no scale set there, so a job routed to it by the defaults waits for
a runner until an operator defines that set or overrides the route.

## Consequences

- **Positive**: every current-state claim in these five areas points at a file, a command or a test,
  and the superseded records say so on their first line.
- **Positive**: the checkable clause below turns "operator data stays in the fork" into a replayed
  check instead of a sentence.
- **Negative**: the image and ARC gaps remain gaps. This record states them; it does not add a publish
  workflow or an arm64 scale set.
- **Neutral**: ADR-0001 through ADR-0006 bodies are unchanged; readers of an older record follow its
  Status line here.

## Checkable clauses

```adr-constraint
id: operator-deployment-data-stays-in-the-fork
kind: forbidden-path
forbids:
  - "deploy/arc/"
  - "deploy/k8s/"
  - ".config/fleet.yaml"
  - ".config/orgs/"
rationale: >-
  ARC scale sets, GitOps Applications and fleet or organization runner policy name one operator's
  organization, secrets and capacity. They are owner-only paths in internal/operationalsync
  (ownerOnlyPrefixes) and live in the operational fork. A copy tracked in the engine would ship one
  operator's data to every adopter and drift from the fork's copy.
```

## References

- Issue #355 (items 1 and 2), #205 (first release), #222 (operator data is configuration).
- ADR-0001, ADR-0003, ADR-0004, ADR-0005, ADR-0006 (superseded by this record).
