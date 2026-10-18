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
[FAIL] Release token: .github/workflows/release.yml:14: job release, step "release-please": token is ${{ github.token }} or ${{ secrets.GITHUB_TOKEN }}.
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
