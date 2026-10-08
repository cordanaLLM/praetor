# Contributing to Praetor

Thank you for contributing to Praetor, an open governance engine for any repository fleet. Every contribution must adhere to the [High-Integrity Systems Standard (HISS)](../standards/hiss-spec.md).

---

## Code of Conduct & Standards

All contributors are expected to uphold deterministic, high-integrity engineering practices:

- Zero-warning tolerance across compiler, linter, and format checks.
- HISS-15 requires a positive, a negative, and a boundary test for every public interface;
  CI additionally enforces a 65% total-statement-coverage floor
  (`COVERAGE_FLOOR` in `.github/workflows/ci.yml`), not 100% coverage.
- All commits must include Developer Certificate of Origin (`Signed-off-by: Your Name <email>`).

---

## Local Development & Verification

Before submitting any Pull Request, ensure local verification passes completely:

```bash
# 1. Run race-detected unit tests
go test -v -race ./...

# 2. Verify agent context synchronization
go run ./cmd/standardsctl compile-context --verify

# 3. Audit repository against declared HISS standards
go run ./cmd/standardsctl audit

# 4. Run all verification gates
make verify-all
```

### Go toolchain

The `toolchain` directive in `go.mod`, repeated in `tools/go/go.mod`, names the Go release that
builds and scans this repository on the host and in CI:

- Local builds follow it through `GOTOOLCHAIN=auto` (the Go default): a `go` older than the
  directive downloads the named release and runs it. `GOTOOLCHAIN=local` opts out and uses the
  installed release, so a `govulncheck` run with it can report standard-library advisories that
  the directive's release has fixed.
- CI jobs set Go up with `go-version-file: go.mod`. A version range, with or without
  `check-latest`, resolves through the `actions/go-versions` manifest, which can lag a Go
  release by days; an exact version missing from it is downloaded from go.dev instead.
  `TestSecurityGovuln_Negative_GateJobsResolveGoFromGoMod` fails a gate job that sets Go up
  any other way, and `TestAdoptWorkflow_Negative_PassesResolvedGoVersionToAction` fails a
  `praetor-adopt` call that does not pass the resolved release on.
- The DevContainer is the exception: it builds with the Go release of its digest-pinned builder
  image under `GOTOOLCHAIN=local` (`internal/devcontainer/bootstrap.go`), and Renovate moves
  that image in its own pull request.
- Renovate's `gomod` manager proposes updates to the directive by default, so a Go security
  release arrives as a dependency pull request, not as a CI change.

### Optional: Probity Test-First Guard

