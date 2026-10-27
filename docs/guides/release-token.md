# Release-token gate

When GitHub Actions opens a pull request using the workflow's default `github.token` or `secrets.GITHUB_TOKEN`, GitHub does not emit new workflow trigger events for that pull request. A release pull request opened by `release-please` under the default workflow token therefore sits without required status checks, unable to trigger CI verification.

The release-token gate fails any workflow step invoking `release-please` that does not source its token from a dedicated GitHub App token step. The gate is `adopt.AuditReleaseToken` in [`internal/adopt/release_token_audit.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/release_token_audit.go) over the measurement in [`internal/forge/release_token.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/forge/release_token.go). Both `praetorctl audit` and the MCP tool `standards_audit` run it.

## Quick start

### Passing shape

Mint a GitHub App installation token in an earlier step of the same job, and pass its output token to `release-please`:

```yaml
jobs:
  release:
    runs-on: ubuntu-26.04
    steps:
      - name: Mint the release token
        id: app-token
        uses: actions/create-github-app-token@64ab729a6294000f5aa4a4bc0b73d1f32bc45510  # v3.3.0
        with:
          client-id: ${{ vars.RELEASE_APP_CLIENT_ID }}
          private-key: ${{ secrets.RELEASE_APP_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
```

### Exceptions

If a repository cannot immediately adopt a GitHub App token, declare an entry in `.standards.yaml` under `exceptions:`. The gate enforces category C (a fixed 10-day rectification interval):

```yaml
exceptions:
  - rule: "release-token"
    path: ".github/workflows/release.yml"
    category: "C"
    entered: "2026-10-18"
    expires: "2026-10-28"
    reason: "GitHub App creation underway; awaiting organization administrator approval"
```

The entry excuses every release-please step in the named workflow. A live entry passes and prints the workflow findings with its reason and expiry. An expired entry fails like a missing one. An entry naming a workflow with no findings is stale and fails until removed.

## What the gate reads

Every workflow document under `.github/workflows` is parsed and checked:

1. **Release-please step identification.** Any step whose `uses:` starts with `googleapis/release-please-action@` or `google-github-actions/release-please-action@` is evaluated.
2. **Token expression.** `with.token` must be exactly one `${{ steps.<id>.outputs.token }}` expression (whitespace inside braces allowed). It must not contain `||` fallbacks or surrounding text. Default tokens (`${{ github.token }}` or `${{ secrets.GITHUB_TOKEN }}`) are refused.
3. **App token step placement.** An earlier step in the **same job** must declare `id: <id>` and `uses:` starting with `actions/create-github-app-token@`. Referencing a step in another job or a later step in the same job is refused.
4. **Unconditional execution.** The app-token step must have no `if:` condition and no `continue-on-error:`. A skipped or forgiven token step yields an empty token, causing `release-please` to silently fall back to `github.token`.
5. **Secret and App identifiers.** The app-token step's `with.private-key` must be a single `${{ secrets.<NAME> }}` expression. The step must also set `client-id` or `app-id` to a non-empty value.
6. **Skip condition.** A repository with no `release-please` step skips the gate with a `[SKIP]` report.

## Reading the output

A failure names each workflow, job, step, and line where known:

```text
[FAIL] Release token: .github/workflows/release.yml:14: job release, step "release-please": takes the workflow token (github.token or secrets.GITHUB_TOKEN), which starts no checks on its release pull request.
[FAIL] Release token: no live exceptions entry declares the steps above; give each a token from an actions/create-github-app-token step (docs/guides/release-token.md), or, for a step that cannot yet, declare its workflow in the exceptions list of .standards.yaml (rule release-token, path .github/workflows/<workflow>, category C, a reason, and an expiry at most 10 days ahead).
```

A pass reports the evaluated steps:

```text
[PASS] Release token: 1 release-please step takes token from a GitHub App token step.
Read from the workflow files under .github/workflows; no release pull request was checked on the forge.
```

When no workflow invokes `release-please`, the gate skips:

```text
[SKIP] Release token not checked: no workflow under .github/workflows holds a release-please step.
```

## Release end-to-end test evidence gate (`release.e2e`)

Both `praetorctl audit` (`cmd/standardsctl/audit_release_e2e.go`) and the MCP tool `standards_audit` (`cmd/standards-mcp/audit_tools.go`) also run the release end-to-end evidence gate (`release.AuditE2E` and `release.Judge` in `internal/release/e2e_gate.go`, order `engine-release-feature-e2e-gate-20261027`).

### Configuration in `.standards.yaml`

Configure `release.e2e` in `.standards.yaml` (`internal/config/release_policy.go`):

```yaml
release:
  e2e:
    paths:
      - "e2e/**"
      - "tests/e2e/**"
      - "test/e2e/**"
      - "**/*_e2e_test.go"
      - "**/*.e2e.ts"
      - "**/*.e2e.js"
      - "playwright/**"
      - "cypress/**"
    tests:
      - "login-flow"
    since: "a1b2c3d4e5f60123456789abcdef0123456789ab"
```

- `paths`: repository-relative slash globs matching end-to-end test files. When omitted or empty, the built-in default list above applies (`config.DefaultReleaseE2EPaths`).
- `tests`: declared end-to-end test identifiers accepted in `E2E-Test: <id>` commit trailers.
- `since`: baseline commit SHA. Commits reachable from `since` are exempt from the check.

### How commits are judged

1. The gate resolves the most recent reachable tag matching `v[0-9]*` via `git describe --tags --abbrev=0 --match v[0-9]*`. When no such tag is reachable from `HEAD`, the gate prints `[SKIP] Release end-to-end evidence not checked: no release tag matching v[0-9]* is reachable from HEAD.`
2. Non-merge commits in `<tag>..HEAD` (excluding commits reachable from `release.e2e.since`) with Conventional Commit type `feat` or `fix` (with optional `(scope)` and `!`) are inspected. Other commit types (`chore`, `docs`, `refactor`, etc.) are ignored.
3. A `feat` or `fix` commit satisfies the gate when at least one changed path matches `release.e2e.paths` or its commit message carries an `E2E-Test: <id>` trailer whose `<id>` is listed in `release.e2e.tests`. An `E2E-Test: <id>` trailer naming an undeclared identifier produces a finding naming that identifier.
4. In release context (`praetorctl audit --release`, `PRAETOR_RELEASE_PR=true`, or `GITHUB_HEAD_REF` starting with `release-please--`), each finding prints `[FAIL] Release end-to-end evidence: <sha12> (<subject>): <reason>` and fails the audit. Outside release context, findings print `[WARN]` lines and the audit passes.
5. During `praetorctl adopt` (`internal/adopt/release_e2e.go`), repositories with a `release-please` configuration or a version-tag release workflow receive `release.e2e.since` set to `HEAD` at adoption time, and hosted gate templates expose `hostedGateReleasePREnv` (`templates/hostedgate.go`).
