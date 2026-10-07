# Workflow trigger audit

`praetorctl audit` and the MCP `standards_audit` read every workflow under `.github/workflows`
and report the triggers that start runs nobody needs: a push that runs on every branch, and a
pull request job that runs in full on a draft. This is the HISS-18 (CI efficiency) check on a
repository's own workflows. One adopter repository measured more than 1,000 workflow runs a day
from exactly these two shapes (#817).

The check is one function, `forge.AuditWorkflowTriggers` in
`internal/forge/workflow_trigger_audit.go`, and both audits call it
(`auditWorkflowTriggers` in `cmd/standardsctl/audit.go`, `auditGates` in
`cmd/standards-mcp/audit_tools.go`). Findings are warnings: the audit passes and lists them
([Why findings warn](#why-findings-warn)).

## What the audit reports

Each finding names the workflow file, the line of the trigger or job, and what starts the run:

| Finding | Why it costs runs | Test |
| :--- | :--- | :--- |
| `push` with no `branches` or `tags` filter, in any of its three shapes (`on: push`, `on: [push]`, a `push:` mapping) | Every push to any branch or tag starts the workflow, before a pull request exists. A `paths` filter alone does not narrow the branches. | `TestAuditWorkflowTriggers_Negative_ReportsEachWastedRun` |
| `push` with only a `branches-ignore` filter | Every branch the filter does not name starts it. | same |
| `push` with a `branches` pattern made of asterisks alone, such as `'**'` or `'*'` | `'**'` matches every branch name, and `'*'` every branch name without a slash. | same |
| A job a `pull_request` run starts whose first step is not the draft step | A draft runs the whole job, and runs it again on every push to the draft. | same |
| A job whose condition reads `github.event.pull_request.draft` | GitHub reports a job its condition skipped as successful, and a required check accepts that, so a draft marked ready can merge on the skip. | same |
| A job that stops a draft in its first step but sets `continue-on-error` | The failed draft reports success, which a required check accepts. | same |
| A job that needs a job held back on a draft, whatever its condition and its own first step | GitHub skips a job whose need failed or was skipped unless its condition runs it after one, and reports the skip as successful, which a required check accepts, so a draft passes the job without its work; its own draft step never runs. The check does not read the status functions in the condition ([Limits](#limits)). Each job of a chain is reported, naming the need that holds it back. | `TestAuditWorkflowTriggers_JobsSkippedThroughNeeds`, `TestAuditWorkflowTriggers_NeedSkipWhateverTheCondition`, `TestAuditWorkflowTriggers_NeedsChainAtTheJobBound` |
| Jobs whose `needs` form a cycle, or need a job on one | No order runs them, so the check cannot say what they do on a draft. One finding names them all, on the line of the first of them. | `TestAuditWorkflowTriggers_ReportsNeedsCycle` |
| A `pull_request` trigger whose jobs stop on a draft but that does not list `ready_for_review` in `types` | Marking the draft ready starts no run, so its failed check stands until the next push. | same |

`pull_request_target` and `issue_comment` triggers are not judged; the permission audit covers
them (`forge.AuditPullRequestPermissions`).

## What passes

The accepted shape is the one the hosted gates Praetor ships already use, defined once in
`internal/ghworkflow/hostedgate.go` ([When the workflow runs](api-compatibility.md#when-the-workflow-runs)):
`push` filtered to the default branch, `pull_request` on `opened`, `synchronize`, `reopened` and
`ready_for_review`, and a job whose first step, `Stop on a draft pull request`, fails a draft
with an error annotation. The check reads that step through `ghworkflow.DraftStepFault`, the
function the hosted gate check uses, so the audit and the emitter cannot disagree
(`TestAuditWorkflowTriggers_EngineWorkflowsPass`, `TestDraftStepFault`).

Each job that does work begins with the draft step itself rather than needing another job that
does. A job also passes when:

- it calls a reusable workflow of this repository (`uses: ./.github/workflows/<file>`) whose
  jobs, judged with their needs like the caller's, each begin with the draft step and need no job
  held back on a draft, or never start on a pull request, as long as at least one of them does
  start; or whose jobs all never start on a pull request, so nothing runs on a draft. A job that
  needs such an idle caller is judged on its own, because the check does not read how GitHub
  reports the caller (`TestAuditWorkflowTriggers_FollowsNeedsInsideLocalCallee`,
  `TestAuditWorkflowTriggers_CalleeNeedsChain`);
- a `pull_request` run never starts it, such as a deploy job guarded by
  `github.event_name != 'pull_request'`, or it has no condition of its own and needs such a job,
  which skips it on every pull request and not only on a draft.

A `push` filtered to named branches, such as `[main, 'release/**']`, or to tags alone passes.
`TestAuditWorkflowTriggers_Positive_HostedGateShapePasses` covers each of these.

The check orders a workflow's jobs by their needs (`util.DependencyOrder`, Kahn's algorithm) and
decides each job once, after every job it needs, from the job itself and the decisions of its
needs. The names of the jobs therefore never change a verdict
(`TestAuditWorkflowTriggers_VerdictIndependentOfJobNames` renames one workflow every way).

## Workflows Praetor writes and runs

Every workflow Praetor ships follows the shape, so a fresh adoption and Praetor's own audit report
no finding:

- **Flavor CI workflows.** The `ci.yml` that `praetorctl flavor apply` (and so `praetorctl adopt`)
  scaffolds for the Go, Rust, Node, JVM and Flutter flavors renders the shape from the same
  constants through the template functions `hostedGateOn`, `hostedGateDraftStep` and
  `hostedGateStepIf` (`templates/hostedgate.go`): it runs on a push to `main` and on the four
  pull request types, its first step is the draft step, and every later step carries
  `github.event.pull_request.draft != true`. Each rendering passes `ghworkflow.HostedGateFault`
  (`TestCIWorkflowTemplatesRenderTheHostedGateShape` in `templates/hostedgate_test.go`), and a
  fresh adoption of each flavor audits clean
  (`TestAdoptThenAuditReportsNoFlavorWorkflowTriggerFinding` in
  `cmd/standardsctl/audit_workflow_triggers_test.go`).
- **Updating an adopted copy.** Every text the CI templates rendered before is recorded
  (`internal/flavor/ci_prior.go`, fixtures in `internal/flavor/testdata/ci-prior`), so a plain
  `praetorctl adopt` or `praetorctl flavor apply` refreshes an unedited `ci.yml` without
  `--force` and an edited one stays until `--force`
  (`TestCIWorkflowApply_Positive_RefreshesAnEarlierText`,
  `TestCIWorkflowApply_Negative_KeepsAnEditedCopy` in `internal/flavor/ci_prior_test.go`).
- **Praetor's own workflows.** `ci.yml`, `compliance.yml`, `pages.yml`, `portability.yml` and
  `security.yml` run on `ready_for_review` and begin every job a pull request starts with the
  draft step; their schedule, dispatch and protected-branch push triggers stay. `ci.yml` also
  keeps `edited`, because its governance step reads the pull request body. A matrix step that
  runs after a failure (`!cancelled()`) carries the draft term too. Every context the committed
  ruleset requires keeps its name and fails a draft in its draft step
  (`TestEngineRequiredContextsFailOnADraft` in `internal/forge/workflow_draft_contexts_test.go`).

On a draft, each required check therefore fails with the annotation the checkpoint planner reads
as `draft_pending` ([checkpoint cadence](checkpoint-cadence.md)); marking the pull request ready
starts the run that replaces the failure on the same head commit.

## Why findings warn

HISS-18's failure action is a CI optimization gate (`internal/hisscatalog/catalog.go`), the one
HISS rule whose failure rejects nothing; the [HISS specification](../standards/hiss-spec.md#hiss-18-diff-aware-ci-efficiency)
describes it as a filter that, when in doubt, runs more rather than blocks. A wasted run costs
minutes, not correctness, so the check prints a `[WARN]` line per finding and the audit passes.

Failing would also block every commit of an adopted repository whose own workflows predate the
shape, or whose `ci.yml` is an edited copy of an earlier flavor rendering that adoption does not
refresh, because the pre-commit hook runs `praetorctl audit --offline`. Such a `ci.yml` is still
reported (`TestAdoptThenAuditReportsNoFlavorWorkflowTriggerFinding` in
`cmd/standardsctl/audit_workflow_triggers_test.go`).

The check does fail when it cannot run: a workflow it cannot parse, more workflows than it reads
(64), or a malformed `HISS-18` exceptions entry
(`TestAuditWorkflowTriggers_SkipsWithoutWorkflowsAndFailsUnread`). A repository without
`.github/workflows` is skipped with that reason stated.

## Declaring a workflow that must run everywhere

A workflow that must run on every branch push or on drafts, such as a branch-preview deployment,
is declared in the top-level `exceptions` list of `.standards.yaml` with rule `HISS-18`:

```yaml
exceptions:
  - rule: "HISS-18"
    path: ".github/workflows/preview.yml"
    reason: "deploys a preview of every branch for review"
    expires: "2026-12-31"
```

The entry names one workflow file directly in `.github/workflows` by `path`; a glob is refused
(`TestValidateExceptionsWorkflowTarget` in `internal/config/exceptions_test.go`). The other keys
follow the [exceptions list rules](clang-tidy-coverage.md#exceptions): one line of reason and an
expiry at most 90 days ahead.

While the entry holds, through its expiry day, the audit prints the workflow's findings under a
`[PASS]` line with the reason and expiry instead of reporting them. From the next day they are
reported again, led by a line naming the expired entry. An entry naming a workflow without
findings is reported as stale until it is removed (`TestAuditWorkflowTriggers_Exceptions`).

## Reading the output

A repository whose workflows all pass gets one line:

```text
[PASS] Workflow triggers (HISS-18): workflows read: 14; no push trigger runs on every branch and no job a pull request starts runs its work on a draft.
```

A finding is one line, and the last line counts them and names both ways out:

```text
[WARN] Workflow triggers (HISS-18): .github/workflows/ci.yml:24: pull_request job verify runs on a draft pull request: its first step is not the draft step "Stop on a draft pull request".
[WARN] Workflow triggers (HISS-18): 1 of 14 workflows start runs this check counts as wasted (findings: 1); reported, not enforced, because HISS-18's failure action is a CI optimization gate. ...
```

## Limits

- A reusable workflow of another repository cannot be read, so a job calling one is reported.
- The audit reads three things from a job condition: whether a `pull_request` run can start the
  job at all, whether it reads `github.event.pull_request.draft`, and whether the job has a
  condition. It does not read the status functions `always()`, `failure()`, `success()` and
  `cancelled()`, so a job that needs a job held back on a draft is reported whatever its
  condition says. A condition can mention `always()`, `failure()` or `cancelled()` and still skip
  the job after a failed need, as `!failure() && !cancelled()` does, so the audit reports by
  default instead of guessing. That includes the aggregate merge gate that begins with the draft
  step and runs with `if: always()`, which does fail a draft through its failed need. A later
  change may pass that proven `always()` aggregate shape once the aggregate prover of #821 is on
  main; until then, declare such a workflow in the exceptions list.
- The audit does not read which jobs the ruleset requires, so every job of a need chain is
  reported, not only the required one.
- A job with a condition of its own that needs a job a `pull_request` run never starts is judged
  alone, as if it had no needs: `always()` runs it on every pull request.
- A reusable workflow that a reusable workflow of this repository calls in turn is not read, so
  the job calling the first one is reported. So is a job calling a reusable workflow of this
  repository whose jobs' needs form a cycle.
- Steps after the draft step are not read: a later step whose condition calls `always()` still
  runs on a draft. Praetor's own required checks are held to that by a test instead
  (`TestEngineRequiredContextsFailOnADraft`).
- The run budget part of #817 (runs and run-minutes per workflow from the forge) and the check
  for a workflow that re-runs on every base-branch push are not implemented.
