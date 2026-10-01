# Live Actions checks

`praetorctl audit` judges a repository on its files, and two GitHub Actions facts are not in any
file: the repository's workflow permissions setting, and whether its workflows actually run and
pass. When the audit can reach the forge, it reads both and compares them with what the manifest
declares. Nothing is written: `praetorctl sync --remote` does not reconcile either setting yet.

## Quick start

Declare the workflow permissions the repository needs in `.standards.yaml`:

```yaml
overrides:
  actions:
    default_workflow_permissions: read
    allow_create_and_approve_pull_requests: false
```

Then run the audit in a checkout whose `origin` remote names that repository on github.com, with a
token available:

```bash
praetorctl audit
```

The last lines of the audit report the two checks:

```text
[PASS] Actions workflow permissions compared with the forge: live workflow permissions match the declaration.
[PASS] Workflow runs on main read from the forge: 12 workflows checked, none failing or never run (adopt.yml, ci.yml, ...).
```

## When the audit asks the forge

The audit asks only when every condition holds (`openActionsForge` in
`cmd/standardsctl/audit_forge.go`):

- `--offline` was not passed.
- The manifest sets `repository.owner` and `repository.name`.
- The `origin` remote names that repository on github.com, the check `sync --remote` uses, so a
  report never describes another repository's settings.
- A token is found: `GITHUB_TOKEN`, then `GH_TOKEN`, then the `gh` CLI session.

Otherwise each check prints `[SKIP]` with the reason, and the audit goes on. A check the forge
refused (401, 403 or 404, a missing scope, a rate limit) or could not answer is reported the same
way. A check that was not made is never printed as a pass
(`TestAuditLiveActions_Boundary_ChecksNotMadeAreNamed`). This repository's CI runs the audit
without a token (`.github/workflows/ci.yml`, `.github/workflows/compliance.yml`), so there both
checks print `[SKIP]`.

`praetorctl plan` previews the permission comparison under the same conditions and never fails on
it, as it never fails on file drift. `plan --offline` skips it.

### In Git hooks and CI

