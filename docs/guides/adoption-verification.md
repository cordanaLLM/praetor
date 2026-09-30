# Adoption verification commands

Adoption prepares governance and declares project commands. It does not execute a
project's scripts or certify that the application builds or passes tests. The
optional `verification` object in the adoption report carries the selected argv,
runtimes, status, and any reasons requiring review.

| Status | Meaning |
| --- | --- |
| `declared-unverified` | Commands follow discovered project metadata. Execution and tool availability still need verification. |
| `unavailable` | A required build/test command is missing or ambiguous. Generated recipes fail explicitly. |
| `preserved-unverified` | Existing custom Makefile ownership is preserved. Review and exercise its `verify-all` contract. |

An `unavailable` plan's `verify-all` runs the governance checks and then fails with one
`printf`/`exit 1` pair, also when adoption appends the rule to an existing Makefile. The
report's `Project verification unavailable` warning says so, names the missing build or
test command, and lists every command discovery found that `verify-all` does not run,
shell-quoted, so you can wire them into the project's own contract. When the existing
Makefile already defines a `test` target, a second warning names it as the likely test
contract. A `test` target whose recipe is that failing pair is adoption's own, such as the
one in a Makefile adoption generated earlier, so a re-run does not name it. A Makefile
adoption does not recognise as its own current output, but that still holds the failing
pair, keeps the plan `unavailable` on the next run rather than being preserved as a custom
`verify-all`. Adoption recognises its earlier and current Makefile output in either
consistent line-ending style, so a CRLF checkout (`core.autocrlf` on Windows) gets the same
plan and report as an LF one, and earlier output it replaces keeps its CRLF endings; a
Makefile mixing both styles counts as edited. The tests are in
`internal/adopt/verification_pillar_test.go`:
`TestAdoptPythonProjectWithoutBuildWarnsVerificationGate`,
`TestNoteExistingTestTargetNamesOnlyTheAdoptersTarget`,
`TestAdoptRerunOnGeneratedMakefileDoesNotNameItsTestTarget` and
`TestPreservedVerifyAllWithPlaceholderStaysUnavailable`, with the CRLF migration in
`TestVerificationLegacyMigrationAndCustomPreservation`
(`internal/adopt/verification_scaffold_test.go`). A plan's `selected_by` list names the
configuration that selected a command where the marker alone does not, such as the
pytest configuration file and table, and the warning repeats it.

Ownership means a rule. Make reads a line in two steps, and adoption follows both. First it cuts
the line at the first unescaped `#`, which opens a comment, or `;`, which opens the inline recipe.
Then it decides rule versus assignment on what is left, by whichever operator it reaches first: an
assignment operator or a colon.

A Makefile that only binds a variable of the same name declares no target, whether the operator is
`:=`, `::=`, `:::=`, `=`, `?=`, `+=` or `!=`, whether the line carries an `export` or `override`
modifier, and whether or not the value itself contains a colon (`verify-all = docker run --rm
ci:latest check`, `verify-all = $(SRCS:.c=.o)`). The same test then runs over the prerequisites,
which is why `verify-all: CFLAGS := -g` binds a target-specific variable without declaring a
recipe -- with or without a trailing comment. A line whose first word is `export` or `unexport`
is that directive and never a rule, so `export verify-all: dep` declares no target either; only
the first word decides, which keeps `override verify-all: dep` and `private verify-all: dep`
rules. In all of these adoption appends its own `verify-all` rule rather than preserving one and
reporting a command the project's `make` answers with `No rule to make target 'verify-all'`.

Because the cut comes first, an `=` that a comment or a recipe carries decides nothing:
`verify-all: lint ## run gates (FAST=1)` and `verify-all: ; FOO=1 echo c` are rules and are
preserved. So are double-colon rules (`verify-all:: dep`), target lists (`all verify-all: dep`) and
rules whose prerequisites hold a substitution reference (`verify-all: $(SRCS:.c=.o)`) or a function
call with a nested reference (`verify-all: $(filter-out $(X),a=b)`): like Make, the reader counts
nested brackets, so a colon or `=` anywhere inside a reference is not an operator. Also preserved
are the forms only Make itself can resolve: an `include` directive, `$(eval ...)` or `${eval ...}`, a
target name containing `$` or `%`, a line longer than the 8192-byte scan bound, which is read in
part, and a Makefile longer than 4096 lines, which is read only in part as well; both are left to
Make. A `define` alone only binds a variable: a rule line inside its body declares nothing, and a
helper used only through `$(call ...)` in recipes leaves the Makefile readable. A define is left to
Make when something can parse its body as rules, which is an eval call anywhere in the file,
define bodies included, or a top-level bare expansion such as `$(name)`, `$(call name)` or
`$(call name,$(P)x,A=b)`. A bare expansion needs no define to declare a rule, so one with a colon
in its arguments, such as `$(if $(X),docs-lint: ; @echo x)`, is left to Make as well. That
includes a colon inside a message argument, such as `$(call check_defined,CC,hint: set it)`, which
Make only prints: telling the two apart needs the function evaluated, so the reader stays on the
safe side and the file fails for review, as the
[documentation governance guide](documentation-governance.md) describes. So does a computed
target name such as `$(BUILD_DIR):`, even when the variable holds a plain directory. The reader
evaluates no function except to know that `info`, `warning` and `error` expand to nothing, so
`$(info ...)` beside a define and a colon inside `$(error ...)` decide nothing, while any other
bare expansion beside a define counts, `$(if ...)` included. A define that is never closed is
left to Make too, since it would swallow an appended block. The
documentation gate uses the same reader to decide whether the project already owns `docs-lint`
(`mayDefineTarget` in `internal/adopt/verification_makefile.go`, define tracking in
`internal/adopt/verification_makefile_define.go`). The table tests behind this contract are in
`internal/adopt/verification_makefile_target_test.go` and
`internal/adopt/verification_makefile_define_test.go`; each row was measured against GNU Make 4.4.1.

The report's `steps` array records each step of the adoption chain it reached
(`completed`, `declined` or `failed`) with the warnings and errors that step
recorded. A step that records an error and carries on is `failed`, like one
that stops the chain; `--force` on an `AGENTS.md` whose harness has no
boundary line is one such case (`internal/adopt/harness.go`). The
**Governance Pillars** lines that `praetorctl adopt` and the
`standards_adopt` MCP tool print are derived from it: a pillar is `✓` only
when its step completed without warnings or errors, and otherwise names itself
`planned` (dry run), `warned`, `declined`, `failed` or `not-run`. A run
that recorded an error is incomplete even when it was a dry run
(`Outcome` and `Pillars` in `internal/adopt/report.go`, tested in
`internal/adopt/report_test.go`). The **Verification Gate** also follows
the verification plan: when the plan is `unavailable`, the `verify-all`
adoption writes can only exit 1, so the pillar is `warned`, never `✓` or
`planned`, and carries the plan's warning. An applied run whose Verification
Gate is warned, failed or unreached ends with `not ready yet: Verification
Gate` instead of the success line. Only the Verification Gate counts: a
warning on another pillar is informational and stays on that pillar's line,
such as the notice that an existing `.devcontainer` was preserved, which every
plain re-run repeats, and a declined pillar does not count either
(`PendingPillars` in `internal/adopt/report.go`,
`TestVerificationPillarFollowsThePlan` in
`internal/adopt/verification_pillar_test.go`,
`TestPrintAdoptReportQualifiesSuccessWithPendingPillars` in
`cmd/standardsctl/adopt_test.go`, and end to end
`TestAdoptExistingDevContainerKeepsTheSuccessLine` in
`cmd/standardsctl/adopt_success_line_test.go`).

Repositories declaring `docs:seo-portal` also receive a locked Markdown gate,
its dedicated required CI workflow, and private scratch-link protection. The
[documentation governance guide](documentation-governance.md) describes its
inventory, diagnostics, adoption behavior, and bounds.

## README governance is adoption evidence, not certification

When `README.md` exists, adoption owns only the region between
`<!-- praetor:readme-governance:start -->` and
`<!-- praetor:readme-governance:end -->`. It refreshes that region from the
recorded debt baseline and preserves content outside it. The badge says
**HISS Adopted**, never **HISS Compliant**: a baseline records existing scanner
debt, while only a commit-bound signed Exit-0 receipt proves that a particular
verification run passed.