Probity is an optional client-side edit guard for contributors using AI coding assistants. When enabled, it enforces a test-first workflow by requiring an observed failing test before an implementation edit is allowed. Probity is an open-source tool created by [nizos/probity](https://github.com/nizos/probity) and licensed under the MIT license. Running Probity requires Node.js >= 22.

The repository root includes `probity.config.ts`, which scopes enforcement to Go source files under `internal/`, `cmd/`, and `tools/` (excluding `**/testdata/**`). The configuration pins the validation judge model to `claude-haiku-5-5` through the `ai` override using the Claude Agent SDK resolved from the pinned package install. The judge resolves its modules only when it judges a write. If a required module (such as `@anthropic-ai/claude-agent-sdk` or `./vendors/to-verdict.js`) cannot be resolved, each judged Go write is denied with a reason naming the missing module and the pinned Probity version (`1.10.1`), instead of being judged by the session model; shell commands and other files keep working, so you can reinstall the pinned version from the same session. Measured floor on 2026-10-08 (Probity 1.10.1, branch config, 4-line new Go file, no transcript): about 5.2k tokens per judged write across the claude-haiku-5-5 judge and an auxiliary claude-haiku-4-5 harness call, about 0.04 USD at the time of measuring. It grows with file size and the recent history Probity embeds (up to 10 events of 6000 characters by default). Contributors without the hook installed are unaffected.

Fail-closed warning: Probity fails closed when no configuration file is found. Installing the plugin at global (user) scope will block edits across every other repository on the machine that lacks a `probity.config.ts`. Always install with local scope (`--scope local`) or configure project-local hooks in `.claude/settings.local.json`. Never edit the tracked, Praetor-owned `.claude/settings.json`.

To enable Probity in Claude Code via the CLI:

```bash
claude plugin marketplace add nizos/probity
claude plugin install --scope local probity@probity
```

Note that the marketplace plugin tracks upstream HEAD because its hook executes unpinned `npx @nizos/probity`.

Alternatively, configure a pinned `PreToolUse` hook in `.claude/settings.local.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|Write|Edit|NotebookEdit",
        "hooks": [
          {
            "type": "command",
            "command": "npx --yes @nizos/probity@1.10.1 --agent claude-code"
          }
        ]
      }
    ]
  }
}
```

Contributors who installed `@nizos/probity` globally on their `PATH` may substitute `probity` for `npx --yes @nizos/probity@1.10.1`.

---

## Pull Request Lifecycle

1. **Feature Branch**: Create a branch off `main` (e.g. `feat/feature-name` or `fix/bug-name`).
2. **Deterministic Checks**:
   - `gofmt` code formatting.
   - `REUSE 3.3` licensing compliance.
   - AST complexity limits (McCabe $\le 10$, cognitive $\le 15$, function LOC $\le 60$
     -- the audit-compatibility ceiling `config.AuditMaxFuncLOC` always tightens a looser
     repository override, per `internal/hiss/hiss.go` and `internal/config/effective.go`).
3. **Receipt Generation**:
   - Commit first, then run `standardsctl gate run --path=.` to verify the ephemeral worktree and generate an Ed25519 Exit-0 receipt. The gate refuses a tree with uncommitted or untracked changes ([details](adoption-verification.md#a-receipt-certifies-only-a-working-tree-that-matches-head)).
4. **Pull Request Submission**:
   - Submit PR via GitHub. Direct pushes to `main` are declined by repository rules.
   - The receipt certifies the commit it was minted on, so every push stales the one in the PR
     body. `forge validate-pr` then refuses it and names the recovery (`receiptRecovery` in
     `internal/forge/pr.go`): run `praetorctl gate run --path=.` on a clean checkout of the
     pushed head, then replace the fenced `receipt` block with the new `.standards-receipt.json`.
     Editing the body re-runs the gate, since `.github/workflows/ci.yml` triggers on `edited`.
   - All 9 required status checks in `.github/rulesets/main.json` must pass before merge:
     `Release & Bot Configuration Validation`, `Documentation Preset Builds`,
     `Standards & Invariant Verification Gate`, `DCO 1.1 & REUSE Compliance Gate`,
     `Platform Neutrality (Linux)`, `Platform Neutrality (macOS)`,
     `Platform Neutrality (Windows)`, `Documentation Governance`, and
     `Go Vulnerability & AST Security Scan`.

## Renovate pull requests

A Renovate pull request is not merged as opened. The landing pipeline takes each one over on a
signed-off `fix/renovate-<number>` branch with its own pull request, and that pull request runs
the full CI and every required check.

CI on the Renovate pull request itself is skipped. A pull request counts as a Renovate pull
request only when both hold: its head branch starts with `renovate/`, and the Renovate app's bot
account `renovate[bot]` opened it. Each of these jobs leads its condition with
`!(startsWith(github.head_ref, 'renovate/') && github.event.pull_request.user.login == 'renovate[bot]')`:

| Workflow | Job skipped on a Renovate pull request |
| :--- | :--- |
| `.github/workflows/ci.yml` | `Standards & Invariant Verification Gate`, `Documentation Preset Builds` |
| `.github/workflows/portability.yml` | the three `Platform Neutrality` legs |
| `.github/workflows/security.yml` | `Go Vulnerability & AST Security Scan` |
| `.github/workflows/pages.yml` | `Build Documentation Site (canonical repository only)` |

- `DCO 1.1 & REUSE Compliance Gate`, `Release & Bot Configuration Validation` and
  `Documentation Governance` still run on the Renovate pull request.
- A person's pull request from a branch named `renovate/...` runs every job, and so does a pull
  request the app opens from any other branch (`TestRenovateSkipRequiresTheRenovateAuthor` in
  `internal/forge/workflow_guard_test.go`).
- The portability workflow's stated-reason job runs instead of the legs and names the Renovate
  pull request as the reason ([HISS-21](../standards/hiss-21-platform-neutrality.md)).
- Push runs are unchanged: `github.head_ref` and `github.event.pull_request` are set on pull
  request runs only.
- The skipped jobs stay required checks (`TestRenovateBranchesSkipOnlyTheHeavyPullRequestJobs`).
  GitHub reports a job its condition skipped as successful, but in this repository the skipped
  matrix reports no `Platform Neutrality` leg at all, so the Renovate pull request cannot satisfy
  the ruleset on its own. An operational fork that does not require the legs has no such stop
  ([Which workflows run where](operational-sync.md#which-workflows-run-where)).

`renovate.json` keeps the bot from refilling the runners and from merging anything:

- `rebaseWhen: conflicted` rebases a branch only when it conflicts with `main`. The default,
  `auto`, rebases every open branch whenever `main` moves, because the ruleset requires
  up-to-date branches.
- `automerge: false` holds for every rule. A skipped check reports success, so an automerging
  rule could merge an update that nothing verified.

Some updates need a step Renovate cannot make, and the takeover pull request fails CI until it
is made:

- The `reviewed devcontainer images` group moves the reviewed pin in
  `internal/devcontainer/bootstrap.go` and the `FROM` line of `docker/dev/Dockerfile`, never the
  generated bundle. Run `praetorctl devcontainer bump` on the takeover branch and commit what it
  writes: it records the replaced image in `internal/devcontainer/prior-images.json` and
  regenerates and verifies `.devcontainer/`
  ([moving a reviewed default image](devcontainer-bootstrap.md#moving-a-reviewed-default-image)).
- A DevContainer feature update moves the catalog under `.config/archetypes/`; re-pin
  `.standards.lock` and regenerate the bundle
  ([archetype authoring](archetype-authoring.md)).
- The documentation and API gate action groups need the outgoing workflow text recorded
  ([documentation governance](documentation-governance.md)).