The `lefthook.yml` adoption writes runs `audit --offline` before a commit and `audit` before a
push ([Local Git hooks](git-hooks.md#offline-before-a-commit-online-before-a-push)):

- A commit never asks the forge, so it never waits on the network and never fails on a forge
  setting. The fallback pre-commit hook, written when lefthook cannot install, is offline too.
- A push asks the forge under the conditions above, since it needs the network anyway. A drifted
  permission verdict refuses the push until the setting the verdict names is fixed.
- `make verify-all` and CI run the online audit. Whether they reach the forge depends on the token
  they provide.

One online audit makes up to 130 requests: two for the workflow permissions, then one or two for
each workflow, at most 64. Each request is bounded at 15 seconds and all of them at three
minutes. Without `GITHUB_TOKEN` or `GH_TOKEN` it also runs `gh auth token` once. A forge that
does not answer costs about 30 seconds, one timed-out request for each check, and both checks
print `[SKIP]`; an unreachable forge never refuses a push. A `lefthook.yml` an earlier release
wrote, whose pre-commit audit still read the forge, is migrated to the offline one on a plain
`praetorctl adopt`, without `--force`.

## Workflow permissions

With `overrides.actions` declared, the audit reads
`GET /repos/{owner}/{repo}/actions/permissions/workflow` and the organisation's
`GET /orgs/{owner}/actions/permissions/workflow` (`GitHubDriver.WorkflowPermissions` in
`internal/forge/actions_state.go`). Reading the repository value needs a token that can read its
administration settings; the organisation value is read when the token allows it.

The comparison (`EvaluateLiveActionsPermissions` in `internal/forge/actions_permissions.go`) has
one passing verdict and four failing ones. Each failing verdict fails the audit:

| Verdict | Meaning | Where to fix it |
| :--- | :--- | :--- |
| `compliant` | The live value matches the declaration. | Nothing to fix. |
| `drifted-by-organisation` | The repository differs from its declaration and matches its organisation, so it inherits the organisation value. | Pin the declared value on the repository. |
| `drifted-at-repository` | The repository differs from both its declaration and its organisation. | The repository setting. |
| `blocked-by-organisation` | The repository declares `allow_create_and_approve_pull_requests: true` and the organisation denies it. | The organisation setting; the repository cannot raise it. |
| `drifted` | The repository differs from its declaration and the organisation value was not read: the owner is a user account, or the token cannot read the organisation. | The source is not named. |

A manifest without `overrides.actions` is not failed, and the audit does not ask the forge
about it. A declared block must set `default_workflow_permissions` to `read` or `write`; any other
value is a manifest error (`internal/config/actions_policy.go`,
`TestActionsPolicy_Negative_MalformedDeclarationsFail`).

## Workflow runs

For each workflow under `.github/workflows`, the audit reads its latest 20 completed runs on the
default branch (`forge.RepositoryDefaultBranch`), and, when there is none, whether it has run on
any branch at all (`GitHubDriver.WorkflowRunHistory`, `AuditWorkflowRunHealth` in
`internal/forge/workflow_run_health.go`). Pull request runs are skipped, and a cancelled, skipped
or neutral run neither extends nor ends a failure streak.

| Finding | Line |
| :--- | :--- |
| Latest decisive run succeeded | Listed in the summary only. |
| Latest decisive run failed | `[WARN]` with the number of failed runs in a row and the latest failed run; `at least 20` when the forge lists a full window of 20 and no success ends the streak inside it. A shorter history is the whole one, so its streak is exact (`TestAuditWorkflowRunHealth_Boundary_ShortFailureHistoryIsExact`). |
| No run on any branch | `[WARN]` naming the triggers from the workflow's `on:` key. |
| No completed run on the default branch, runs elsewhere | `[INFO]` with the newest run, such as a tag-triggered release. |
| Only cancelled, skipped or neutral runs in the window | `[INFO]` with the newest completed run. |
| Only `workflow_call` triggers | `[INFO]`; not read, since it runs inside its callers' runs. |
| Unknown to the forge | `[INFO]`; the file is not on the default branch yet. |

These findings are reported and never fail the audit: the summary line ends with `reported, not
enforced` (`TestAuditLiveActions_WorkflowRunsAreReportedNotEnforced`). The first read the forge
refuses ends the run check with `[SKIP]`, so a report never looks complete over workflows it
could not read.

### Declaring a known state

A workflow that fails on purpose, or has not had a reason to run yet, is declared with a reason so
it stays visible without being counted:

```yaml
workflow_runs:
  expected:
    - workflow: nightly-build.yml
      state: failing
      reason: the compile step refuses until its toolchain is packaged
    - workflow: release.yml
      state: unexercised
      reason: no release tag has been pushed yet
```

A declaration covers only the state it names. A workflow declared `unexercised` that runs and
fails, or one declared `failing` that has never run, is still warned about. A declared workflow
that is healthy again is told the declaration no longer applies, and a declaration naming a file
that is not under `.github/workflows` is warned about
(`TestAuditWorkflowRunHealth_Boundary_DeclarationsCoverOnlyTheirState`).

`workflow` is a bare `.yml` or `.yaml` file name, `state` is `failing` or `unexercised`, and
`reason` is one nonempty line of at most 500 bytes; at most 64 workflows can be declared, and
each only once (`TestActionsPolicy_Boundary_BoundsAndAbsence`). The section is repository-only:
no profile, facet or fleet file can set it.

## Limits

- Only GitHub is read. The GitLab and Gitea drivers return `ErrNotImplemented` for both reads.
- Neither setting is written. Reconciling the permissions through `sync --remote`, and letting
  archetypes and facets declare them, are separate follow-ups.
- The run check reads the default branch. A workflow that only runs off it, such as a release on
  tag push, is listed with its newest run but its failures are not counted.
- The audit bounds all forge reads to three minutes and each request to 15 seconds.
