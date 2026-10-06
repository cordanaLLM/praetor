# Live Actions checks

`praetorctl audit` judges a repository on its files, and three GitHub facts are not in any file:
the repository's workflow permissions setting, whether its workflows actually run and pass, and
the branch protection GitHub enforces on the default branch. When the audit can reach the forge,
it reads all three and compares them with what the manifest declares. Nothing is written:
`praetorctl sync --remote` reconciles the branch protection
([Branch protection](#branch-protection)) and neither Actions setting yet.

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

The last lines of the audit report the three checks:

```text
[PASS] Actions workflow permissions compared with the forge: live workflow permissions match the declaration.
[PASS] Workflow runs on main read from the forge: 12 workflows checked, none failing or never run (adopt.yml, ci.yml, ...).
[PASS] Live branch protection of main compared with the forge: GitHub enforces every declared property (protected by ruleset "praetor-main-protection" #7).
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
(`TestAuditLiveActions_Boundary_ChecksNotMadeAreNamed`,
`TestAuditLiveBranchProtection_Boundary_NotComparedIsNamed`). This repository's CI runs the
audit without a token (`.github/workflows/ci.yml`, `.github/workflows/compliance.yml`), so there
every check prints `[SKIP]`.

`praetorctl plan` previews the permission comparison under the same conditions and never fails on
it, as it never fails on file drift. `plan --offline` skips it.

### In Git hooks and CI

The `lefthook.yml` adoption writes runs `audit --offline` before a commit and `audit` before a
push ([Local Git hooks](git-hooks.md#offline-before-a-commit-online-before-a-push)):

- A commit never asks the forge, so it never waits on the network and never fails on a forge
  setting. The fallback pre-commit hook, written when lefthook cannot install, is offline too.
- A push asks the forge under the conditions above, since it needs the network anyway. A drifted
  permission verdict or drifted branch protection refuses the push until the setting the verdict
  names is fixed.
- `make verify-all` and CI run the online audit. Whether they reach the forge depends on the token
  they provide.

One online audit makes up to 130 requests for the Actions checks: two for the workflow
permissions, then one or two for each workflow, at most 64. The branch protection comparison adds
three, and one more page for each further 100 active rules or rulesets. Each request is bounded at
15 seconds and all of them at three minutes. Without `GITHUB_TOKEN` or `GH_TOKEN` it also runs
`gh auth token` once. A forge that does not answer costs about 45 seconds, one timed-out request
for each check, and every check prints `[SKIP]`; an unreachable forge never refuses a push. A `lefthook.yml` an earlier release
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

## Branch protection

The committed ruleset gate compares `.github/rulesets/main.json` with the ruleset the declared
policy renders ([Branch protection rulesets](adoption-verification.md#branch-protection-rulesets-and-adoption-decline)).
A file says nothing about what GitHub enforces: a ruleset that was never applied, one left in
evaluate mode, or legacy branch protection that requires other checks all passed that gate
(#159). So the audit also reads the branch protection of the default branch
(`forge.RepositoryDefaultBranch`) and compares it with the declared policy
(`auditLiveBranchProtection` in `cmd/standardsctl/audit_forge.go`). The read and the comparison
are the ones `praetorctl plan --remote` prints (`compareLiveProtection` in
`cmd/standardsctl/protection_report.go`):

- Both mechanisms are read: the active rules of every ruleset that targets the branch, the
  repository's and its organisation's, and the legacy protection object
  (`GitHubDriver.ReadBranchProtection` in `internal/forge/branch_protection_live.go`). GitHub
  enforces their union, so a branch protected by legacy protection alone is compared on what that
  object requires.
- Each declared property is compared (`EvaluateBranchProtection` in
  `internal/forge/branch_protection_eval.go`): pull requests, the approving review count,
  code-owner review, stale review dismissal, signed commits, linear history, deletion and force
  pushes blocked, and the required status checks: the jobs that report on every pull request in
  that repository, read from the workflows of `origin/<default branch>` as the checkout last
  fetched it (`forge.RequiredStatusContextsAt`, `auditProtectionTarget`).
- When the repository has the ruleset praetor writes, `praetor-main-protection`, its enforcement
  must be `active`. GitHub applies no rule of a ruleset in `evaluate` or `disabled` mode and
  leaves its rules out of the branch's rule list, so the audit reads the mode from the ruleset
  listing.

A property the branch does not enforce fails the audit, and the failure names each one with its
declared and live value. A branch whose legacy protection requires other checks than the
declared one, and no signed commits, reads:

```text
[FAIL] Live branch protection of main on GitHub does not match the declared policy (protected by branch protection):
  Signed commits: declared required, live not enforced
  Required status checks: declared 1, live 0 of 1 required; missing: CI Gate
To reconcile it, run 'praetorctl plan --remote' and then 'praetorctl sync --remote' from an up-to-date checkout of main, not from another branch:
  both require the status checks of the workflows of the checkout they run in, and sync keeps every check it finds required, so a sync from another branch requires its jobs that main does not run, and they block every pull request until removed by hand.
  sync --remote also writes the labels in .config/labels.yaml and the repository description, homepage and topics; plan --remote previews only the branch protection, so review those first.
  Both read the token from --token, GITHUB_TOKEN or GH_TOKEN, never from the gh session.
```

The remedy names the default branch because `sync --remote` merges the checks it requires into
the live ruleset and never drops one (`unionStatusChecks` in `internal/forge/ruleset_merge.go`):
a check only a feature branch runs, once required, blocks every pull request that lacks it.

A live setting stricter than declared passes; `plan --remote` lists it as `[STRICTER]`
(`TestAuditLiveBranchProtection_Positive_MatchingProtectionPasses`,
`TestAuditLiveBranchProtection_Negative_DriftFailsNamingEachProperty`).

Every live check runs whatever an earlier one found: drifted workflow permissions and drifted
branch protection fail one audit together, each with its own `[FAIL]`
(`TestAuditLiveForge_Negative_EveryDriftIsReported`). A comparison that cannot be evaluated,
such as a negative approving review count or more required checks than a ruleset holds, is a
local defect and fails the audit too; only a read the forge did not answer is reported as not made
(`TestAuditLiveBranchProtection_Negative_UnevaluableComparisonFails`).

Nothing is compared, and the audit says why, in four cases:

| Case | Line |
| :--- | :--- |
| `adoption.decline` lists `branch-ruleset` | `[INFO] ... not compared with the forge: branch-ruleset declined by adoption.decline.` The committed ruleset gate keeps its own decline line. |
| The policy requires neither linear history nor signed commits | `[INFO]`; the policy declares no ruleset (`adopt.RulesetRequired`, `TestAuditLiveBranchProtection_Boundary_PolicyWithoutRulesetIsNotCompared`). |
| `--offline`, no token, no matching `origin`, or a read the forge refused | `[SKIP]` with the reason. Reading the legacy protection object needs read access to the repository's Administration permission, which a workflow's `GITHUB_TOKEN` cannot be granted. |
| The default branch does not exist on GitHub yet | `[SKIP] Live branch protection of main not compared with the forge: main does not exist on GitHub yet, so nothing is enforced on it.` The first push of the branch is not refused; the audit compares it once it is pushed (`TestAuditLiveBranchProtection_Boundary_UnpushedDefaultBranchIsNotCompared`). |

The status checks come from the default branch, not from the checkout under audit. GitHub can
require a job only once it runs on the default branch, and requiring it earlier blocks every open
pull request that lacks the job. So a job a branch adds is listed and not compared:

```text
[INFO] Live branch protection of main: status checks compared with the ones origin/main (1a2b3c4d) reports.
[INFO] Live branch protection of main: not compared yet, as origin/main does not report them: Lint. Once they are on main, 'praetorctl sync --remote' requires them.
```

A job on the default branch that the live protection does not require fails the audit on every
branch, a branch cut before the job landed included, until `praetorctl sync --remote` requires it.
Run that sync from an up-to-date default branch once the job has landed: `sync --remote` and
`plan --remote` use the workflows of the checkout they run in, because they write and preview what
that checkout declares. A checkout without `origin/<default branch>`, such as a single-branch
clone of another branch, compares the live protection with its own workflows and says so
(`TestAuditLiveBranchProtection_Positive_CheckAddedOffTheDefaultBranchIsNotCompared`,
`TestAuditLiveBranchProtection_Negative_DefaultBranchCheckNotRequiredDrifts`,
`TestAuditLiveBranchProtection_Boundary_DefaultBranchNotFetched`).

The workflows of `origin/<default branch>` are read from the checkout's object store, and nothing
is fetched. A blobless partial clone (`git clone --filter=blob:none`) may lack a workflow that
changed there since the checkout's own commit. The audit then compares with the checkout's own
workflows too, and names the workflow it could not read
(`forge.ErrCommittedWorkflowAbsent`,
`TestAuditLiveBranchProtection_Boundary_AbsentDefaultBranchWorkflowIsNamed`):

```text
[INFO] Live branch protection of main: the workflows of origin/main (1a2b3c4d) are not all in this checkout, as in a partial clone (workflow api.yml at commit 1a2b3c4d...: its object is not in this checkout, and nothing is fetched to read it), so the status checks are compared with the ones this checkout's workflows report.
```

`praetorctl plan` reads local files only unless `--remote` is passed. Its status then ends with
`[INFO] Live branch protection not compared with the forge: this plan read local files only.`
instead of saying that no change is required, and the MCP `standards_plan` tool prints the same
line, from the same function (`adopt.FormatPlanStatus` in `internal/adopt/plan_drift.go`,
`TestFormatPlanStatus_LiveNotCompared`).

## Limits

- Only GitHub is read. The GitLab and Gitea drivers return `ErrNotImplemented` for both Actions
  reads and have no branch protection reader.
- Neither Actions setting is written. Reconciling the permissions through `sync --remote`, and
  letting archetypes and facets declare them, are separate follow-ups.
- The run check reads the default branch. A workflow that only runs off it, such as a release on
  tag push, is listed with its newest run but its failures are not counted.
- The audit bounds all forge reads to three minutes and each request to 15 seconds.