The block never links a repository-relative path: a documentation portal that
includes the README resolves a relative link against its own pages, and
`mkdocs build --strict` aborts on a target it cannot find (#506). The
**HISS Adopted** badge links `AGENTS.md` on the default branch of the repository
that `repository.owner` and `repository.name` name, by that forge's absolute URL,
only when the forge is known:

| Origin host | Link |
| :--- | :--- |
| `github.com` | `https://github.com/<owner>/<name>/blob/HEAD/AGENTS.md` |
| `gitlab.com` | `https://gitlab.com/<owner>/<name>/-/blob/HEAD/AGENTS.md` |
| `gitea.com`, `codeberg.org` | `https://<host>/<owner>/<name>/src/branch/HEAD/AGENTS.md` |

Each forge resolves `HEAD` to the default branch, so the link needs no branch
name. The manifest fields do not say which forge hosts the repository, so
adoption takes the host from the `origin` remote, and only when that remote's
path is exactly `<owner>/<name>`. Any other host, including a self-hosted
instance whose forge software the host name does not reveal, a nested GitLab
namespace, and a repository without an `origin` remote, gets an unlinked image
(`TestAdoptReadmeGovernanceLinksTheOriginForge` in
`internal/adopt/readme_governance_test.go`). The block records the host in the
link itself. `praetorctl audit` reads it back rather than asking the clone's
remote, so a mirror verifies the same block; it accepts only the exact link a
known forge serves for the manifest's repository, and reports a link into
another repository or onto another host as stale.

With the `docs:seo-portal` facet the block also carries a workflow badge that
links the `praetor-docs.yml` workflow runs on GitHub, where that workflow runs.
`TestRenderedBlockBuildsInStrictMkDocsPortal` in
`internal/readmegovernance/links_test.go` builds a strict MkDocs portal from
the linked and unlinked renderings when `mkdocs` is on `PATH`, and the
link-shape tests beside it hold the same contract on hosts without it. A block
an earlier Praetor wrote with the relative `AGENTS.md` link fails audit as
stale; plain `praetorctl adopt` re-renders the whole marker region, so it needs
no `--force` (`TestAdoptReadmeGovernanceRefreshesRelativeAgentsLink`). With the
facet and a manifest naming no identity, or only half of one, because adoption
could not resolve it, adoption leaves the README unchanged and records a
`Governance block not reconciled` warning instead of linking to a guessed
repository (`TestAdopt_UnresolvedIdentityCompletesWithoutGuessing`,
`TestReadmeIdentity`).
Set both fields in `.standards.yaml` and re-run `praetorctl adopt` to reconcile
the block; adoption never rewrites an existing manifest, so adding an `origin`
remote alone does not fill them (`TestAdopt_RerunCompletesOnceIdentityIsSet`).

An operational fork carries the engine's README, whose block links the public
source. `praetorctl operational sync plan` and `prepare` read the recorded state,
the forge host included, back from that block and render it again for the fork's
`repository.owner` and `repository.name` with the same renderer, so the fork
passes its own audit
([README governance block](operational-sync.md#readme-governance-block),
`internal/operationalsync/readme_test.go`).

Every line of the block passes markdownlint's MD013 at its default 80 columns,
strict mode and tables included, whatever the repository identity, so an
adopter's own line-length lint needs no disable comment around it. The badges
are reference-style images whose URLs sit in link reference definitions at the
end of the block, which MD013 always exempts, and the prose is wrapped. Each
gate is a paragraph rather than a list item: MD004 takes a document's list
marker from its first list, and a list near the top of the README would impose
its marker on every list below (`TestRenderedBlockPassesMD013Positive` and its
negative and boundary cases in `internal/readmegovernance`, the boundary at
GitHub's 39-character owner and 100-character repository limits).

`praetorctl audit` renders the same expected block in memory and fails when the
README block is missing, malformed, duplicated, or stale. Re-run
`praetorctl adopt` to migrate the historical unmarked HISS-16 badge and
governance table. A repository that deliberately keeps README ownership can set
`adoption.decline: [readme]` in `.standards.yaml`; audit reports that explicit
decision instead of silently treating an unchecked README as current.

Adoption refuses unbalanced or duplicate live markers before writing
`README.md`. Marker examples inside fenced code are inert. Human-authored and
custom badges outside the block remain untouched, and a custom HISS badge
prevents the renderer from adding a second badge.

### Branch protection rulesets and adoption decline

Repositories adopting Praetor that manage branch protection externally or decline
generated GitHub rulesets can record `adoption.decline: [branch-ruleset]` in
`.standards.yaml` (accepted by `standardsctl adopt`). When this decline is recorded,
`standardsctl adopt` omits `.github/rulesets/main.json`.

Both `standardsctl audit` and the MCP server's `standards_audit` tool share one
authority path (`adopt.AuditBranchProtectionWithPolicy`) to verify branch protection,
against the effective policy the audit resolved: profiles, facets and overrides joined, the
policy adopt and sync render the ruleset from. Built-in defaults plus overrides are not a
stand-in; every shipped archetype requires signed commits, which the defaults do not
(`TestAuditBranchRuleset_CLI_Positive_ProfileContributesBranchProtection`,
`TestServerAuditBranchRuleset_MCP_Positive_ProfileContributesBranchProtection`):

- If `branch-ruleset` is explicitly and validly declined in `adoption.decline`,
  audit reports `[PASS] Branch protection ruleset declined by adoption.decline.`
  and passes cleanly without requiring `.github/rulesets/main.json`.
- If `branch-ruleset` is not declined and active policy requires branch protection
  (such as linear history or signed commits), audit fails closed if
  `.github/rulesets/main.json` is missing, or if its content differs from the ruleset
  the declared policy renders for the status checks the repository's workflows report.
  That comparison is `forge.ValidateRepositoryRuleset`, the same check
  `praetorctl sync` runs; key order and whitespace do not count, so `{}`, zero
  required approvals or a missing `required_signatures` rule fail where the policy
  asks for them (`internal/adopt/ruleset_audit_test.go`).
- An unknown decline item, malformed decline entry, or unreadable `.standards.yaml`
  fails closed, ensuring invalid configuration cannot produce a false pass.

### Agent definitions and adoption decline

`praetorctl audit` counts the agent personas `compile-context` projects: one file per agent,
`.agents/agents/<name>.md`. The gate reads them through `compiler.ReadAgentInventory`
([`internal/compiler/agent_projection.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/compiler/agent_projection.go)),
the walk `compile-context` and its `--verify` project from, so the count and the projected set
cannot differ. A pass prints `[PASS] Agent definitions verified (2 agents registered).`

A subdirectory of `.agents/agents`, such as `<name>/AGENTS.md` next to an `agent.json`, is not
a persona file: `compile-context` neither projects nor lints it. The gate names every such
entry with what it holds, and fails even when flat persona files sit beside it:

```text
[FAIL] .agents/agents holds entries compile-context does not project (2):
repo-auditor/ (holds AGENTS.md), repo-gatekeeper/ (holds AGENTS.md); praetor reads one
persona file per agent, .agents/agents/<name>.md; move each definition to that layout, or
decline agent-definitions in adoption.decline to keep another
```

The line is wrapped here; audit prints it as one line. A subdirectory holding an `agent.json`
but no `AGENTS.md` is named the same way, never counted. A directory with neither persona files
nor subdirectories still fails with `contains zero agent definitions`. A symlinked persona file
is refused, as `compile-context` refuses it; a symlinked entry without the `.md` suffix is
named and never followed.

A repository that keeps its own agent layout records `adoption.decline: [agent-definitions]`
in `.standards.yaml`. Adoption then writes no personas, and audit reports the decline together
with what `.agents/agents` holds instead of failing. Persona files that do exist are still
projected and linted by the projection and caveman gates, which have no opt-out
([the persona and skill gate](text-register.md#the-persona-and-skill-gate)). An unknown or
malformed decline entry fails closed. `cmd/standardsctl/audit_agent_definitions_test.go` and
`internal/compiler/agent_inventory_test.go` pin each case.

### Writing the ruleset, labels and repository metadata to GitHub with `sync --remote`

`standardsctl sync` verifies `.config/labels.yaml` and `.github/rulesets/main.json` locally.
Only `--remote` writes to GitHub, with a token from `--token`, `GITHUB_TOKEN` or `GH_TOKEN`; the
gh CLI session is never used.

```bash
standardsctl sync --remote                              # origin must be on github.com
standardsctl sync --remote --forge-host=ghe.example.com \
  --endpoint=https://ghe.example.com/api/v3             # GitHub Enterprise
```

- **Repository identity.** The `origin` remote must name `--forge-host` (default `github.com`)
  and the path `<repository.owner>/<repository.name>` from `.standards.yaml`. Another host, a
  local path or a `file://` URL is refused before any request (`util.ParseGitRemote`,
  `TestSync_Remote_OriginHost`).
- **Ruleset.** GitHub gets the ruleset `.github/rulesets/main.json` declares: the name
  `praetor-main-protection` and the refs `refs/heads/<default branch>` and `refs/heads/lts-*`
  (`forge.RepositoryRulesetRefs`). The default branch is the one the local ruleset was verified
  for (`forge.RepositoryDefaultBranch`, see
  [Protected default branch](../adoption.md#protected-default-branch),
  `TestSync_Remote_DefaultBranchMaster`). The required status checks it adds are the local
  ruleset's checks whose jobs report in `<repository.owner>/<repository.name>`: a job behind a
  repository guard that names another repository is left off (`forge.RequiredStatusContextsIn`,
  `TestSync_Remote_RequiresOnlyChecksThatReportInTheRepository`; see
  [Which jobs the ruleset requires](../adoption.md#which-jobs-the-ruleset-requires)). After the
  write, the live ruleset is read back and each left-off check is named: an `[INFO]` line when
  GitHub does not require it, and a `[WARN]` line when the live ruleset still requires it because
  an earlier sync added it. The merge below keeps that check, so remove it by hand
  (`TestSync_Remote_WarnsAboutLeftOffChecksTheLiveRulesetStillRequires`). An existing ruleset is read, merged, updated and read back. The
  merge sets every parameter praetor renders from policy, a declared relaxation such as
  `review_mode: single_maintainer` included. Each parameter whose live value was stricter (a
  higher approving review count, or stale-review dismissal, code-owner review, last-push
  approval, thread resolution or the up-to-date check policy switched on) is printed on a
  `[LOWERED]` line that names the rule, the parameter, the live value and the declared one, so no
  hosted setting drops silently. The merge keeps
  everything else: extra refs, bypass actors, other rules and parameters, and required checks
  praetor does not list. A protected ref is removed from the excludes. The command fails unless
  the readback includes everything written (`internal/forge/ruleset_merge.go`,
  `TestGitHubDriver_ReconcileProtection_Positive_MergesLiveRulesetWithoutNarrowing`,
  `TestGitHubDriver_ReconcileProtection_Boundary_SingleMaintainerLowersLiveReviews`,
  `TestMergeRuleset_Positive_AppliesAndReportsLoweredParameters`). Removing a rule or ref from a
  live ruleset is a manual change on GitHub.
- **Branch protection readback.** Before and after the ruleset write, sync reads what the default
  branch enforces from both of GitHub's mechanisms: the active rules of every ruleset that targets
  it, organisation rulesets included, and the legacy branch protection object, whose "Branch not
  protected" answer means the branch has none (`forge.GitHubDriver.ReadBranchProtection`). A
  default branch not pushed yet ("Branch not found") has no legacy object either; the report says
  it does not exist on GitHub yet, and the ruleset still applies to it once pushed. GitHub
  enforces the union of the two, so each declared property is compared with that union
  (`forge.EvaluateBranchProtection`): pull requests, approving reviews, code-owner review, stale
  review dismissal, signed commits, linear history, deletion and force pushes blocked, and the
  required status checks. The line before the write lists each `[DRIFT]` and the mechanisms
  found, or says that nothing protects the branch. After the write, every property is printed as
  `[OK]`, `[DRIFT]` or `[STRICTER]` with the mechanism that enforces it, such as
  `ruleset "praetor-main-protection" #7` or `branch protection`. A setting still stricter than
  declared after the write, marked `[STRICTER]`, comes from something sync does not write: a rule
  praetor does not render, another ruleset or legacy protection; change it on GitHub by hand if
  the declared policy is intended. If a declared property is still not enforced after the ruleset
  converged, because another ruleset, legacy protection or the repository's GitHub plan overrides
  it, the command fails instead of reporting success, and a legacy protection object the token
  may not read fails it before any write
  (`TestSync_Remote_ReadsBackBranchProtection_Positive`, `_Negative`, `_Boundary`).
- **Labels.** Every label in `.config/labels.yaml` is updated on GitHub, or created when GitHub
  lacks it; labels the taxonomy does not name are left alone (`forge.ParseLabelTaxonomy`,
  `TestSync_Remote_Labels`).
- **Repository metadata.** The `repository.description` and `repository.homepage` of
  `.standards.yaml` are written when they differ from GitHub's, and the declared
  `repository.topics` GitHub lacks are added. A field left unset is never cleared on GitHub, and a
  topic the manifest does not name is kept, so an empty topic list removes nothing. Topics are
  lower-cased as GitHub stores them, and one GitHub would refuse (anything but lowercase letters,
  numbers and hyphens, more than 50 characters, or more than 20 topics) fails the sync before
  anything is written to GitHub, the ruleset and labels included (`forge.ValidateRepositoryTopics`).
  Declared topics that would take the repository's own past 20 fail right after the repository
  is read, before the description or homepage is written; a topic write GitHub rejects after
  those were written names them in the error. `repository.visibility` is compared and a mismatch
  printed as `[DRIFT]`, but never written: making a repository public or private stays the
  operator's decision (`internal/forge/repo_metadata.go`, `TestSync_Remote_RepositoryMetadata`).

### Comparing live branch protection with `plan --remote`

`standardsctl plan` previews the effective policy, the local files and the live Actions checks
([Live Actions checks](actions-live-checks.md)); `--offline` skips every forge read. Without
`--remote` it does not read branch protection and says so on its last line. `--remote` adds a
read-only comparison of the default branch's live protection with the declared policy, the same
readback `sync --remote` prints after its write, against the status checks `sync --remote` would
require there. Plan refuses `--offline` together with `--remote`, since the two contradict:

```bash
standardsctl plan --remote                              # same --token, --endpoint, --forge-host as sync
```

The token and repository identity follow `sync --remote`: `--token`, `GITHUB_TOKEN` or `GH_TOKEN`,
and an `origin` remote that names `<repository.owner>/<repository.name>` on `--forge-host`, checked
before any request. Nothing is written. When a declared property is not enforced, plan prints
`[DRIFT] GitHub does not enforce the declared ...` and the command that reconciles it,
`praetorctl sync --remote`. Like the local drift it reports, this leaves the preview's exit status
at zero (`cmd/standardsctl/plan.go`, `TestPlan_Remote_ComparesLiveBranchProtection_Positive`,
`TestPlan_Remote_ComparesLiveBranchProtection_Negative`).

### Label taxonomy

`praetorctl adopt` and `praetorctl sync` write the same eight-label
`.config/labels.yaml` into a repository that has none (`forge.DefaultLabelTaxonomy`,
which `internal/forge/labels_test.go` holds byte-for-byte equal to praetor's own file).
An existing taxonomy is the repository's configuration: `adopt --force` keeps it
(`TestAdopt_Positive_ForceKeepsRepositoryConfiguration` in `internal/adopt/adopt_test.go`).

### Stages that do not apply are skipped, not failed

`praetorctl gate run` records one of four verdicts per stage (`StageStatus` in
[`internal/gating/pipeline.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/pipeline.go)), and a stage that ran nothing is
never recorded as passed:

| Verdict | Report tag | Meaning |
| :--- | :--- | :--- |
| `passed` | `[PASS]` | the stage ran its checks and they held |
| `failed` | `[FAIL]` | the stage rejected the repository or could not run; the pipeline stops |
| `skipped` | `[SKIP]` | the stage applies here but did not run all its checks: a dry run, no race detector, no `cargo` or `cargo-audit`, or it ran for one language and not the other |
| `not_applicable` | `[N/A]` | the repository has nothing this stage checks |

`--json` carries the verdict as `stages[].status`, and the stage output the Exit-0 receipt signs
(`praetor-gate-output/v2`) carries it on every stage line. The previous `passed` bool rendered a
skipped stage and a passed one identically, so the CLI printed `[PASS]` and the receipt certified
security scans and prefetches that had never executed.

`praetorctl gate verify`, `praetorctl forge validate-pr` and `praetorctl paperclip verify` refuse a
receipt whose gate output does not open with `praetor-gate-output/v2`, after checking its signature
and output hash (`lockdown.VerifyPinnedReceiptFile` and `lockdown.VerifyUnpinnedReceiptFile` in
[`internal/lockdown/keys.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/lockdown/keys.go)). A
v1 receipt still carries a valid signature, but its stage lines cannot tell a skipped stage from a
passed one, so it is rejected with `receipt certifies an unsupported gate output version`. Re-mint
it with `praetorctl gate run` on the current release. `TestVerifyPinnedReceiptFile_Negative`,
`TestRunGateVerify_Negative`, `TestValidatePRChecklist_Negative_V1GateOutput` and
`TestDisposition_VerifyReceipt_V1Refused` sign a real v1 receipt and require each verifier to
refuse it.

A skipped or not-applicable stage names its reason and does not fail the repository:

- The prefetch, security and race-detector stages -- the toolchain stages -- are **not
  applicable** where the repository root holds neither a `go.mod` nor a `Cargo.lock`
  ([Cargo repositories](#cargo-repositories)). Without this a TypeScript or Python repository failed
  its own pre-push gate at `FAIL ./... [setup failed]`, which reads as a broken repository rather
  than an inapplicable stage. Such a repository still gets no receipt
  ([below](#no-receipt-when-no-toolchain-stage-ran)).
- Flavor conformance is **not applicable** where the repository's declared profile has no
  flavor implementing it -- an OS image forge is not a Go service and should not be measured as one.
- The race-detector stage is **skipped** where the race detector cannot build, naming what is
  missing:

  ```text
  5. [SKIP] Race-Detector Tests       (62ms)
     Reason: race detector unavailable (the C compiler "gcc" named by go env is not on PATH):
             race-detector tests skipped; CI runs this leg on Linux with cgo
  ```

  The detector needs cgo and a host C toolchain. Checking `CGO_ENABLED` alone is not enough, and
  the difference is the common case rather than an edge: a stock Windows Go reports
  `CGO_ENABLED=1` and `CC=gcc` while no gcc is installed, so the toolchain claims cgo and every
  race build still fails. The compiler the toolchain names is therefore resolved, not assumed.

  Without this the stage did not report a missing compiler -- it reported `# runtime/cgo` followed
  by every package failing to build, which reads as a repository whose whole tree is broken. On a
  workstation without a C toolchain that was every push, including a push fixing support for that
  platform, so the gate could not be repaired from the platform it was broken on. Set
  `CGO_ENABLED=0` to skip the stage deliberately; install a C toolchain to run it.

A skipped stage prints its reason. That distinction matters: a skipped stage that reads as a pass is
how a gate comes to certify what it never examined. The verdicts are pinned by
`TestExecuteStage_SkipVerdicts` and `TestStageOutput_3D` in
[`internal/gating/gating_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/gating_test.go).

### Cargo repositories

Where the repository root holds a `Cargo.lock`, the toolchain stages run Cargo besides Go, or
instead of it ([`internal/gating/cargo.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/cargo.go)):

| Stage | Cargo command | Runs in | Bound |
| :--- | :--- | :--- | :--- |
| Prefetch & Lockfiles | `cargo fetch --locked` | the repository | `DefaultPrefetchTimeout`, 60 s |
| Security & SCA Scan | `cargo audit`, when `cargo-audit` is on `PATH` | the repository | `CargoAuditTimeout`, 3 min |
| Race-Detector Tests | `cargo test --workspace --locked`, then `cargo clippy --workspace --all-targets -- -D warnings` | the isolated worktree | a race stage bound of its own |

- A `Cargo.toml` without a committed `Cargo.lock` runs no Cargo command: every command is held to
  the lockfile with `--locked`, and without one there is nothing to hold it to.
- A missing toolchain is reported, never passed. Without `cargo` on `PATH` each Cargo part reads
  `not run: cargo is not on PATH (install the Rust toolchain from https://rustup.rs)`. Unlike the
  Go scanners, `cargo audit` is optional: without `cargo-audit` the part reads
  `cargo audit not run: cargo-audit is not installed (cargo install cargo-audit --locked)`.
- Where a `Cargo.lock` is present, each toolchain stage's reason names every language's outcome,
  and that reason is part of the signed stage output. A stage that ran for one language and not
  the other is recorded as skipped, not passed:

  ```text
  3. [SKIP] Security & SCA Scan       (4.1s)
     Reason: go: passed; cargo: cargo audit not run: cargo-audit is not installed (cargo install cargo-audit --locked)
  ```

- A repository without a `Cargo.lock` runs the same commands and records the same verdicts and
  reasons as before Cargo support, so its signed output is unchanged
  (`TestToolchainStages_Positive_GoPathUnchanged`).
- The Cargo suite runs in a worktree of its own under a whole race stage bound (default 3 min),
  not what the Go suite left of one. The run deadline reserves that bound beside the Go suite's
  and adds the `cargo fetch` and `cargo audit` bounds to the other stages' allowance
  ([the whole run's deadline](#the-whole-runs-deadline)). Raise `PRAETOR_TEST_STAGE_TIMEOUT` as
  [the race stage's bound](#the-race-stages-bound-and-what-firing-it-means) describes; it applies
  to each suite.
- `cargo test` and `cargo clippy` build into `praetor/cargo-target` under the repository's git
  common directory (`cargoTargetDir` in `cargo.go`), passed as `--target-dir`. The test worktree is
  removed after every run, and Cargo's default `target/` inside it went with it, so every run
  compiled every dependency twice inside the bound. The shared directory outlives the worktree, is
  the same for every linked worktree of the clone, sits outside every working tree (the tree the
  receipt certifies stays clean), and does not contend with your own `target/`. Only the first run
  in a clone builds cold; give that run a raised bound on a large workspace.
  - An absolute `CARGO_TARGET_DIR` is used instead, without a flag. A relative one would resolve
    inside the removed worktree, so the gate's directory is used.
  - Where no git common directory resolves, for example when `--path` names a subdirectory of the
    checkout, the commands run without the flag, and the stage reason says the run compiled every
    dependency.
  - The directory grows like any `target/`; delete it to reclaim the space, and the next run
    rebuilds it.
- A failing command's standard output is cut at 64 KiB in the stage reason; `util.RunCommand`
  already bounds its standard error the same way.

[`internal/gating/cargo_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/cargo_test.go) replays each outcome
through a recording command runner, and the `TestCargoTargetDir_*` cases pin the shared directory.
[`internal/gating/cargo_path_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/cargo_path_test.go) builds a
stand-in `cargo` with `testsupport.BuildExecutable`, puts it first on `PATH`, and runs the stages
through the production runner: all commands passing mints a receipt that verifies and leaves the
build output in the shared directory after the worktree is gone, and a clippy failure mints none.

### No receipt when no toolchain stage ran

The receipt stage signs only after at least one toolchain stage ran and passed for some language
(`requireVerification` in
[`internal/gating/languages.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/languages.go)). When the prefetch,
security and test stages were each not applicable or skipped, it fails, the run is rejected with
exit status 1, and the reason names what the repository holds:

```text
6. [FAIL] Ed25519 Exit-0 Receipt    (1ms)
   Reason: no verification stage ran for any language: Prefetch & Lockfiles, Security & SCA Scan
           and Race-Detector Tests each ran nothing, so no Exit-0 receipt is signed; the gate runs
           the toolchains of Go (go.mod) and Cargo (Cargo.lock); unsupported languages at the
           repository root: node (package.json), python (pyproject.toml)
```

The reason says instead that a `Cargo.lock` is present but its stages could not run on this host,
or that no language marker at the root is recognised. Before this check such a repository received
a receipt that certified nothing beyond the HISS scan.

**Migration.** A repository with neither a `go.mod` nor a `Cargo.lock` at its root no longer
receives a receipt. Commit the `Cargo.lock` of a Cargo workspace; for any other language no
receipt can be minted until the gate runs its toolchain. A dry run is never refused, because it
mints nothing. `TestRunReceiptStage_Boundary_NothingVerifiedIsRefused` and
`TestToolchainStages_Boundary_CargoAbsentFromPath` in
[`internal/gating/cargo_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/cargo_test.go) pin the refusal.

### The pre-push hook admits languages the gate has no runner for

`praetorctl gate run --admit-unsupported` changes one verdict. When no toolchain stage ran and the
root holds neither a `go.mod` nor a `Cargo.lock`, the receipt stage is recorded as not applicable
instead of failed, and the run is admitted with exit status 0 and no receipt. Every other stage
runs as without the flag, so a HISS or flavor failure still rejects the run. The reason is the
refusal's own text followed by the admission, and the report ends with a line saying so:

```text
6. [N/A]  Ed25519 Exit-0 Receipt    (1ms)
   Reason: no verification stage ran for any language: ... the gate runs the toolchains of Go
           (go.mod) and Cargo (Cargo.lock); unsupported languages at the repository root: meson
           (meson.build); admitted without a receipt (--admit-unsupported): verify these languages
           with the repository's own entry point, such as make verify-all

Admitted without an Exit-0 receipt (--admit-unsupported): the gate runs no toolchain for the
languages at the repository root, so they are unverified here; the receipt stage names them.
```

`--json` carries the same verdict and `"admitted_unverified": true`. A root that holds a `go.mod`
or a `Cargo.lock` is refused with the flag exactly as without it when its stages did not run, so a
missing `cargo` never passes for a Cargo workspace. A `Cargo.toml` without its `Cargo.lock` is a
language the gate cannot run, so it is admitted and named.

The `gate` job of the `lefthook.yml` adoption writes passes the flag (`prePushGateArgs` in
[`internal/adopt/hooks.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/hooks.go)),
and its header states the rule. Before, that job ran the plain gate, so a Meson, CMake, npm or
Python repository had every push rejected, and the only ways past were editing the generated file,
which stops adoption from activating it, or skipping hooks, which the agent hook policy refuses
(#648). Adoption migrates an unedited earlier rendering without `--force`
([Migration and activation limits](#migration-and-activation-limits)). CI and the gatekeeper need
a receipt and run the gate without the flag. Tests:
[`internal/gating/admit_unsupported_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/admit_unsupported_test.go),
[`cmd/standardsctl/gate_admit_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/gate_admit_test.go) and
[`internal/adopt/lefthook_gate_admit_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/lefthook_gate_admit_test.go).

### A receipt is signed only by the pinned key

When `.standards.yaml` pins `receipt.public_key`, the receipt stage signs only with that key's
private half. It reads the pin through `lockdown.PinnedPublicKey`, the read `praetorctl gate
verify` resolves its default key with, and checks the signed receipt with
`lockdown.VerifyPinnedReceipt`, the check `gate verify` applies (`signReceipt` in
[`internal/gating/receipt_key.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/receipt_key.go)).
A signing key from `PRAETOR_RECEIPT_KEY` or the per-user key file that is not the pinned key fails
the stage, writes no receipt, and names both keys:

```text
6. [FAIL] Ed25519 Exit-0 Receipt    (4ms)
   Reason: Exit-0 receipt not written, gate verify would reject it: receipt was not signed by the
           pinned receipt.public_key: signing key <signing>, pinned key <pinned> in .standards.yaml;
           sign with the pinned key's private half (PRAETOR_RECEIPT_KEY, or the key file
           'praetorctl gate keygen' wrote) or pin the signing key instead
```

Before this check the stage reported `[PASS]` and pointed at `gate verify`, which then rejected
the receipt. A repository without a manifest, or whose manifest pins no key, still receives a
receipt signed by whichever key is configured; only `gate verify --public-key` verifies it. A
malformed pin and a manifest that cannot be read, such as one with a second YAML document, fail the
stage before the signing key is loaded, as they fail `gate verify`.

**Migration.** A workstation whose signing key is not the pinned key no longer receives a receipt,
so the `gate` job of its pre-push hook fails. Point `PRAETOR_RECEIPT_KEY` or the per-user key file
at the pinned key's private half, or, when this workstation's key is meant to be the repository's
trust anchor, pin its public half as `receipt.public_key`. `TestRunReceiptStage_Positive_PinnedSigningKeySigns`,
`TestRunReceiptStage_Negative_UnpinnedSigningKeyRefused` and
`TestRunReceiptStage_Boundary_PinnedKeyResolution` in
[`internal/gating/receipt_key_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/receipt_key_test.go)
pin each case.

### A dry run changes nothing

`praetorctl gate run --dry-run` runs only the read-only stages: lockfile verification, the HISS
scan and flavor conformance. It records the module prefetch (`go mod verify`, `go mod download`),
the security scanners (`go list`, govulncheck, gosec), the race-detector tests and the receipt as
**skipped**, and mints no receipt, and it skips every Cargo command the same way. `go mod download`
writes the module cache and the scanners can fetch modules and query the vulnerability database,
so a dry run that started them was not one. `TestExecuteStages_DryRunInvokesNoCommand` in
[`internal/gating/gating_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/gating_test.go) runs a whole dry run
through a recording command runner and fails if any command starts through the stage runner;
`TestExecuteStages_DryRunWithCargoLockInvokesNoCommand` does the same with a `Cargo.lock`.

The gatekeeper persona that adoption writes (`.agents/agents/repo-gatekeeper.md`) and the
pre-migration epic's verification task both run the full gate, `praetorctl gate run --path=.`,
derived from `gating.RepoRunCommand`
([`internal/gating/pipeline.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/pipeline.go)). The persona names `--dry-run`
only as a read-only preflight: its mission is the prefetch, security scans and receipt that a dry
run skips. Praetor's own `praetor-gatekeeper` persona runs the same full gate that
`praetorctl agent run praetor-gatekeeper` runs, then `gate verify`, and lists the dry run as a
preflight. All three previously passed `--target=.`, which `gate run` rejects as an undefined flag
before any stage runs.
[`cmd/standardsctl/gate_command_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/gate_command_test.go) parses the
constant and every `gate run` line in `.agents/agents/*.md` against the real flag set;
[`internal/adopt/persona_command_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/persona_command_test.go) and
`TestGeneratePreMigrationEpic_GateTaskRunsTheGateCommand` in
[`internal/needs/epic_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/needs/epic_test.go) pin the generated text to it.

`praetorctl agent run praetor-gatekeeper` also fails the way `gate run` does. It prints the
stage report, then the verdict line, and exits 1 when the pipeline is REJECTED. It used to print
only the verdict and exit 0 on a rejection, so a script or agent that ran it read a rejected
repository as admitted. `TestGatekeeperAgent_Negative_RejectedExitsOneWithTheFailingStage` in
[`cmd/standardsctl/agent_gatekeeper_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/agent_gatekeeper_test.go)
pins the exit code and the failing stage's reason in the output.

A help probe never starts the pipeline. `--help`, `-h` or `help` anywhere after `agent`, for
example `praetorctl agent run praetor-gatekeeper --help`, prints the agent usage and exits 0
before any helper runs, the way `praetorctl gate --help` does; both use `isHelpToken` in
[`cmd/standardsctl/main.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/main.go).
Any other argument after the persona is refused.
[`cmd/standardsctl/agent_help_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/agent_help_test.go)
counts pipeline calls for every help spelling and position.

### A receipt certifies only a working tree that matches HEAD

The scan stages read the working tree, while the receipt names a commit. `gate run` without
`--dry-run` therefore refuses, before any stage runs and without writing a receipt, a tree that
differs from HEAD:

- a modified, staged or untracked file anywhere in the repository, the receipt file itself
  excepted, because the gate rewrites it and a checkout may keep it untracked. "Untracked"
  means what plain `git status` lists: the repository's ignore files, `.git/info/exclude` and
  your global excludes file (`core.excludesFile`, else `~/.config/git/ignore`) all apply, so
  editor and OS files you ignore globally do not block the gate;
- an index entry flagged assume-unchanged or skip-worktree, which `git status` never compares;
- an untracked `.standards-baseline.json` or `.gosec.json`, hidden from `git status` by any
  ignore rule, your global excludes file included. Both relax what the gate enforces -- the
  baseline raises the HISS limit, the gosec configuration selects the rules -- and `git status`
  does not list an ignored file, so each present one must be tracked in the index
  (`util.GitUntrackedPaths` in [`internal/util/git_ignore.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/util/git_ignore.go));
  a committed one stays trusted even when an ignore pattern also matches it;
- a `.standards-baseline.json` or `.gosec.json` that is a symbolic link or any other non-regular
  file. `git status` compares a tracked link by its target path, not the content behind it, while
  the stages follow the link, so a committed link to an ignored file or to one outside the
  repository would read as clean. Replace the link with the file itself.

The refusal is recorded as a failed `Clean Tree Precondition` stage naming the first changed paths:

```text
  1. [FAIL] Clean Tree Precondition   (0s)
     Reason: the gate certifies only a working tree that matches HEAD: 1 changed path(s) differ
             from HEAD: ?? scratch.txt; commit, stash or remove the changes, or preview with --dry-run
```

`--json` carries the same text as `worktree_problem`. A dry run mints nothing, so it reports the
state on a `Worktree:` line and runs its stages anyway. The receipt stage reads the tree again
before signing and refuses when it changed or HEAD moved while the stages ran. Before this, the
gate recorded `worktree_clean false` in the signed output and signed it anyway, so a receipt could
certify a commit whose scan had read uncommitted files (BUG-787) or an untracked baseline
(BUG-788).

Verification enforces the same rule on the receipt side: `gate verify`, `forge validate-pr` and a
paperclip disposition carrying a receipt all reject one whose signed gate output does not record
`worktree_clean true` exactly once in its header. The check (`lockdown.RequireCleanWorktree` in
[`internal/lockdown/receipts.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/lockdown/receipts.go)) runs inside
`lockdown.VerifyPinnedReceiptFile` and `lockdown.VerifyUnpinnedReceiptFile`
([`internal/lockdown/keys.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/lockdown/keys.go)), right after the format-version
check, so all three callers get it from the verifier they already share.

`praetorctl paperclip verify` still leaves the gate receipt and the disposition file out of its
own clean-tree check, so the documented order -- commit, push, mint the receipt, write the
disposition -- verifies unchanged.

Cleanliness is read through `util.GitWorkingTreeChanges`
([`internal/util/git_status.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/util/git_status.go)), which the gate,
`praetorctl paperclip verify` and release preparation share. It overrides the repository settings
that could hide a change (`status.showUntrackedFiles`, submodule ignore settings, `core.fsmonitor`,
hooks), takes no optional index locks, and refuses a repository whose own configuration names a
clean or process filter that a tracked path's filter attribute selects, rather than executing it
during a read-only probe; a driver no tracked path selects never runs and passes. Each caller bounds the
whole walk: the gate with `gating.GitQueryTimeout`, paperclip verify and release preparation with
`util.GitTreeProbeTimeout`; a probe that runs out of time is a refusal, never a clean answer. The
cases are replayed in [`internal/util/git_status_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/util/git_status_test.go) and
[`internal/gating/tree_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/tree_test.go); the receipt-side checks in
[`internal/lockdown/keys_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/lockdown/keys_test.go) and
[`internal/paperclip/paperclip_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/paperclip/paperclip_test.go).

### A HISS rejection names the violations

The gate's HISS stage rejects on the same ratchet as `praetorctl audit`, and both render the
rejection with `baseline.RatchetResult.Summary`
([`internal/baseline/describe.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/baseline/describe.go)): the counts, then up to
three violations of each class as `[rule] file:line - message (class)`. The stage previously
reported only `hiss ratchet failed: N infractions (M new, baseline B)`, so a blocked push named no
file to open.

A class with more than three violations ends with a line that says how many it hid and which
read-only command lists them all. `praetorctl baseline --verify` and `praetorctl audit` print the
whole list with `--all-violations` and never write `.standards-baseline.json`:

```text
  ... and 3 more unbaselined violations not shown; 'praetorctl baseline --verify --all-violations' lists every one
  ... and 2 more touched-file violations not shown; 'praetorctl audit --all-violations' lists every one
```

Touched-file violations need the audit's change set, so their marker names `praetorctl audit`; run
it with the same `--base` as the failing run. `TestRatchetResultSummary_Boundary_MarkerCountsEachClass`
in [`internal/baseline/describe_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/baseline/describe_test.go) and
`TestBaselineVerify_Positive_AllViolationsListsEveryOne` in
[`cmd/standardsctl/baseline_ratchet_report_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/baseline_ratchet_report_test.go) pin both.

A violation in an untouched file that the baseline does not record is not necessarily new code: a
Praetor upgrade can add a check that reports code nobody changed. `praetorctl audit`,
`praetorctl baseline --verify` and the `standards_audit` MCP tool therefore attribute each one
before rendering (`hiss.AttributeRatchet` in
[`internal/hiss/attribution.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/attribution.go)). They compare against the
baseline's commits:

- `commit_sha`, the `HEAD` that `praetorctl baseline --record` ran on;
- the last commit on `HEAD`'s history that changed the baseline file (`git log -1 HEAD -- <baseline>`).

The recorder scans the work tree, so a baseline recorded before the code it scanned was committed,
then committed together with that code, holds the code only at the second commit. After a squash
merge, `commit_sha` names a branch commit a fresh clone does not hold; the second commit is the
squash commit. A commit the clone lacks is left out, and so is a shallow clone's boundary commit,
which reads as having added every file whatever it changed. An uncommitted baseline compares
`commit_sha` alone.

For each commit, they copy the violations' files as that commit holds them into a temporary
directory and scan the copy with the current checks and the same policy. A Go call cycle brings
its whole package. The copy is scanned as its own scope, without asking git which files belong, so
a temporary directory inside a work tree that ignores it changes nothing. A copy scan that leaves a
file unread, or a file of the commit that does not parse, traces nothing:

| Class tag | Meaning |
| :--- | :--- |
| `(new)` | The checks report it at neither commit: the code changed since the baseline was recorded and committed. |
| `(check added or changed since the baseline)` | The checks report the same rule, file, symbol and message at one of the commits, yet the baseline does not record it: a check or limit changed, not the code. |
| `(recorded in the baseline at line N)` | The baseline records the same rule, file, symbol and message at line N, which no current violation occupies: lines above it moved, the debt did not. Needs no commit, so a baseline without `commit_sha` gets it too. |
| `(not in the baseline)` | Not traced. The baseline records no `commit_sha`, the clone holds neither commit, the violations span more than 200 files, they need more than 1000 committed files (a large Go package a call cycle brings counts whole), the copy scan was incomplete, or the rejection comes from a surface that does not attribute (the gate's HISS stage, dogfood verification). The rejection states the reason. |

Only a rejection whose every unbaselined violation is `(new)` keeps the header `HISS invariant
violations introduced`. One whose every unbaselined violation only moved reads `HISS invariant
violations the baseline records at other lines` and names the plain re-record, `praetorctl
baseline --record`: the count did not rise, so it needs no `--allow-increase`. Every other
rejection reads `HISS invariant violations the baseline does not record` and names the deliberate
re-record, `praetorctl baseline --record --allow-increase --reason=<why>`, for the findings a
changed check or an untraced cause explains. Matching ignores line numbers, so when one file holds
several identical findings, the count per class is exact but which line gets `(new)` follows scan
order.
The verdict never changes: HISS-13 still refuses the higher count
([`internal/hiss/attribution_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/attribution_test.go),
`TestRatchetResultAttribute*` in `internal/baseline/describe_test.go`,
`TestBaselineVerify_Positive_MovedFindingNeedsOnlyARerecord` and
`TestBaselineVerify_Positive_RecordThenCommitIsNotNew` in
`cmd/standardsctl/baseline_ratchet_report_test.go`).

Known gaps of the attribution:

- A baseline without `commit_sha`, such as one written before the field was filled, stays
  untraced even when a commit holds the baseline file.
- The baseline records no engine version or rule set, so the attribution cannot name the check
  that changed; it re-scans the baseline's commits instead.
- The gate's HISS stage and the dogfood public-checkout verification do not attribute; their
  rejections read `(not in the baseline)`.

A ratchet can also fail with both lists empty: every violation matches a baselined fingerprint
and no file was touched, yet the total rose above the baseline's. `RatchetResult.CountRegressed`
marks that case, and `Summary` names both totals (`total infractions rose from 5 to 7 (no new
fingerprints)`) rather than a listing that reads as zero new violations. The `standards_audit`
MCP tool and the dogfood public-checkout verification render their rejections through the same
method (`TestEvaluateRatchet_CountRegressed_3D` and `FuzzBaselineRatchet` in
`internal/baseline`).

### The HISS stage scans with the audit's function-length limit

The gate's HISS stage resolves the function-length limit through
`config.ResolveRepositoryComplexity` (`internal/config/repository_policy.go`), the resolver the
editor projections use, and prints it on the stage line (`function length limit N`):

- A repository with `.standards.lock` scans at the limit `praetorctl audit` enforces.
- A manifest without a lock scans at the HISS-04 ceiling tightened by its
  `overrides.complexity.max_func_loc`.
- A tree without `.standards.yaml` scans at the ceiling.
- A manifest or lock that does not resolve scans at the ceiling, and the stage line appends the
  resolver's `repository policy unresolved (...)` warning; `praetorctl audit` fails on that state.

The gate used to scan at the scanner's 60-line default whatever the manifest declared, so a
repository with a stricter limit passed the gate with functions its audit rejected (BUG-638,
`internal/gating/pipeline_test.go`).

### A failing flavor stage names the files that cost the score

The flavor stage scores required templates and settings, and a setting counts only where the file
is present **and** parses as the shape that setting declares
([onboarding guide](onboarding.md#what-the-flavor-score-measures)). A repository can
therefore fail this stage with every template present: two settings that exist but do not parse put
a go-library repository at 4 of 6 required items, 66.7%, below the 80% bar.

The failure names them, because at that point the push is already blocked:

```text
  4. [FAIL] Flavor Conformance        (3ms)
     Reason: flavor audit failed (score: 66.7%, 0 missing templates, missing or invalid settings: lefthook.yml, .github/rulesets/main.json)
```

Without the file list the whole report was `score: 66.7%, 0 missing templates`, which tells an
operator that something is wrong and nothing about which file to open. `praetorctl flavor audit .`
prints the same files with their names and descriptions under **Missing or Invalid Settings**.

### The race stage's bound, and what firing it means

The race-detector stage is bounded, because an unbounded stage is how a gate hangs instead of
failing (HISS-02). The default is 180 seconds, and `PRAETOR_TEST_STAGE_TIMEOUT` raises it up to a
30-minute ceiling. The stage passes the same bound to `go test -race -timeout`, so every package
binary may use the whole stage budget; go test's own default of ten minutes per package would
otherwise panic a slow package mid-suite while the stage still had budget left
(`TestRunTestStage_3D` in `internal/gating/gating_test.go` pins the command line).

The override is clamped rather than trusted. An empty, unparseable, zero, negative or
over-ceiling value falls back to the default or the ceiling and **says which**, so a typo cannot
quietly remove the bound or shrink it to nothing. A raised bound is printed as the stage's reason,
so it appears in the receipt: an override that changed the gate's strictness without showing up in
its output would be an invisible difference between what one operator verified and what every
reviewer reads.

When the bound fires, the stage says so rather than reporting a test failure. These are different
outcomes and used to print identically:

```text
5. [FAIL] Race-Detector Tests  (3m0.024s)
   Reason: tests failed in .standards/worktrees/gate-2856-...: ok github.com/... 1.437s
```

That message is the tail of a *successful* run that was cut off, so the first reading is always
"my change broke the tests" — and the suite had not broken at all. The stage now names the bound,
the variable that raises it and the ceiling, so the reader is pointed at the real cause. A suite
that genuinely fails inside the bound still reports as a test failure; the fix must not trade one
wrong diagnosis for another.

The isolated worktree is created under the same bound, so the bound can fire one step earlier
than the tests. On a Windows host with the bound set to 50 ms it did: `git worktree add` outlasted
it, the kill surfaced as a bare `exit status 1`, and the stage reported a repository whose
worktree could not be created — the same misattribution, pointing at the checkout instead of at
the suite. Creation now reports the bound when the deadline is what stopped it, and keeps the
"could not be created" text for every other cause.

### The whole run's deadline

`gate run` has a deadline of its own, and it is derived from the race stage's bound rather than
fixed: the resolved bound plus a five-minute allowance for every other stage
(`OtherStagesAllowance` in [`internal/gating/deadline.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/deadline.go)).
The default is therefore 3 + 5 = 8 minutes, and the 30-minute ceiling gives 35. The run prints
the value it applies on its `Run Deadline:` line, and `praetorctl gate deadline [--json]` prints
it without running anything:

```text
$ PRAETOR_TEST_STAGE_TIMEOUT=30m praetorctl gate deadline
Run Deadline: 35m0s (30m0s race stage bound + 5m0s for the other stages)
Note: race stage bound raised to 30m0s by PRAETOR_TEST_STAGE_TIMEOUT
```

The run deadline used to be a fixed five minutes, so no value of `PRAETOR_TEST_STAGE_TIMEOUT` could
give the race stage more than what was left of those five minutes (#314). The `praetor-gatekeeper`
agent helper runs the same pipeline and takes the same derived deadline.

The deadline is sized to the repository `--path` names (`ResolveRepoRunBudget` in `deadline.go`):

| Repository root holds | Test suites, one bound each | Allowance for the other stages |
| :--- | :--- | :--- |
| `go.mod`, no `Cargo.lock` | 1 | 5 min |
| `Cargo.lock`, no `go.mod` | 1 | 5 min + 1 min `cargo fetch` + 3 min `cargo audit` = 9 min |
| `go.mod` and `Cargo.lock` | 2 | 9 min |

A mixed repository therefore gets 2 × 3 + 9 = 15 minutes by default. Sized for one suite, the run
deadline let the Go suite and the Cargo commands spend the time the Cargo suite needed, and no value
of the variable could fix it. `gate deadline` takes the same `--path` (default `.`) and reports the
suite count as `test_suites` in its JSON:

```text
$ praetorctl gate deadline --path=.
Run Deadline: 15m0s (2 test suites at a 3m0s stage bound each + 9m0s for the other stages)
```

Two deadlines can now stop the race stage, and the stage names the one that actually fired. Only
when the stage's own bound fired does it report `hit the … stage bound`. When the run deadline
fired first — which means the stages before it used more than their allowance — it reports the
run deadline and its value instead:

```text
race-detector tests in .standards/worktrees/gate-… did not finish: the gate run's deadline of
8m0s (3m0s race stage bound + 5m0s for the other stages) fired first, not the 3m0s stage bound,
and this is not a test failure.
```

Before this, a run cut at 4 minutes 57 seconds reported `hit the 30m0s stage bound`, which sent the
reader to raise a bound the stage had never reached. A deadline or cancellation from a library
caller that does not use the gate's run deadline is reported as `its caller stopped it`. Any other
stage the run deadline cuts off is labelled the same way (`stage "…" did not finish: the gate run's
deadline … fired first, so this is not a finding`), so a scanner killed mid-run does not read as a
scanner finding. The cases are replayed in
[`internal/gating/deadline_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/deadline_test.go) and
[`cmd/standardsctl/gate_deadline_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/gate_deadline_test.go).

Governance profile names no longer select Go or Meson commands. A shared plan
renders both newly generated Makefiles and AGENTS.md. Discovery recognizes:

- Go modules: build and race tests; Cargo projects: locked build and tests.
- Root npm scripts: a declared build, optional check, and a test script that can
  pass: not blank, and not the `echo "Error: no test specified" && exit 1`
  placeholder `npm init` writes. Another declared package manager requires an
  explicit project contract. `internal/nodemanifest/scripts.go` holds both
  decisions. The `typescript-node` CI scaffold reads `packageManager` and the
  test script the same way, and installs with pnpm, Yarn or Bun where the
  repository uses one ([archetype authoring](archetype-authoring.md)).
- C# project files: locked restore and Release build with warnings as errors;
  explicit unconditional test projects receive `dotnet test`. Conditional,
  contradictory, or disabled test markers cannot establish a test gate.
- Python: explicit pytest configuration, or tests under `tests/` with an exact
  Python 3.14+ version pin. Pytest configuration is any root file pytest itself
  reads, in pytest's precedence order: `pytest.toml`, `.pytest.toml`, `pytest.ini`
  or `.pytest.ini` (even empty), a `pyproject.toml` with a `[tool.pytest]` or
  `[tool.pytest.ini_options]` table, a `tox.ini` with a `[pytest]` section, or a
  `setup.cfg` with a `[tool:pytest]` section
  ([pytest configuration reference](https://docs.pytest.org/en/stable/reference/customize.html)).
  The report names the file and table it used. A `pyproject.toml`, `tox.ini` or
  `setup.cfg` without that table or section is not pytest configuration; section
  lines are read the way pytest's INI parser reads them, so an indented `[pytest]`
  or `[ pytest ]` is not the section (`pytestConfiguration` in
  `internal/adopt/verification_pytest.go`, tested by
  `TestVerificationPytestConfigurationPositive` and
  `TestVerificationPytestConfigurationNegative`). `tox.ini` and `setup.cfg` also
  configure other tools, in repositories with no Python at all, so one that
  discovery cannot read (a symlink, a file that is not UTF-8 text, one past the
  per-file byte bound, one the process may not read) is not pytest
  configuration: adoption goes on, the plan's `unreadable` list says why, and
  the report's verification warning names the file
  (`TestVerificationUnreadablePytestConfigurationIsNotConfiguration` and
  `TestAdoptWithUnreadableSetupCfgWarnsInsteadOfFailing` in
  `internal/adopt/verification_pillar_test.go`). Unittest commands check the
  interpreter version before discovery. Python 3.14 fails when discovery finds no
  tests. A separate build command remains necessary; a Python-only project without
  one needs a custom contract rather than a generated no-op build.
- Zig builds: a root `build.zig` runs `zig build`, its default install step,
  ahead of every other build and test command, because language builds such as
  Cargo's link the native libraries it produces. `build.zig` is Zig source, not
  metadata: discovery records its presence without reading it, so a build script
  of any size fits the byte bounds, and a symlinked one is refused. It supplies
  no test command, since `zig build test` exists only where the script declares
  a test step. The language markers beside it supply the tests; without one the
  plan is unavailable and its reason names the missing native test step for
  `build.zig`. A `build.zig.zon` alone selects nothing, and a `build.zig` below
  the root is not a marker (`internal/adopt/verification.go` `addZigVerification`,
  `internal/adopt/verification_zig_test.go`). The walk skips the trees Zig
  writes inside the checkout, `zig-pkg` (fetched packages), `zig-out` and
  `.zig-cache` (`util.IsToolchainTreeDir`): their files spend no entry bound,
  and the C sources of a fetched package do not make a pure-Zig repository
  C/C++ (`TestVerificationSkipsZigToolchainTrees`,
  `TestVerificationZigToolchainTreesSpendNoEntries`).

Mixed projects retain all detected gates; npm build precedes .NET builds for
frontend resources, and `zig build` precedes both. A solution marker without projects, or Meson/CMake markers
without a selected configured build directory, remains unavailable. Discovery is
bounded to 65,536 entries (`util.DefaultDiscoveryEntries` in
`internal/util/discovery_bounds.go`), 32 directory levels (to cover nested public
`src`/test project layouts), 128 metadata files, 64 KiB per metadata file and
2 MiB in aggregate. Generated dependency/build trees are omitted.
Exceeding a bound is an error, never a truncated successful plan, and the error names
the `--verification-max-*` flag that raises the entry, depth or file bound
([large repositories](../adoption.md#large-repositories)). Selected metadata
uses bounded reads that refuse symlinks. Command paths with line breaks are
rejected; shell arguments are quoted and Make dollar signs escaped.

The .NET runner selection follows `global.json`: VSTest uses a positional project,
while Microsoft.Testing.Platform uses `--project`. MTP requires a suitable .NET 10+
SDK and compatible test projects. Selection is still unverified until the declared
commands run. See the [VSTest CLI reference](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-test-vstest),
[MTP CLI reference](https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-test-mtp),
and [Python 3.14 unittest behavior](https://docs.python.org/3.14/library/unittest.html).

## Migration and activation limits

Re-run adoption to replace byte-exact historical Praetor Makefiles, including the
old echo-only `verify-all` stub. Custom or edited Makefiles are preserved even with
`--force`; includes, generated target names and pattern rules are treated as
ambiguous ownership. A missing target can be appended to a simple existing
Makefile without replacing its recipes. Existing AGENTS.md is preserved by default
with a command-synchronization warning. Review the declared plan; `--force`
regenerates a recognized harness and keeps its preamble, the repository's own invariant
rows and the instructions below it
([adoption](../adoption.md#what-adoption-reads-before-it-writes)).
Malformed or oversized command metadata now fails before any adoption writes.

`lefthook.yml` follows the same rule (`internal/adopt/lefthook_identity.go`):

- An exact earlier Praetor rendering is replaced by the current one and activated,
  `--force` or not. The recognised renderings are listed by SHA-256 of their LF text in
  `priorLefthookDigests`; `internal/adopt/testdata/lefthook/` reproduces each one. A CRLF
  checkout of one (`core.autocrlf` on Windows) is recognised too and replaced by the current
  rendering's LF bytes, the only bytes activation trusts
  (`TestAdopt_Positive_CRLFPriorLefthookMigratedAndActivated`). An edited copy, or one with
  mixed line endings, is not exact: it is preserved and not activated.
- A CRLF checkout of the current rendering is verified and kept as it is. Activation trusts
  only the rendering's exact LF bytes, so the file is not activated; `--force` rewrites it
  with those bytes, reports a reconcile rather than a replace, and activates it
  (`TestAdopt_Positive_ForceRewritesCRLFCurrentLefthookAndActivates`,
  `TestAdopt_Boundary_CRLFCurrentLefthookKeptWithoutForceAndInDryRun`).
- Every other configuration is the repository's and is never replaced, `--force` included,
  nor activated: the audit checks only that `lefthook.yml` exists. That covers a copy with
  mixed line endings, which is no checkout, a file that does not parse, and a file behind a
  symlink, whose target is left as it is
  (`TestAdopt_Negative_ForeignLefthookKeptWithAndWithoutForce`,
  `TestAdopt_Negative_ForceKeepsMixedEndingCurrentLefthook`,
  `TestAdopt_Boundary_ForceKeepsSymlinkedLefthook`). The skip reason names the generated jobs
  the file lacks and the jobs it adds, whether they are declared in `commands` or `scripts`
  maps or in a `jobs` list; the checkpoint jobs count as neither. Beside a kept file only
  absent checkpoint files are installed. To regenerate it, remove `lefthook.yml` and re-run
  adopt.
- A configuration that reaches `.config/lefthook/praetor.yml`, the vendorable canonical
  policy, through `extends` or through the `configs` of a `remotes` entry is kept the same
  way, and so is `.config/agent/hooks/block_evasion.py` beside it, so the policy and its
  scripts stay one version. The skip names the policy; update it as
  [`.config/lefthook/README.md`](https://github.com/cordanaLLM/praetor/blob/main/.config/lefthook/README.md)
  describes.

The generated hooks resolve `praetorctl` or, failing that, `standardsctl` through the
same expression as the generated Makefile's `PRAETORCTL` variable
(`util.ShellCLIResolution` in `internal/util/clinames.go`). A missing binary blocks the
commit or push; a `./cmd/standardsctl` source tree no longer stands in for one. The language
jobs follow the languages the plan detects: `gofmt`, `govet` and `security` for a root
`go.mod`, `rustfmt` and `clippy` for a root `Cargo.toml`, all of them when no language is
detected ([the lefthook.yml adoption writes](git-hooks.md#the-lefthookyml-adoption-writes)).
Each runs only where the root holds its marker and otherwise prints
`no <marker> at the repository root, skipping <tool>`, matching the gate stages above. A module
kept in a subdirectory is not scanned by these jobs. The `gate` job runs
`praetorctl gate run --path=.`, which fails where no toolchain stage ran for a `go.mod` or a
`Cargo.lock` ([no receipt](#no-receipt-when-no-toolchain-stage-ran)); the file's header comment
says so, and every earlier rendering, the Go-only ones included, is recognised as earlier
Praetor output. Tests: `internal/adopt/lefthook_identity_test.go`,
`internal/adopt/lefthook_languages_test.go`, `internal/adopt/lefthook_keep_test.go`,
`internal/adopt/checkpoint_test.go`, `internal/adopt/hooks_gomod_test.go`,
`internal/adopt/cli_name_test.go`.
Generated Makefiles run `caveman-sources` (`praetorctl caveman check
--configured-sources`) inside `verify-all`, and `praetorctl audit` fails when
`.standards.yaml` has no `register.sources`. Re-running adoption on a repository
with an existing manifest adds the contract for its Paperclip harness without
replacing operator fields, and binds a valid operator-owned harness byte for byte
(`TestAdoptExistingManifestAddsSourceContractWithoutDroppingContent` and
`TestAdoptCustomHarnessPreservesBytesAndBindsActualCoverage` in
`internal/adopt/adopt_test.go`). A harness still byte-identical to an earlier
release's output is refreshed to the current text first
(`TestAdoptUpgradesReleasedHarnessToPassingSourceGate`), including a CRLF checkout
(`TestAdoptUpgradesCRLFReleasedHarness`). A declined paperclip step writes no
harness and binds no contract to one it does not write
(`TestAdoptDeclinedPaperclipWithoutHarnessBindsNothing` in
`internal/adopt/harness_plan_test.go`). `--force` regenerates the
harness and re-binds an existing contract, keeping every declared input
(`TestAdoptForceRebindsExtendedSourceContract` in
`internal/adopt/manifest_sources_test.go`). An existing `register.sources` that
fails its own gate stops adoption in both modes
(`TestAdoptRejectsStaleExistingSourceContract`,
`TestAdoptForceRefusesDriftedSourceContract`). The
[text-register guide](text-register.md#upgrading-an-adopted-repository) describes the
upgrade path.

Run the resulting commands under the intended toolchain and retain actual results
before claiming application verification. Public dogfood governance verification,
HISS language coverage, native tests, and deployment smoke tests remain distinct
pieces of evidence. Repository enrollment and external application execution are
not activated by this plan.

This correction does not repair every language-specific consumer of the archetype
catalog. DevContainer generation, audit and adoption already resolve the selected
catalog's pinned feature union (`internal/devcontainer/devcontainer.go`): a
non-Go or empty catalog no longer receives an unconditional Go feature, and a
feature conflict fails instead of silently picking one side. Editor generation has
not reached the same parity: some editor generators still accept an `arch`
argument and discard it, so the emitted `.clang-tidy` stays C/C++-only regardless
of the adopted archetype (`internal/editor/editor.go`, `generateVisualStudio` and
neighboring generators). Archetype catalogs and this downstream consumer still
require separate reconciliation; a C# command plan does not imply a configured
.NET development container or complete C# scanning.

## What the generated harness claims

The `AGENTS.md` harness states only what adoption generated. Its source is
[`internal/adopt/harness.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/harness.go); the tests are in
[`internal/adopt/harness_truth_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/harness_truth_test.go).

- **Title.** `# <owner>/<name> Agent Operating Harness`, from the origin remote, or from the
  manifest's `repository.owner` and `repository.name` when no remote resolves. With neither, the
  title names the checkout directory alone (`TestHarnessTitleNamesOwner`).
- **Turn-end command.** The block under "Before concluding any turn" names `make verify-all`
  only when the repository has that target after the run: the one adoption generates, or a
  repository-owned one it preserved. When `adoption.decline` lists `makefile` and no custom
  target exists, the block names `praetorctl compile-context --verify`,
  `praetorctl caveman check --configured-sources` and `praetorctl audit` instead, and the footer
  drops its `make verify-all` line (`TestHarnessEntrypointFollowsVerifyAll`).
- **Verification line.** It calls `make verify-all` the repository's own gate and points at the
  `Makefile` for its steps, without listing them: the repository owns the target and may change
  it after adoption, so a restated recipe would drift from what runs. Per plan status it adds
  that adoption generated the target and executed none of it (`declared-unverified`), kept a
  repository-owned target unread (`preserved-unverified`), or wrote one that fails until the
  project declares build and test commands (`unavailable`). When `adoption.decline` lists
  `makefile`, it says no target exists and to run the gates directly
  (`TestHarnessSummaryDescribesVerifyAllAsRepositoryGate`). A preserved custom target keeps the
  commands the project markers declare in the report's `verification.declared` list, and the
  harness footer lists them, so `--force` no longer reduces them to `make verify-all`
  (`TestAdoptForceKeepsOwnerAndDeclaredCommands`).
- **Receipt.** Only `praetorctl gate run` mints an Ed25519 Exit-0 receipt. The harness says so,
  tells agents to report no receipt that command did not mint, and ties none to `verify-all`
  (`TestHarnessMakesNoUnbackedClaims`).
- **Server side.** Adoption installs no CI job that runs a `praetorctl` gate; those gates run in
  the local hooks and targets only. Rule 5 says so and names each CI workflow the run does
  scaffold, with what it runs as read from the workflow body
  ([`internal/adopt/harness_ci.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/harness_ci.go),
  `forge.WorkflowRuns`):
  - `.github/workflows/praetor-docs.yml`, which runs the Markdown verification, when the
    `docs:seo-portal` facet is declared;
  - the detected flavor's workflows that flavor apply leaves as its own rendering
    (`flavor.PlannedWorkflows`), such as a Go flavor's `ci.yml` running `go vet ./...` and
    `go test -race ./...`, or a Rust flavor's running `cargo fmt`, `cargo clippy` and
    `cargo test`. A workflow the repository already owns, and every flavor workflow when
    `adoption.decline` lists `working-dir-and-flavor`, is not named.

  A one-line `run:` step is named by its command. A multi-line script is named by its step
  name, or its first line when it has none, and labelled `step` so it never reads as a command
  to run. Without a scaffolded workflow, rule 5 says the gates run locally only
  (`TestScaffoldedWorkflowsReadWhatAdoptionWrites`, `TestCIClaimFollowsWhatWorkflowsRun`).
  `TestNoScaffoldedWorkflowRunsPraetor` checks every line of every step in every flavor workflow
  and the documentation workflow for a `praetorctl` call; should one ever run a gate, rule 5
  drops its "no server-side `praetorctl` gate run" sentence.
- **Invariant table.** One row per rule in the HISS rule catalog
  ([`internal/hisscatalog/catalog.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/catalog.go)), HISS-01 through
  HISS-21 (`TestHarnessTableListsEveryRegisteredInvariant`). The rows have the shape
  `hisscatalog.ParseGatedInvariants` reads, the parser the generated wiki uses for praetor's own
  table (`TestHarnessDirectives_ParseGatedInvariants`). The `Rule` column is the catalog's
  adopted directive for this repository
  ([`internal/hisscatalog/directive.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/directive.go)):
  - A clause that names one language's construct renders only where the verification plan
    found that language, labelled with it: Go's `context.Context` deadline and unchecked
    `error` returns, Rust's `.unwrap()` / `.expect()` ban, C and C++'s `goto` and banned libc
    calls, the `// SAFETY:` proof for Go and Rust `unsafe`. A rule left with no clause, such as
    HISS-09 in a TypeScript repository, says it has no analogue there. With no detected
    runtime or source language, every clause renders with its label.
  - C/C++ (one language for the clauses, labelled `C/C++`) is detected from a native build
    marker (`meson.build`, `core/meson.build`, `CMakeLists.txt`) or from C/C++ sources, so a
    repository that compiles C or C++ from a `Makefile`, Bazel or a script still reads the
    C/C++ clauses. The verification walk records the file names it already visits, under the
    same `--verification-max-*` bounds, and the plan lists them as `source_languages: ["c"]` in
    the JSON report, also when a build marker declares the language too. Every extension the
    audit's native scan reads (`.c`, `.cpp`, `.cc`, `.cxx`, `.hpp`, `.cu`, `.hip`, in any case;
    `hiss.IsNativeExtension`) makes the repository C/C++, the same files the scan runs the
    `goto` and banned-libc checks on. A `.h` file does too, unless an Objective-C source (`.m`,
    `.mm`) sits beside it: `.h` is Objective-C's header as well, and the audit does not scan
    Objective-C. Files under a directory the scan ignores (`vendor`, `third_party`, `testdata`,
    build output, and the Zig trees `zig-pkg`, `zig-out` and `.zig-cache`;
    `hiss.ShouldIgnorePath`) never count. In a work tree a file must also be one
    git reports as the repository's own: tracked, or untracked and not ignored
    (`hiss.GitVisiblePaths`, the scan's own listing, asked once and only when the walk saw a
    C/C++ or Objective-C file). An in-place Cython `.c` a `*.c` rule ignores, or C under ignored
    IDE build output, therefore selects no clause, and a fresh clone and a built checkout render
    the same table. Outside a work tree git gives no answer and every walked file counts. The
    walk visits a subset of what the scan reads: it also skips `bin`, `obj`, `dist` and
    `__pycache__`, which the scan enters, so C/C++ sources found only there select no clause
    ([`internal/adopt/verification_sources.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/verification_sources.go),
    `TestCSourceObservationDecidesC`, `TestRepositoryHISSFactsDetectsCSources`,
    `TestCSourceDetectionFollowsTheAuditScan`, `TestRepositoryHISSFactsReadsOnlyGitVisibleCSources`,
    `TestGitVisiblePathsAnswersTheScanScope`, `TestIsNativeExtensionMatchesTheScanDispatch`,
    `TestCSourceDetectionStaysInsideTheWalkBounds`, `TestAdoptedHarnessRendersCClausesFromSources`).
  - A Zig build (`build.zig`) is a runtime no clause names. It selects no C/C++ clause by
    itself, since the build script is a program that declares no language; the C or C++
    sources it compiles do, as above. A Cargo workspace built with `zig build` whose crates
    carry C shims therefore states the Rust and C/C++ clauses and no Go clause, and a Zig-only
    repository is a known language set in which no labelled clause renders
    (`TestAdoptedHarnessOfZigBuiltWorkspace`).
  - HISS-04 states the function length the repository's audit enforces, read from the policy
    the policy-catalog step resolved (container-image, for example, enforces 50). At the 60-line
    audit ceiling it adds `(audit ceiling)`: a pinned profile snapshot such as
    `native-gpu-systems` states 75, and the audit-compatibility layer caps it at 60
    ([effective policy](effective-policy.md#consequence-of-the-60-line-default)). The ceiling
    comes from `config.AuditMaxFuncLOC`; the catalog keeps no copy of it
    (`TestAdoptHarnessesStateTheAuditFunctionLength`, `TestFuncLOCLimit`).
  - A C or C++ repository that declares `hiss.exceptions.c_goto_cleanup` and carries the
    document it names reads that exception in HISS-01 instead of the zero-`goto` clause: the
    exact rule the audit's native scan applies, rendered from the scan's own text
    (`hiss.CleanupGotoRule`), so the harness grants nothing the audit rejects. Go's own ban
    stays. Without the document the ban stays, the scan reports every `goto` and the report
    warns ([declared HISS exceptions](../adoption.md#what-adoption-reads-before-it-writes),
    `TestAdoptHonoursDocumentedCleanupGotoException`, `TestCleanupGotoExceptionReachesTheAudit`).
  - `TestAdoptedHarnessGolden` pins the whole harness for a Go framework, a Rust crate, a
    native C engine and one declaring the cleanup-`goto` exception
    ([`internal/adopt/testdata`](https://github.com/cordanaLLM/praetor/tree/main/internal/adopt/testdata));
    regenerate with `go test ./internal/adopt -run TestAdoptedHarnessGolden
    -update-harness-golden` after reviewing the change.

  The `Adopted check` column names the
  check and the generated stages that run it, such as `praetorctl audit` HISS scan in verify-all +
  lefthook pre-commit/pre-push, qualified by language, or `not enforced`. It credits only the
  pipelines this run generates ([`internal/adopt/harness_pipelines.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/harness_pipelines.go)):
  - the `verify-all` target, unless `adoption.decline` lists `makefile` or the plan is
    `preserved-unverified`;
  - `lefthook.yml`, unless `adoption.decline` lists `git-hooks`, the git-hooks step will keep a
    file that is not praetor's rendering, or lefthook will not install its hooks. The step keeps
    every existing file that is neither a current nor an earlier Praetor rendering, `--force`
    included. The hooks are installed only when `lefthook version` runs on `PATH`
    and hook activation is not skipped. Without a runnable lefthook, adoption writes the
    fallback pre-commit hook, which runs `compile-context --verify` and `audit --offline` alone,
    so the lefthook stages are not credited.

  A check is also language-bound (`Rule.AdoptedFor` in
  [`internal/hisscatalog/adopted.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/adopted.go)):
  the harness credits the audit's HISS scan in Go, Rust, Python and C/C++ sources only, and each
  rule's scan decides a subset of them. The scan also reads JavaScript, TypeScript and Svelte
  ([`internal/hiss/script.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/script.go)),
  but no directive clause names those languages yet, so their rows understate what the audit
  checks rather than overstate it. A row reads `not enforced` where the check decides none of the
  repository's languages or the rule has no analogue there. HISS-09 in a C engine, HISS-01 in a
  TypeScript repository and the lefthook `go vet` row in a Rust crate are examples
  (`TestAdoptedFor`).

  The line above the table names the pipelines it read and says the cells describe the files as
  adoption wrote them; a later edit to the `Makefile` or `lefthook.yml` is not reflected there.
  Rule 5 claims hooks only when lefthook
  installs praetor's `lefthook.yml`, and says the file's hooks stay inactive when lefthook did not
  run (`TestHarnessTableFollowsGeneratedPipelines`,
  `TestAdoptHarnessDropsDeclinedAndPreservedPipelines`). `TestGeneratedPipelinesPredictGitHooks`
  replays the prediction against the git-hooks step for each case, including a failing and a
  missing lefthook binary and skipped activation, and checks which pre-commit hook it installed.
- **Protected files.** Rule 3 names the files `compile-context` writes under the manifest's
  `agent_clients`, read from `agentcontext.VendorTargets`, the selection the transpiler itself
  applies: all six by default, `.gemini/GEMINI.md` and `.codex/rules.md` included, and none for
  an empty selection (`TestHarnessRule3NamesExactlyTheCompiledFiles`,
  `TestHarnessNamesEveryProtectedContextFile`, `TestHarnessRule3FollowsAgentClients`).
- **Paperclip harness.** `.paperclip/harness.json` and `.paperclip/rules.md` come from
  [`internal/paperclip/harness.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/paperclip/harness.go).
  - The invariants are the same adopted directives for HISS-01, 02, 04, 07, 10, 15 and 16, for
    the repository's languages and declared exceptions
    (`TestSynthesizeHarness_Positive_InvariantsFollowLanguages`,
    `TestSynthesizeHarness_Positive_ExceptionAndLimitFollowFacts`). Adoption binds the harness
    in `register.sources` during its manifest step, before the policy-catalog step runs, so it
    resolves the policy the planned manifest and lock produce, with the loader and audit layer
    `praetorctl audit` uses (`resolvePlannedPolicy` in
    [`internal/adopt/policy_dryrun.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/policy_dryrun.go)),
    and HISS-04 states the same function length as `AGENTS.md`. `praetorctl paperclip harness`
    reads the same facts through `adopt.RepositoryHISSFacts`, which resolves the length with
    `config.ResolveRepositoryPolicy`, and writes the same bytes. Only where no policy resolves
    yet, such as a first adoption without `--lock-source-root` or a manifest without a lock,
    does HISS-04 state the 60-line ceiling and that a stricter repository policy wins.
  - The receipt row prescribes attaching receipts only when `.standards.yaml` pins a well-formed
    `receipt.public_key`. Without one, every attached receipt is refused, so the row says to
    attach none and names `praetorctl gate keygen`
    (`TestSynthesizeHarness_Positive_PinnedKeyPrescribesReceipts`,
    `TestSynthesizeHarness_Negative_NoPinnedKeyPrescribesNoReceipt`,
    `TestSynthesizeHarness_Boundary_ReceiptKeyShape`).
  - With the key pinned, the row still promises no receipt per proposal. It says only
    `praetorctl gate run` without `--dry-run` mints one, to attach a minted receipt, and to
    report none that was not minted, the Paperclip counterpart of the `AGENTS.md` receipt line.
    A dry run never mints (`runReceiptStage` in
    [`internal/gating/pipeline.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/gating/pipeline.go)),
    and a repository-owned pre-push hook may pass `--dry-run`. A missing `go.mod` does not
    change the row: the gate's Go stages then report `not_applicable` and the receipt stage
    still signs (`TestSynthesizeHarness_Positive_GoModuleKeepsReceiptRule`,
    `TestSynthesizeHarness_Negative_NoGoModPromisesNoUnconditionalReceipt`,
    `TestSynthesizeHarness_Boundary_ReceiptRuleForms`).
  - An unmodified harness from an earlier release, including the ones that prescribed receipts
    on every repository or on every pinned one, still counts as earlier output, so
    `praetorctl adopt` refreshes it to the current receipt row without `--force`; an edited
    harness stays operator-owned (`TestPriorGeneratedRecognisesTheCavemanRelease`,
    `TestAdoptRefreshesUnconditionalReceiptRows`).
  - The same holds for this release's own harness after a repository fact it reads changes.
    Pinning `receipt.public_key`, as the unpinned row advises, adding or removing a language,
    declaring or withdrawing an exception, or a policy that resolves the function length the
    harness stated as the unresolved ceiling leaves the harness unmodified output, and the next
    plain `praetorctl adopt` refreshes it. The refresh key compares the harness byte for byte
    with this release rendered under both receipt rows and each earlier pinned receipt row
    (`priorPinnedReceiptRows`), every language set
    (`hisscatalog.AllLanguages`), every exception set (`hisscatalog.AllExceptions`) and the
    HISS-04 statements it accepts: the 60-line ceiling unresolved, the ceiling resolved, and
    the plain number this run states, if any. So an edit to the receipt row or an invariant
    still keeps it operator-owned (`TestAdoptRefreshesHarnessAfterFactsChange`,
    `TestAdoptKeepsHandEditedHarnessAfterFactsChange`, `TestAdoptHarnessRefreshFactBoundary`,
    `TestPriorGeneratedRecognisesThisReleaseUnderEveryFactCombination`).
  - The function length is never read from the harness on disk, since any number found there
    could be an operator's edit (`TestPriorGeneratedKeepsEditedFactRows`, `TestLimitFacts`).
    The cost: once a policy has resolved the length, a later move of it (50 to 45, 50 up to
    the ceiling, or back to unresolved) leaves a harness stating the old plain number
    operator-owned, under `--force` too. Delete `.paperclip/harness.json` and rerun
    `praetorctl adopt` to regenerate it
    (`TestPriorGeneratedKeepsHarnessAfterResolvedLimitMoves`).
  - Still open: the `## AGit Push Protocol` section (`agit_push_format`) prescribes
    `git push origin HEAD:refs/for/main -o topic=<issue-id>` whatever forge `origin` names. That
    push opens a review only on a forge that implements AGit, such as Forgejo or Gitea. The
    manifest declares no forge kind, and choosing a push form from the remote's host name would
    be a guess, so the harness does not choose one. On any other forge, push the review branch
    (the second half of the command) and open the pull request there.

An existing harness that is neither the current synthesis nor unmodified earlier output is
operator-owned and keeps its text, under `--force` too
(`TestAdoptForceKeepsReboundEditedOperatingContract`). A declared `register.sources` is never
re-bound to an edited harness, so recompute its pins after an edit
([text register](text-register.md#upgrading-an-adopted-repository)). `--force` sets only a
`platform` that names another repository than the identity, the value the audit's Paperclip
gate compares. It replaces that value in place (`clientjson.ReplaceMember`), so every other byte
stays, layout, line endings and the `\u003c`-style escapes of a released harness included, and
the reported delta is that one line; a plain run keeps the file and warns
(`TestAdoptForcePatchesOnlyHarnessPlatform`,
`TestPatchPlatform_Positive_ReleasedHarnessChangesOneLine`). To regenerate the harness, delete
`.paperclip/harness.json` and rerun `praetorctl adopt`. A harness written where none existed is
Praetor output, so a contract whose every input selects it is bound to the written bytes, pins
an earlier release bound included (`TestAdoptDeletedHarnessRebindsPinsAcrossReleases`). A
`.paperclip/rules.md` you deleted stays deleted, under `--force` too
(`TestAdoptForceKeepsDeletedRulesAbsent`).

## Canonical context preparation

Adoption preserves repository-specific `AGENTS.md` instructions when composing
the Praetor harness. The combined canonical content must fit every generated
client projection's 300-line budget. Context compilation is preflighted before
writing the composed canonical file, so a budget failure leaves canonical and
vendor context files unchanged. Other adoption stages may already have run;
this check does not make the entire adoption operation transactional.

An oversized composition remains an explicit preparation failure. Review and
condense the repository's instructions before retrying; adoption does not silently
discard them or raise the limit. A dry-run reports the same compilation failure
without writing context files.

## Generated Markdown and markdownlint

Every Markdown file adoption writes passes markdownlint's default configuration, so an
adopter whose own lint runs the defaults does not fail on Praetor output. Where a rule
has to be disabled, the disable covers only the lines that need it and ends before the
repository's own text.

| File | Rules disabled |
| :--- | :--- |
| `CONTRIBUTING.md`, `.github/pull_request_template.md`, `SECURITY.md`, `docs/adr/README.md`, `docs/adr/0000-template.md` | none; lines are wrapped within 80 columns |
| `.agents/agents/repo-auditor.md`, `.agents/agents/repo-gatekeeper.md` | none |
| `.paperclip/rules.md` | none, except MD013 around a push command too long to wrap; contract lines are wrapped within 80 columns |
| `AGENTS.md` | MD013 from the first line to `<!-- praetor:harness:end -->`, for the harness table; MD025 after it, because the repository's instructions open with their own H1 |

Files adopted before this change open with a file-wide `<!-- markdownlint-disable MD013 -->`
(an older harness disables MD013 and MD025 together). Adoption with `--force` rewrites the
personas and refreshes the `AGENTS.md` harness while keeping the repository's own
instructions. `.paperclip/` is refreshed, with or without `--force`, only while it is
unmodified earlier output; delete an edited one and rerun adoption to regenerate it. It
never rewrites an existing `CONTRIBUTING.md`, pull request template, `SECURITY.md` or
`docs/adr/`: those belong to the repository once written (`internal/adopt/governance.go`),
so delete their disable line by hand and wrap the lines it covered.

`internal/adopt/generated_markdown_test.go` and
`internal/paperclip/rules_markdown_test.go` check each file with
`testsupport.MarkdownFindings` (`internal/testsupport/markdown.go`), a Go subset of the
markdownlint rules these generators must hold.
