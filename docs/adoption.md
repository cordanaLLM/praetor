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

# Regenerate drifted audit-locked files & record technical debt
praetorctl adopt --force --record-baseline --lock-source-root=/path/to/praetor

# Change the declared profile or facets later, or re-pin to a newer catalog
praetorctl profile set os-image --lock-source-root=/path/to/praetor --dry-run
```

`--force` refreshes rather than resets: what it rewrites, merges and keeps is in
[What a forced re-adoption changes](#what-a-forced-re-adoption-changes).

`--facets` names the facets adoption writes when it creates `.standards.yaml`. Omitted, adoption
writes `security:high`, `api:public-contract`, `docs:seo-portal` and `agent:sandboxed`
(`config.DefaultFacets` in `internal/config/facets.go`). An existing `.standards.yaml` keeps the
facets it declares, none or an empty list included, and adoption ignores `--facets` for it
(`TestManifestForLock_FacetsScaffoldOnlyANewManifest_3D` in `internal/adopt/paths_read_test.go`).
`adopt --help` says both (`TestAdoptHelpStatesTheDefaultFacets` in
`cmd/standardsctl/profile_test.go`). To change the facets of an adopted repository, see
[Changing the profile, facets or catalog](#changing-the-profile-facets-or-catalog).

The report's `Facets:` line shows the facets the run applies: the declared ones when
`.standards.yaml` exists, whatever `--facets` names (`TestAdopt_Positive_ReportsTheDeclaredFacets`
in `internal/adopt/facet_selection_test.go`). A `--facets` that differs from them, `--facets=`
included, adds a warning naming both lists and the `praetorctl profile set --facets=...` command
that changes them (`TestAdopt_Negative_WarnsAboutIgnoredFacets`). An overridden `--profile` warns
the same way and names `praetorctl profile set <profile>`.

When a first adoption falls back to the default facets, notes under `Facets:` say so, say whether
`--facets` was omitted or given empty (`--facets=` also declares the defaults), and name what each
default facet raises over the detected profile alone in `branch_protection` and `supply_chain`,
read from the pinned catalog (`config.EffectivePolicy.FacetEffects` in
`internal/config/facet_effects.go`). For an empty repository adopted as `template-seed`:

```text
Facets:             [security:high api:public-contract docs:seo-portal agent:sandboxed]
  default facets: --facets was omitted, so adoption declares security:high, api:public-contract, docs:seo-portal, agent:sandboxed in the .standards.yaml it creates
  security:high raises over template-seed alone: branch_protection.required_approving_reviewers 1 -> 2, branch_protection.require_signed_commits false -> true, supply_chain.slsa_level 1 -> 3, supply_chain.enforce_cosign false -> true, supply_chain.require_sbom false -> true
  api:public-contract raises over template-seed alone: branch_protection.required_approving_reviewers 1 -> 2, branch_protection.require_signed_commits false -> true
  docs:seo-portal raises over template-seed alone: no branch-protection or supply-chain setting
  agent:sandboxed raises over template-seed alone: no branch-protection or supply-chain setting
  choose other facets with --facets=<id>,... on a first adoption, or afterwards with praetorctl profile set --facets=<id>,... --lock-source-root=<praetor checkout> (--facets= declares none)
```

`TestFacetEffects_Positive_ShippedDefaultsOverTemplateSeed` in
`internal/config/facet_effects_test.go` pins these values against the shipped catalog. A run that
resolved no pinned policy, such as `--dry-run --record-baseline=false` without
`--lock-source-root`, says the effects are not listed instead
(`TestAdopt_Boundary_EmptyFacetsFlagAndNoPolicy`). The report's JSON carries the same facts as
`facet_origin` (`declared`, `requested` or `default`) and `facet_notes`, and the MCP
`standards_adopt` tool prints each note as a `facet note:` line
(`TestFormatAdoptMCPResultPrintsFacetNotes`).

### Dry-run ruleset preview

`adopt --dry-run` writes nothing. For the branch protection ruleset
(`.github/rulesets/main.json`), it prints what a real run would do and what the file would contain
(`AdoptReport.Previews`, `internal/adopt/preview.go`). The MCP `standards_adopt` tool prints the
same preview text (`adopt.FilePreview.Text`, `TestFormatAdoptMCPResultPrintsPreviews`):

| Action | Meaning | Printed |
| :--- | :--- | :--- |
| `create` | no ruleset yet; the run writes one | the rendered ruleset |
| `update` | the run replaces the ruleset: the rendering current before the run, which it refreshes, or any differing ruleset under `--force` while the policy requires one | unified diff from the file on disk to the rendering |
| `unchanged` | the ruleset already is the rendering, line endings aside | nothing more |
| `keep` | the ruleset differs and stays: `--force` was not passed, or the policy requires neither linear history nor signed commits | the diff regenerating it would apply |

The preview takes its action from the same keep-or-replace decision as the real run
(`internal/adopt/preview_test.go`). Its status checks come from the workflows the run leaves
before its `branch-ruleset` step, not only the ones on disk:

- workflows the dry run records as written, replaced under `--force` or removed, such as the
  documentation gate's (`TestReplaceExisting_3D_DryRunPlansTheReplacedBytes` in
  `internal/adopt/replace_test.go`);
- the workflows of the flavor the run applies for the profile it records
  (`flavor.PlannedWorkflows`), which a dry run plans but does not write.

On a first adoption the preview is therefore the file the run writes, byte for byte
(`TestAdoptDryRun_Positive_FirstAdoptionPreviewIsTheWrittenRuleset`).

### What a dry run lists

`adopt --dry-run`, `--force` included, lists every path the real run writes, with the action the
real run records, under the same effective policy (`AdoptReport.EffectivePolicy`):

- the flavor templates, which the flavor layer plans through the decisions an apply makes,
  writing nothing (`flavor.ApplyOptions.DryRun`, `internal/flavor/dry_run_test.go`);
- each pinned catalog file below `.config/archetypes/`;
- the persona copies of the canonical personas the run writes
  (`compiler.PlanAgentSurfacesOver`);
- the pre-commit hook that hook activation installs below `.git/hooks`. Only running
  `lefthook install` shows whether it succeeds; a failed one installs the fallback hook at the
  same path. A directory git does not know fails activation in both runs;
- the ADR template and `.paperclip/rules.md` under their own paths. An existing one beside a
  missing ADR index or harness is kept, not overwritten;
- an existing file the run overwrites, as replaced (`Planned Replacements`), never as created.

`internal/adopt/dryrun_parity_test.go` runs a dry run and a real run on two copies of one
fixture, a first adoption and a forced re-adoption among them. It fails when either report has
an entry the other lacks, when the real run writes a file no planned path names, when a report
lists an existing file as created, or when the effective policies differ.

### Which jobs the ruleset requires

Adoption, flavor apply, `sync` and the audit take the ruleset's required status checks from one
function, `forge.RequiredStatusContexts` (`internal/forge/workflow_checks.go`). A job's check is
required when the job reports on every pull request:

- Its workflow triggers on `pull_request` without `paths` or `paths-ignore`. A filtered workflow
  does not run on every pull request, and a required check that never reports leaves the pull
  request waiting forever.
- The job has no `if:`, or a condition that holds on every run: `always()`,
  `success() || failure()` or `!cancelled()`, bare or as one `${{ }}` expression. A disjunction
  containing `github.event_name != 'schedule'`, which holds on every pull request run, counts
  too (`TestHoldsOnEveryPullRequestRun` in `internal/forge/required_contexts_in_test.go`). So does
  a repository guard that holds for the manifest's identity
  (see [Which workflows run where](guides/operational-sync.md#which-workflows-run-where)).
- A condition that leads with the conjunct
  `!(startsWith(github.head_ref, 'renovate/') && github.event.pull_request.user.login == 'renovate[bot]')`
  is judged by the rest alone: the rest must be one parenthesised group or hold no top-level `||`
  (`TestRenovateBranchSkipKeepsAJobRequired` in `internal/forge/workflow_guard_test.go`). Only
  that exact term counts; a looser skip, such as the head branch test alone, makes the job
  optional. The term skips pull requests the Renovate app opened from a `renovate/` branch only,
  and requiring the job keeps every other pull request protected. The engine's heavy jobs use it because its landing pipeline takes Renovate pull
  requests over ([Renovate pull requests](guides/contributing.md#renovate-pull-requests)). A
  repository without such a takeover should not use it: a skipped job reports success, so
  nothing then verifies the Renovate pull request it merges.
- The job is not advisory: `continue-on-error` is absent or `false`.

Any other condition makes the job optional, a status function joined with anything else
(`always() && ...`) included. GitHub reports a job its condition skipped as successful, so a lane
that runs only when a planner job selects it would pass as a required check whether or not its
work ran.

A repository guard is judged against the manifest identity. An operational fork resolves that
identity to its `repository.source`, so the ruleset the fork commits equals the canonical one.
On the fork's forge the guard is false, and a guarded matrix job is skipped before its legs
exist, so none of their checks is ever reported. `sync --remote` therefore adds only the checks
whose jobs report in `<repository.owner>/<repository.name>` (`forge.RequiredStatusContextsIn`)
and prints the ones it left off (`TestSync_Remote_RequiresOnlyChecksThatReportInTheRepository`).
It never removes a check the live ruleset already requires, so it warns about a left-off check
an earlier sync added; remove that check from the live ruleset by hand
(`TestSync_Remote_WarnsAboutLeftOffChecksTheLiveRulesetStillRequires`).
The Platform Neutrality matrix is the case in point
([HISS-21](standards/hiss-21-platform-neutrality.md#outside-the-canonical-repository-the-matrix-is-opt-in-and-says-so)).

Path-filtered CI therefore gets its protection from an aggregate job: it `needs` every lane, runs
with `if: always()`, and fails when a job it needs failed or was cancelled. The ruleset requires
that aggregate beside the unconditional planner, never the gated lanes
(`internal/forge/workflow_aggregate_test.go`). The aggregate is only as strict as its own steps.

A repository with one maintainer declares `review_mode: single_maintainer` under
`overrides.branch_protection` ([review policy](guides/review-policy.md)). Adoption, `sync` and the audit
render the ruleset from the same effective policy, so the file adoption writes, with zero
approvals, no code-owner review and the aggregate required, is the one the audit accepts
(`TestAdopt_Positive_SoloPathFilteredRulesetIsMergeable` in `internal/adopt/ruleset_solo_test.go`).

### Refreshing a ruleset Praetor rendered earlier

The ruleset is rendered from the effective policy and the workflows present, so it goes stale when
either changes. `flavor apply` before adoption renders it under the built-in policy; the adoption
that follows pins an archetype policy and adds the documentation gate.

Each run reads the repository's policy and workflows before it writes anything
(`forge.ReadRulesetBaseline`). The ruleset rendered from that state is the one sync and the audit
accepted before the run. A ruleset that is exactly that rendering, line endings aside, is
refreshed without `--force` (`forge.PriorRulesetDigests`, looked up through
`util.LookupCanonicalText`). Nothing is read back from the file itself.

| Ruleset on disk | Without `--force` | Test |
| :--- | :--- | :--- |
| the current rendering, LF or CRLF | verified, left as is | `TestApplyFlavor_Boundary_CurrentRenderingIsUnchangedInEitherLineEnding` |
| the rendering current before this run, LF or CRLF | refreshed, in its own line endings | `TestAdopt_Positive_RefreshesTheRulesetFlavorApplyWrote`, `TestAdopt_Positive_FlavorApplyAfterAdoptionRefreshesTheRuleset` |
| a rendering with one value edited: review count, signature rule, status check | kept and reported | `TestAdopt_Negative_ValueEditAfterFlavorApplyIsKept` |
| anything else: another name, layout, a final newline | kept and reported | `TestAdopt_Negative_EditedRenderingIsKept` |

`--force` replaces a kept ruleset only while the policy enforces linear history or signed
commits: only then does the audit compare the file (`rulesetRequired` in
`internal/adopt/ruleset_audit.go`). Under a policy requiring neither, `--force` keeps it too
(`TestReconcileBranchRuleset_Boundary_ForceReplacesOnlyWhilePolicyRequiresIt`).

A ruleset rendered for a policy the repository no longer declares is kept too, for example after
`.standards.yaml` overrides change. `praetorctl sync` then reports it as drift. Pass `--force` to
adoption or `flavor apply`, or delete the file and run `sync`, which writes the missing ruleset.
Keep a hand-managed ruleset with `adoption.decline: [branch-ruleset]`.

### Protected default branch

The ruleset protects the repository's default branch and every `lts-*` branch
(`forge.RepositoryRulesetRefs`). Adoption, `flavor apply`, `sync` (local and `--remote`) and the
audit resolve that branch the same way (`forge.RepositoryDefaultBranch`); the first source that
names one wins:

| Source | Written by | Applies |
| :--- | :--- | :--- |
| `repository.default_branch` in `.standards.yaml` | the operator, or the command that creates the manifest (below) | whenever it is set |
| `refs/remotes/origin/HEAD` in the checkout | `git clone`, `git remote set-head origin --auto` | no declaration |
| `main` | built in | neither |

```yaml
repository:
  default_branch: master
```

Resolving the branch asks nothing of the forge, so `adopt --dry-run` and the audit's ruleset
check stay offline. The audit's [live Actions checks](guides/actions-live-checks.md) do read the
forge, but only when the `origin` remote names the repository on github.com and a token is
found; `praetorctl audit --offline` skips them, as the generated pre-commit hook does. A CI
checkout usually has no `refs/remotes/origin/HEAD` and would resolve `main`, so a repository
whose default branch is not `main` declares `repository.default_branch`; CI then audits the
ruleset a local run writes. The manifest writers record it for you (`forge.DefaultBranchToDeclare`): adoption,
`praetorctl init` and harvester onboarding write the checkout's origin HEAD into the
`.standards.yaml` they create when it is not `main`
(`TestAdopt_Positive_MasterRepositoryRulesetProtectsMaster`, `TestInit_3D_DefaultBranch`,
`TestEnsureOnboardingManifest_Positive_DeclaresAMasterOriginHead`). Adoption never rewrites an
existing manifest: when one declares no branch and the origin HEAD is not `main`, it warns and
names the line to add (`TestAdopt_Negative_UndeclaredBranchInAnExistingManifestIsWarned`).

A declaration outside `config.ValidBranchName` (1 to 128 letters, digits, `.`, `_`, `/` or `-`,
no `..`) fails the manifest load. An origin HEAD outside it fails the resolution instead of
falling back to `main` (`TestRepositoryDefaultBranch_Negative_UnusableSourcesAreErrors`), and
fails adoption, `init` and onboarding before they write a manifest.

The ruleset name `praetor-main-protection` and the path `.github/rulesets/main.json` are the same
for every default branch, because a live ruleset is matched by them.

Earlier Praetor versions rendered `main` whatever the default branch was. In a repository whose
default branch is something else, that file is still Praetor's unedited rendering: adoption and
`flavor apply` refresh it to the default branch without `--force` (`forge.PriorRulesetDigests`,
`TestAdopt_Positive_RefreshesTheEarlierMainRulesetOfAMasterRepository`). Until then `sync` and the
audit report it as drift.

### Large repositories

Adoption discovers verification inputs (Makefiles, manifests, scripts) through a bounded
walk of the target: 65536 directory entries, 128 files and 32 levels of depth by default
(`util.DefaultDiscoveryEntries` in `internal/util/discovery_bounds.go`). The entry count
covers every directory the walk enters. Version-control, dependency, build-output and agent
scratch directories are skipped by name (`skipVerificationDirectory` in
`internal/adopt/verification_inputs.go`); generated output under any other name, such as a
built documentation site, counts. A repository above those bounds fails with an error that
names the flag raising the bound:

```text
verification discovery exceeds 65536 entries; raise max_entries with --verification-max-entries, up to 200000
```

Raise a bound explicitly instead of trimming the tree:

```bash
standardsctl adopt --dry-run --path /path/to/large-repo \
  --lock-source-root=/path/to/praetor --verification-max-entries=131072
```

`--verification-max-entries` also raises the editor language scan, which reads the
same tree for the IDE step
([editor capabilities](guides/editor-capabilities.md)).
`--verification-max-files` and `--verification-max-depth` raise the other two bounds.
Each value is validated against its ceiling (200000 entries, 512 files, 64 levels). A
value past its ceiling is refused, and an unset flag keeps the default. A bound already
at its ceiling reports that instead of naming the flag. The flags apply to
single-repository adoption; batch `--all-missing` keeps the defaults.

`praetorctl paperclip harness` detects the repository's languages with the same walk and
takes the same three flags:

```bash
praetorctl paperclip harness --path /path/to/large-repo --verification-max-entries=131072
```

Tests: `internal/adopt/large_repo_bounds_test.go` and
`TestPaperclipHarness_VerificationLimitFlags` in `cmd/standardsctl/adopt_limits_test.go`.

### What Adoption Scaffolds Automatically

1. **`.standards.yaml`**: Declarative repository manifest containing profile, facets, tool versions, and policy locks.
2. **`.standards.lock`**: Cryptographic SemVer lockfile binding your repo to exact governance standard releases.
3. **`.standards-baseline.json`**: Technical debt ratcheting baseline. Existing infractions (e.g. legacy loop bounds, unwrapped errors) are recorded so legacy code compiles while new code is strictly gated. A re-adoption with `--record-baseline` (the default) rescans the repository and keeps the recorded file untouched when the rescan finds the same debt for the same repository, so an unchanged repository gets no `generated_at`-only diff; it rewrites the file when the infractions or the repository identity changed (`TestReconcileBaseline_KeepsUnchangedBaseline_3D` in `internal/adopt/baseline_identity_test.go`). A rescan without a resolved identity keeps the repository the file records instead of blanking it, as `praetorctl baseline --record` does (`TestReconcileBaseline_UnresolvedIdentityKeepsRecordedRepository_3D`).
4. **`AGENTS.md` + 6 Vendor Targets**: Canonical agent operating harness transpiled to `CLAUDE.md`, `.cursor/rules/*.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md` and `.codex/rules.md`. `agent_clients` in `.standards.yaml` limits these to the clients the repository uses ([agent client selection](guides/editor-capabilities.md#selecting-agent-clients)).
5. **`.devcontainer/devcontainer.json`**: Multi-architecture container configuration pinned to verified base images. Adoption also pins the directory to LF: the managed block at the end of `.gitattributes` opens with `.devcontainer/* text eol=lf`, so a Windows checkout with `core.autocrlf=true` verifies the same bytes. Every other line of an existing `.gitattributes` is kept, and a rule of the repository's own that names `.devcontainer/` and sets another line-ending treatment stops adoption before its first write ([checkout line endings](guides/devcontainer-bootstrap.md#checkout-line-endings)).
6. **Multi-IDE Configs**: Workspace settings for every supported editor, or only the ones `editors` in `.standards.yaml` names ([editor selection](guides/editor-capabilities.md#selecting-editors)).
7. **Makefile & LeftHook**: Automated pre-commit hooks and standard verification targets (`make verify-all`).
8. **Documentation gate** (the `docs:seo-portal` facet, in the default facet set): the locked
   Markdown gate under `tools/markdownlint/`, the interactive figure engine under
   `tools/figures/`, `docs-lint` and `docs-figures` targets on `verify-all`, the figure
   engine's rules in the managed block at the end of `.gitattributes`, and
   `.github/workflows/praetor-docs.yml` with its required
   **Documentation Governance** context
   ([documentation governance](guides/documentation-governance.md#adoption-audit-and-ci),
   [figures](guides/figures.md#in-adopting-repositories)). The figure checks skip, saying why,
   until the repository adds its first spec under `docs/figures/`. Adoption stops, even with
   `--force`, when a file the repository already had at one of the engine's paths under
   `tools/figures/` differs from it; move that file aside and rerun. Remove the facet to opt out.
9. **Go API compatibility gate** (the `api:public-contract` facet, in the default facet set):
   the gate program `tools/apicompat/gate/main.go` and `.github/workflows/praetor-api.yml` with
   its required **Go API Compatibility** context, which compares the exported API of every Go
   module with the pull request's base or the newest root release tag
   ([Go API compatibility gate](guides/api-compatibility.md)). Adoption writes it only where git
   tracks a `go.mod`, at the root or nested; a repository declaring the facet without one gets no
   gate and no required context, and adoption and audit say that no API compatibility checker runs
   for its languages. Adoption stops, even with `--force`, when a file the repository already had
   at either path differs from it. Remove the facet to opt out.
10. **actionlint runner labels**: when a workflow adoption writes, the documentation gate's, the
    Go API compatibility gate's or a flavor's CI workflow, runs on a runner label actionlint does
    not know, adoption declares it under `self-hosted-runner.labels` in `.github/actionlint.yaml`
    (or an existing `.github/actionlint.yml`), so a repository that lints its workflows with
    actionlint accepts them. It only adds labels and leaves a file it cannot patch safely
    untouched, with a warning naming the labels
    ([actionlint runner labels](guides/documentation-governance.md#adoption-audit-and-ci),
    `internal/adopt/actionlint.go`). Decline `actionlint-labels` to opt out.

### What a forced re-adoption changes

`praetorctl adopt --force` (MCP: `"force": true`) refreshes a governed repository; it does not reset
it to the scaffold. It overwrites a file only when `praetorctl audit` compares that file's bytes, so
the audit fails until the file holds them, and merges or keeps every other file. `adopt --help`
states this contract (`adopt.ForceContract` in `internal/adopt/force_contract.go`,
`TestAdoptHelpStatesTheForceContract` in `cmd/standardsctl/adopt_force_contract_test.go`), and the
`standards_adopt` schema restates each clause for agents
(`TestCreateAdoptTool_ForceStatesTheContract` in
`cmd/standards-mcp/tools_adoption_force_test.go`). `--force` rebuilds the lock, so it needs
`--lock-source-root` (MCP: `source_root`), a dry run included
(`TestAdoptForceNeedsLockSourceDryRunIncluded`). Preview a forced run with `--dry-run` first.
Every message that tells you to re-run adopt with `--force` prints the whole command,
`praetorctl adopt --force --lock-source-root=<praetor checkout>`, with the run's own lock source in
place of the placeholder when it was given one (`adopt.ForceCommand`, `TestForceRemedies` in
`internal/adopt/force_contract_test.go`).

Three rules hold on every run, `--force` or not:

- An absent file is created. A file that already holds what adoption writes, line endings aside, is
  verified. An unedited text an earlier release wrote is refreshed without `--force`
  (`refreshPriorScaffold` in `internal/adopt/scaffold.go`).
- Any other overwrite is listed under `Files Replaced` with action `replace`, never as created; an
  editor JSON merge is listed as `merge`. Both carry the line delta and a backup under
  `.workingdir/adopt-backups/<UTC stamp>/<path>` when git ignores that path, and a dry run lists
  them as planned (`replaceExisting` in `internal/adopt/replace.go`; the replaced files item
  below has the details).
- Adoption removes no file of the repository's own. A `<file>.bak` an earlier release left beside a
  hook file is reported and kept (`warnLegacyHookBackup` in `internal/adopt/replace.go`).

| Class | Files | Without `--force` | With `--force` |
| :--- | :--- | :--- | :--- |
| Audit-locked, rebuilt under `--force` | `.standards.lock` and the texts it pins under `.config/archetypes/` | a valid lock is kept, and one that pins only unmodified earlier catalog texts is re-pinned to `--lock-source-root` when no value changes; an invalid lock fails the run | rebuilt from `--lock-source-root`, an invalid lock included (`TestReconcileLockfile_Positive_ForcedRebuildOverEditedLockReplacesWithBackup`) |
| | documentation gate files under `tools/markdownlint/` and `tools/figures/`, and `.github/workflows/praetor-docs.yml` | a drifted file is kept with the warning `preserved, not verified (praetorctl adopt --force --lock-source-root=<praetor checkout> regenerates it)` | replaced; a file under `tools/figures/` that predates adoption still stops the run (`reconcileManagedFamily` in `internal/adopt/managed_family.go`) |
| | `.github/rulesets/main.json` | kept with a warning; the rendering current before the run is refreshed | replaced while the policy enforces linear history or signed commits, kept under any other policy (`TestReconcileBranchRuleset_Boundary_ForceReplacesOnlyWhilePolicyRequiresIt`) |
| | the `.devcontainer/` bundle | an existing `devcontainer.json` keeps the bundle as it is, with a warning | regenerated; each image the bundle records is kept or refreshed, and reported (`prepareAdoptDevContainer` in `internal/adopt/devcontainer.go`, `TestReconcileDevContainer_Positive_ForcedEditIsReplacedWithBackup`) |
| | the documentation gate block in the `Makefile`, the managed block at the end of `.gitattributes` | an edited block fails the run | the block is restored (`TestReconcileDocumentationMakefile_Boundary_DeltaListsOnlyInBlockLines`, `TestReconcileMakefile_Positive_ForceRestoresEditedBlockOnAppendPath`, `TestReconcileGitAttributes_Positive_EditedBlockReplacedUnderForce`) |
| Audit-locked, recompiled every run | vendor context files such as `CLAUDE.md`, persona copies under `.claude/agents/`, plugin copies | compiled from `AGENTS.md` and the canonical personas and skills; a hand edit is replaced | the same (`TestAdopt_Positive_HandEditedVendorFileOnPlainRunReplacedWithBackup`) |
| Managed block, every run | the `.gitignore` and `.prettierignore` tail blocks, the README governance block, the Renovate rule for the managed files, the text register block in `AGENTS.md` | Praetor's block or rule is written; every line outside it is kept | the same |
| Merged | agent hook settings: `.claude/settings.json`, `.gemini/settings.json`, `.codex/hooks.json` | missing Praetor handlers are merged in; every other entry is kept | the same (`TestReconcileAgentHooks_Positive_MergesPreservingForeignContent`) |
| | editor JSON: `.vscode/*.json`, `.zed/*.json`, `.fleet/*.json`, `standards.sublime-project` | kept; the warning names the missing managed values | merged, every repository key kept; a file adoption cannot merge is kept with a warning (`TestAdopt_Positive_ForceMergesEditorJSONKeepingAdopterKeys`) |
| | the `AGENTS.md` harness | kept; only its text register block is spliced from the manifest | regenerated, keeping the preamble, invariant rows under the repository's own IDs and the instructions below the harness; an edited generated line is a `replace` (`TestAdopt_AgentsMD_ForceKeepsRepositoryAdditions`) |
| | `.paperclip/harness.json` | a harness the repository edited is kept; a `platform` naming another repository is a warning | only that `platform` is reset to this repository's `<owner>/<name>`; `.paperclip/rules.md` stays as it is, a deleted one included (`TestAdoptForcePatchesOnlyHarnessPlatform`) |
| | `.standards.yaml` | profiles, facets and every other declaration kept (`praetorctl profile set` changes them); `register.sources` added, or re-bound only to a harness the run writes | the same |
| Generated, not audit-verified | the anti-evasion interceptor, the checkpoint scripts and policy, the personas `.agents/agents/repo-auditor.md` and `repo-gatekeeper.md`, the label taxonomy, `CONTRIBUTING.md`, the pull request template, `SECURITY.md`, editor files that are not JSON | an edited file is kept with a warning that counts the lines regenerating it would change | the same; delete the file and re-run adopt to regenerate it (`TestAdopt_Negative_EditedEvasionHookKeptUnderForce`) |
| | `lefthook.yml` | a current or earlier Praetor rendering is written, migrated or verified; any other configuration is kept and not activated | the same, and a CRLF checkout of the current rendering is rewritten with LF bytes (`TestAdopt_Negative_ForeignLefthookKeptWithAndWithoutForce`) |
| | the `pre-commit` hook written when lefthook cannot install | a hook Praetor did not write is kept | the same (`TestAdopt_Hooks_ForeignPreCommitKeptWithAndWithoutForce`) |
| Written only when absent | flavor templates such as `rustfmt.toml`, developer-owned editor files such as `.nvim.lua`, the ADR directory | an existing file is kept | the same |

A caller that ran `adopt --force` to reset a repository to the scaffold gets this refresh instead:
delete the files to regenerate, then run adopt. `scripts/adopt_repos.sh` runs
`adopt --force --lock-source-root=<its checkout>` for every target (`scripts/test_adopt_repos.py`),
and the praetor-adopt action passes the checkout it builds `standardsctl` from
(`TestPraetorAdoptAction_ForcedRunCarriesTheLockSource` in `internal/forge/adopt_action_test.go`).
Loaded remotely, the action has no `.git`, so a forced run first checks out praetor with git
([Inputs, the binary, and the `report` output](#inputs-the-binary-and-the-report-output)).

### What Adoption Reads Before It Writes

- **Repository identity.** `repository.owner` and `repository.name` come from the
  `origin` remote only (`util.ResolveRemoteIdentity`). The checkout path is never read
  as identity, because its parent directory names where the checkout sits, not who owns
  the repository (`TestAdoptionManifest_CheckoutLayoutIsNotIdentity`). The remote must
  be a network remote naming a host and `<owner>/<repo>` (`util.ReadOriginRemote`); a
  local path or `file://` origin names where a copy sits and counts as no remote
  (`TestReadOriginRemote_Negative_LocalRemoteIsNotIdentity`). Without such a remote,
  both stay empty, the report warns `repository identity unresolved`, the checkpoint
  lifecycle is not installed, and the README badge block is skipped. The Paperclip
  harness `platform` and `rules.md` heading name the identity `.standards.yaml`
  declares, else the origin remote's; with neither, adoption writes no harness, the
  report records `Paperclip harness not written`, and an existing harness stays as it
  is. `praetorctl paperclip harness` fails the same way instead of writing a guessed
  `cordanaLLM/<dir>` (`TestSynthesizeHarness_Negative_NoIdentityIsAnError`). No `cordanaLLM/<name>` default and no `<parent>/<name>` guess
  reaches any adopted file; a flavor body that names the repository, such as the
  scaffolded `.gitleaks.toml`, names the checkout directory alone
  (`TestAdopt_NoRemoteWritesNoGuessedPlatform`,
  `TestApplyFlavor_Negative_CheckoutLayoutIsNotOwner`). A remote read git does not
  answer, such as a cancelled run, fails adoption instead of counting as no identity.
  `repository.visibility` is always left unset because adoption cannot observe it
  offline (`TestAdoptionManifest_UnresolvedIdentityStaysEmpty`).
- **Recovering an unresolved identity.** Until both fields are set, `praetorctl audit`
  fails with `Manifest repository owner and name must not be empty`
  (`cmd/standardsctl/audit.go`), so the audit pre-commit hook blocks commits. Adoption
  never rewrites an existing manifest, so set `repository.owner` and `repository.name`
  in `.standards.yaml` by hand. Add the `origin` remote too and re-run `praetorctl adopt`:
  the re-run installs the checkpoint policy and evaluator, which need that remote,
  writes the Paperclip harness, and reconciles the README block from the fields you
  set. A re-run that finds the remote while the manifest still names no identity
  installs the checkpoint lifecycle and the harness but leaves the manifest and the
  README as they are (`TestAdopt_RerunCompletesOnceIdentityIsSet`).
- **Profile.** The profile an existing `.standards.yaml` declares outranks `--profile`,
  which outranks file markers. A conflicting `--profile` is reported as ignored
  (`TestAdopt_DeclaredProfileGovernsAdoption`). Adoption never rewrites a declared profile;
  `praetorctl profile set` does
  ([Changing the profile, facets or catalog](#changing-the-profile-facets-or-catalog)).
- **Declared HISS exceptions.** A repository whose own standard allows a construct a HISS
  directive bans declares that exception under `hiss.exceptions` in `.standards.yaml`, naming
  the repository document that records it. The one exception is `c_goto_cleanup`: a C or C++
  `goto` that jumps forward to the one cleanup label of its function.

  ```yaml
  hiss:
    exceptions:
      c_goto_cleanup: docs/adr/0003-cleanup-goto.md
      c_goto_cleanup_labels: [unwind] # optional, beside cleanup, out, err and fail
  ```

  The exception holds only while that document is a regular file in the repository. The audit's
  native HISS-01 scan then reports every `goto` except one that meets all five conditions
  ([`internal/hiss/cleanup_goto.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hiss/cleanup_goto.go)):

  1. **Forward.** The `goto` line comes before its label.
  2. **Same function.** The label is in the function body that holds the `goto`.
  3. **Single level.** That function defines exactly one label, so it has one cleanup exit.
  4. **Body level.** The label sits directly in the function body, outside every nested block,
     so the jump leaves blocks and never enters one.
  5. **Name.** The label is `cleanup`, `out`, `err` or `fail`, or is listed in
     `c_goto_cleanup_labels`: C identifiers of at most 64 bytes, at most 8 of them, declared
     only beside `c_goto_cleanup`.

  A backward `goto`, a jump to a label inside a nested block, a second label, another name and
  a computed `goto` all stay findings (`TestCleanupGoto_Negative_OtherGotosStillFail`). The scan
  reads a `goto` or a label only where it opens a line, as it always has. `praetorctl audit`, the
  gate's HISS stage, `praetorctl baseline`, the MCP audit, public-repository verification and
  adoption's legacy-debt scan all read the declaration through
  `config.Manifest.CleanupGotoException`, so they judge the same tree the same way
  (`TestCleanupGotoExceptionReachesTheAudit`, `TestRunHissStage_CleanupGotoException`). HISS-01
  in the `AGENTS.md` and Paperclip harnesses states that rule in place of the C/C++ zero-`goto`
  clause, rendered from the scan's own text (`hiss.CleanupGotoRule`), and keeps Go's own ban.
  Without the declaration every `goto` is a finding and the ban stays. A declaration whose
  document is missing keeps both and warns, naming the document, in the adopt report,
  `praetorctl paperclip harness`, `praetorctl audit`, `praetorctl baseline`, the gate stage and
  the MCP audit (`TestAdoptHonoursDocumentedCleanupGotoException`,
  `TestPaperclipHarness_WarnsOnUndocumentedException`). The document path must be a clean
  repository-relative path of at most 256 bytes, and an exception key praetor does not know
  fails the manifest (`TestHISSExceptions_Negative_BadDeclarationsFail`,
  `TestHISSExceptions_Labels`). The section is repository-only: no profile or fleet layer
  declares an exception, and the effective policy does not change with it
  ([`internal/config/hiss_exceptions.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/config/hiss_exceptions.go)).
- **Existing files.** An existing manifest must parse, or adoption fails and leaves it
  unchanged. Every other existing scaffold is compared with what adoption would write:
  a match is reported as verified, a difference as `differs from the scaffold` with a
  warning, and the file is kept (`TestScaffoldFile_ReportsDriftInsteadOfVerified`). `--force` never
  overwrites an editor file: it merges the managed values into a JSON one, keeping every adopter
  key, and keeps any other differing file with a warning
  ([editor capabilities](guides/editor-capabilities.md#adoption-and-onboarding)).
- **What `--force` overwrites** (every file class:
  [what a forced re-adoption changes](#what-a-forced-re-adoption-changes)). Only a file the audit compares byte for byte, so that the audit
  fails until it holds the scaffold (`scaffold.auditLocked` in `internal/adopt/scaffold.go`):
  the documentation gate's and the Go API compatibility gate's managed files and workflows, and
  the branch protection ruleset while the policy requires one. Every other generated file is not audit-verified and is kept under
  `--force` too, with the note `differs from the scaffold adoption writes (-N/+M lines); not
  audit-verified; kept` and a warning: the agent anti-evasion interceptor, the checkpoint
  scripts, the canonical personas `.agents/agents/repo-auditor.md` and `repo-gatekeeper.md`,
  the label taxonomy and the checkpoint policy (`TestAdopt_Negative_EditedEvasionHookKeptUnderForce`,
  `TestAdopt_Negative_EditedPersonaKeptUnderForce`,
  `TestAdopt_Boundary_ForceKeepsDriftedCheckpointScriptLifecycleUnavailable`). So is a
  pre-commit hook praetor did not write ([git hooks](guides/git-hooks.md#hook-files-adoption-keeps)).
  To regenerate one of them, delete it and re-run adopt. So is a `lefthook.yml` that is neither a
  current nor an earlier Praetor rendering: it is kept and not activated, and the skip names the
  generated jobs it lacks and the jobs it adds
  ([the lefthook.yml adoption writes](guides/git-hooks.md#the-lefthookyml-adoption-writes)).
- **Replaced files.** When `--force` overwrites a drifted scaffold, the report lists it under
  `Files Replaced` with action `replace`, never as created. The entry carries a line delta
  (`-removed/+added lines` and the first three removed lines; a replace that only reorders
  lines reads `-0/+0 lines, N moved` with the first three moved lines, so a reordered table
  names the rows that moved: `util.LineDeltaOf`, tests in `internal/util/line_delta_test.go`)
  and where the prior bytes went:
  `.workingdir/adopt-backups/<UTC stamp>/<path>`, written only when `git check-ignore`
  confirms that path is ignored. The `git-ignore` step, which writes the managed
  `/.workingdir/` rule, runs right after the manifest step and before every step that can
  replace a file, so a first adoption keeps a backup of every file it replaces, editor merges
  included, and a dry run plans the same backups without writing the rule or a copy
  (`adoptSteps` in `internal/adopt/adopt.go`, `backupIgnored` in `internal/adopt/replace.go`,
  tests in `internal/adopt/first_adoption_backup_test.go`). Without that confirmation, for
  example when `adoption.decline` names `git-ignore` and the repository's own rules do not
  ignore `.workingdir/`, the file is replaced without a copy, its entry says `no backup`, and
  one warning per run gives the reason. A file that differs only in its line endings is
  verified, not replaced (`TestScaffoldFile_Positive_ForceReplacesWithBackupAndDelta`,
  `TestScaffoldFile_Boundary_DryRunPlansReplaceAndDeltaTruncates`,
  `TestScaffoldFile_Boundary_CRLFOnlyDifferenceIsNotReplaced`). Under `--force`, a backup
  root that is a symlink or sits behind one fails the run before the first write
  (`preflightForceBackupRoot` in `internal/adopt/replace.go`,
  `TestAdopt_Negative_ForceRefusesSymlinkedBackupRootBeforeAnyWrite`). Agent hook merges keep
  their copy in the same place ([agent hooks](guides/agent-hooks.md)). A backup written before
  the private `.workingdir/` exists creates it owner-only (`0700`), as the ledger setup does
  (`TestBackupExisting_Positive_CreatesPrivateWorkingDir`). The `standards_adopt` MCP result
  lists the same entries under `Replaced Files` (`Planned Replacements` in a dry run), and
  neither report repeats a replaced file among the reconciled ones
  (`AdoptReport.ReconciledNotReplaced` in `internal/adopt/scaffold.go`,
  `TestServer_Positive_AdoptForceReportsReplacedFile`). The `--all-missing` summary counts
  replaced files on their own line.
- **Audit-locked files.** Files whose exact bytes audit checks are rewritten, and an overwrite
  of edited bytes is a replace with its delta and backup, never a create. Under `--force` that
  covers `.standards.lock`, a pinned file under `.config/archetypes/`, a DevContainer bundle
  file, a documentation family file such as `tools/markdownlint/markdownlint-cli2.yaml`, an
  edited documentation gate block in the `Makefile`, whose delta lists only the lines inside
  the block, and an edited managed attribute block at the end of `.gitattributes`; without
  `--force` either edited block fails the run, and a disable of `docs:seo-portal` refuses to
  touch it. On every run, `--force` or not, it covers a vendor context file such as
  `CLAUDE.md` and a persona copy such as `.claude/agents/praetor-auditor.md` that holds a hand
  edit (`recordProjections` in `internal/adopt/vendor_targets.go`,
  `internal/adopt/persona_copies.go`). A file that already holds its bytes is verified. Earlier
  Praetor texts are refreshed, not replaced: a vendor file that is the projection of
  `AGENTS.md` as the run found it or as the `HEAD` commit holds it (an `AGENTS.md` edited
  after the last `compile-context`; without a commit only the first counts), a persona copy
  that is the copy of its canonical persona as the run found it, Praetor's own
  unedited DevContainer placeholder, a catalog
  text with a layout-only successor, an earlier text of a documentation family file, the
  documentation gate block an earlier Praetor wrote, and a managed attribute block holding
  the rules of an earlier release or of another facet and decline state, such as the block
  without the DevContainer rule (tests in
  `internal/adopt/locked_replace_test.go`, `internal/adopt/vendor_targets_test.go`,
  `internal/adopt/vendor_head_projection_test.go`,
  `internal/adopt/documentation_makefile_refresh_test.go`,
  `internal/adopt/gitattributes_edit_test.go` and
  `internal/adopt/gitattributes_devcontainer_test.go`). Without `--force`, a symlinked backup root
  fails the run before its first write when a vendor file or a persona copy holds a hand edit
  (`preflightVendorBackupRoot` in `internal/adopt/vendor_targets.go`,
  `preflightPersonaBackupRoot` in `internal/adopt/persona_copies.go`,
  `TestAdopt_Negative_PlainRunRefusesSymlinkedBackupRootForVendorEdit`,
  `TestAdopt_Boundary_PlainRunRefusesSymlinkedBackupRootForPersonaCopyEdit`). A re-run lists
  an existing persona copy and the pre-commit hook adoption installed as reconciled, never as
  created (`internal/adopt/rerun_report_test.go`).
- **`AGENTS.md` harness under `--force`.** An existing harness is kept without `--force`,
  apart from its text register block, which is spliced from the manifest as `compile-context`
  splices it (`keepAgentHarness` in `internal/adopt/harness.go`,
  `TestAdoptKeptHarnessSplicesRegisterBlock` in `internal/adopt/harness_register_test.go`). A
  splice that changes the file is reported as a replace with its line delta and backup, like a
  forced refresh. A block with no end marker, or a backup root adoption refuses, fails adoption
  before its first write (`TestAdoptKeptHarnessSpliceRefusedBeforeAnyWrite`).
  With it, the harness is regenerated and what the repository added around it stays:
  - the preamble: every line above the harness start, for example an SPDX header. The
    harness starts at the first `# ... Agent Operating Harness` title, together with a
    `<!-- markdownlint-disable ... -->` line directly above it. A harness whose title was
    renamed is found by its `## Core Directives & Invariants` heading and starts at the
    nearest H1 above that heading, or at the top of the file when no H1 precedes it, so the
    renamed title and its intro are regenerated rather than kept above a second copy
    (`harnessStart` in `internal/adopt/harness.go`,
    `TestAdopt_AgentsMD_ForceReplacesRenamedHarnessTitle`);
  - invariant rows under an ID of the repository's own, such as `**ACME-01**`, appended
    after the catalog rows in their original order and byte for byte, escaped pipes
    included. `hisscatalog.InvariantTableRows` reads both tables through
    `SplitInvariantRow`, the splitter `ParseGatedInvariants` uses. A `HISS-<n>` row
    belongs to the catalog, so it is regenerated;
  - the instructions below the harness boundary: the end marker, the footer of an older
    harness, or a `---` line, searched only below the harness start.

  Every other harness line is regenerated. An edited one, such as reworded `HISS-02` text, is
  reported as `replace` with its line delta and backup, as above. The text register block
  comes from the manifest, as `compile-context` renders it. The file keeps its CRLF
  convention, and a second `--force` run leaves it byte-identical. A file whose harness has no
  boundary is left untouched and the run records an error. Prose that only mentions the
  harness is not a harness: the harness is merged above it and every line is kept
  (`TestAdopt_AgentsMD_ForceKeepsRepositoryAdditions`,
  `TestAdopt_AgentsMD_ForceKeepsCRLFPreamble`,
  `TestAdopt_AgentsMD_ForceRefusesUnknownBoundary`,
  `TestAdopt_AgentsMD_ForceKeepsProseNamingTheHarness` in `internal/adopt/adopt_test.go`).
- **Text register policy.** Every run renders the harness's text register block from the
  manifest, `--force` or not. A policy the renderer rejects, such as a `register.tasks`
  entry that is not a `target_tasks` label ([text register](guides/text-register.md)), fails
  adoption before its first write, a dry run included; a manifest that declines
  `agent-harness` is not checked (`preflightAgentHarness` in `internal/adopt/adopt.go`,
  tests in `internal/adopt/register_preflight_test.go`).
- **Agent context verification.** The agent-definitions step projects the canonical personas
  and skills through the writer `compile-context` uses (`compiler.CompileAgentSurfaces`): the
  persona copy in every persona directory `agent_clients` selects and, when
  `.agents/plugins/praetor/plugin.json` exists, the plugin persona and skill copies. A
  hand-edited plugin copy is replaced with its line delta and backup like any persona copy, and
  a symlinked plugin directory fails adoption before its first write. After the last step,
  adoption runs the check `compile-context --verify` runs (`compiler.VerifyCompiledContext`,
  `verifyAgentContext` in `internal/adopt/context_verify.go`). The check reads the whole
  repository, not only the files adoption wrote. Each rejection is an error on the report,
  led by "compile-context --verify rejects the repository's agent context after adoption", and
  on the step that writes what it rejects: `agent-definitions` for a persona or plugin copy,
  `git-ignore` for the evidence ignore rule, `agent-harness` for `AGENTS.md` and the vendor
  files. The run is then incomplete and `praetorctl adopt` exits non-zero; a caveman finding on
  text adoption keeps as written stays a warning. A dry run, and a manifest that declines
  `agent-harness` or `agent-definitions`, skip the check (tests in
  `internal/adopt/plugin_projection_test.go` and `internal/adopt/context_verify_test.go`).
  Earlier releases exited 0 in these cases, which now fail the run:
  - A file in a persona directory that projects no canonical persona. `compile-context
    --verify` treats every `.md` file in `.claude/agents`, `.codex/agents`, `.github/agents`,
    `.gemini/agents` and `.agents/plugins/praetor/agents` as compiled output, so a subagent
    written there by hand fails the run. Move it into `.agents/agents`, from where adoption
    projects it to every selected client, or remove it
    (`TestAdopt_Negative_ClientPersonaDirFileWithoutCanonicalPersona`). Adoption never rewrites
    or removes such a file.
  - A manifest that declines `git-ignore` while Git does not ignore `.workingdir/evidence/`.
    Add `/.workingdir/` to the repository's own `.gitignore`
    (`TestAdopt_DeclinedGitIgnoreNeedsTheOperatorWorkingDirRule`).
  - Any other failure `compile-context --verify` names: fix what it names, or run
    `praetorctl compile-context`.
- **Earlier Praetor output.** The manifest, lock, label taxonomy, pinned catalog, flavor
  YAML (`.clang-format` and `.clang-tidy` included) and the `docs:seo-portal` documentation
  gate's YAML that adoption writes pass `yamllint --strict` with its default rules
  (`make hooks-lint`, [git hooks](guides/git-hooks.md)). YAML written by other commands is not
  covered yet: `.needs.yaml`, `FRAMEWORK_DEMAND.yaml`, `changelog.d` fragments and
  `.github/FUNDING.yml` still fail `yamllint --strict`. Files an earlier release wrote
  before that layout, and nobody edited since, are refreshed on a plain re-run with every value
  unchanged: a manifest that is exactly `yaml.Marshal` of what it declares
  (`TestAdoptMigratesAnEarlierManifestRendering`), the earlier label taxonomy
  (`TestReconcileLabels_Positive_RefreshesPriorTaxonomy`), and a lock that pins only earlier
  catalog texts, which is re-pinned to `--lock-source-root` while the pinned catalog files are
  replaced (`TestAdoptRepinsAnUnmodifiedEarlierCatalog`). The catalog re-pin happens only when
  every source file decodes to exactly the values of the earlier text it replaces; a source
  that changes even one value keeps failing plain adoption
  (`TestAdoptDoesNotRepinAnEarlierCatalogToChangedValues`). The failure names each declared
  text whose values the source changes and the narrow re-pin, `praetorctl profile set`
  (`TestExistingLockMismatchNamesTheValueChanges` in `internal/adopt/lock_remedy_test.go`). The files `--force` no longer
  overwrites are refreshed the same way when they hold a text an earlier release wrote: the agent
  anti-evasion interceptor, the checkpoint scripts, which go to the `--lock-source-root` bundle,
  and the two canonical personas, which go through the root-pinned writer `compile-context` uses
  (`TestAdopt_Positive_PriorEvasionHookRefreshedOnPlainRun`,
  `TestReconcileCheckpointBundle_Positive_PriorScriptRefreshedOnPlainRun`,
  `TestAdopt_Positive_PriorPersonaRefreshedOnPlainRun`). Their sets hold the current text too,
  and a test fails until a changed text is added, so the release that changes one still
  refreshes the copy adopters hold (`priorPersonaDigests` in `internal/adopt/ruleset.go`,
  `TestPriorPersonaDigests_Boundary_CurrentTextsRecorded`; the hook files are covered in
  [git hooks](guides/git-hooks.md#hook-files-adoption-keeps)). An earlier text is recognised in
  either consistent line-ending style, so a CRLF checkout of the manifest, label taxonomy or a
  persona (`core.autocrlf` on Windows) is refreshed too and keeps CRLF
  (`TestAdoptMigratesACRLFEarlierManifestInItsOwnStyle`,
  `TestReconcileLabels_Positive_RefreshesCRLFPriorInItsOwnStyle`,
  `TestReconcileAgentDefinitions_Boundary_CRLFPriorKeepsCRLFAndSymlinkNotWrittenThrough`). The catalog is the
  exception: `.standards.lock` pins the exact LF bytes of each file, so a CRLF checkout of it
  fails lock verification before and after this refresh; keep `.config/archetypes` at
  `eol=lf` in `.gitattributes`. An edited copy of any of them, or one with mixed line endings,
  is left as it is and keeps the contract above
  (`TestAdoptDoesNotRepinAnEditedOrForeignCatalog`).
- **Files the repository ignores.** Once every step has run, adoption asks git whether the
  repository's own ignore rules exclude any file it created, verified, merged or replaced
  (`reportIgnoredWrites` in `internal/adopt/ignored_writes.go`). An ignored file that git does
  not track is still written, so the local checkout works, but no commit carries it, so a
  clean checkout lacks it. Each one is reported on the step that wrote it, naming the rule
  and the negation that re-includes it, such as
  `tools/figures/dist/loader.js: ignored by .gitignore:1 (dist/) ... Add !/tools/figures/dist/ after that rule`.
  The negation names the shallowest ignored parent directory, because git never looks inside an
  ignored directory. The finding is an error, except for editor files, which audit never reads:
  those are a warning, and the IDE Ecosystem pillar shows it. The private ledger paths the
  managed block ignores on purpose and the installed Git hooks are not checked. Neither is an
  entry whose file is not on disk after the run: without a Prettier configuration the
  `formatter-ignore` step records `.prettierignore` but writes nothing, so a rule that ignores
  that name is no finding. A dry run checks the files it plans to create instead
  (`writtenPath`). When git cannot answer, the report warns that the check was skipped
  (`TestReportIgnoredWrites_Positive_FindingsLandOnTheWritingStep`,
  `TestReportIgnoredWrites_Negative_PrivateHookRemovedAndSkippedPathsAreNotChecked`,
  `TestReportIgnoredWrites_Boundary_OnlyFilesOnDiskOrPlannedAreChecked`,
  `TestAdopt_Negative_UnwrittenPrettierIgnoreIsNotReported`,
  `TestAdopt_Negative_DeclinedGitIgnoreProposesTheNegation`).

  Praetor writes its pinned catalog, label taxonomy, checkpoint policy and hook scripts under
  `.config/`, the name Kconfig gives its build configuration file. Where a Kconfig-style rule (a
  bare `.config`, `/.config` or `.*`) hides that directory, the `git-ignore` step adds the
  anchored, directory-only negation `!/.config/` to the managed block and the `.gitignore`
  entry says so. Kconfig `.config` files stay ignored at the root and at every depth, a
  `.config/` directory below the root (a vendored tool's) stays ignored, and a later run keeps
  the negation. A repository that ignores the directory itself with `.config/` gets no
  negation, only the errors. With `git-ignore` declined, each error proposes `!/.config/`, and a
  dry run plans the negation without writing it (`kconfigConfigRule`,
  `TestAdopt_Positive_KconfigRuleGetsTheDirectoryNegation`,
  `TestAdopt_Boundary_DryRunAndRootConfigFile`). The negation also re-includes any file
  already below `.config/` that adoption does not write, such as a tool's credentials, so the
  next `git add -A` would commit it. The `git-ignore` step warns and names each such file,
  leaving out tracked files and, in a real run, files another rule still ignores; a dry run
  cannot see those rules yet and says so. Ignore each named file by path above the managed
  block, or move it (`reportReincludedConfigFiles`,
  `TestAdopt_Boundary_NegationNamesTheForeignFilesItReincludes`). A `.config` file at the repository root
  cannot share its name with that directory: adoption stops before any write and names the
  collision (`preflightConfigRoot`). Moving Praetor's files out of `.config/` is planned
  separately. `praetorctl audit` fails a documentation gate file that git ignores and does not
  track, so a local audit gives the verdict a clean checkout gets
  (`auditDocumentationFilesCommitted` in `cmd/standardsctl/audit_documentation.go`,
  `TestAuditDocumentationGate_Negative_IgnoredUntrackedFileFails`).

### Changing the profile, facets or catalog

`praetorctl profile set` changes what an adopted repository declares, or re-pins it to a newer
Praetor catalog, without `adopt --force`:

```bash
# Move to another profile; preview first, then apply
praetorctl profile set os-image --lock-source-root=/path/to/praetor --dry-run
praetorctl profile set os-image --lock-source-root=/path/to/praetor

# Replace the declared facets; --facets= declares none
praetorctl profile set --facets=security:high --lock-source-root=/path/to/praetor

# Keep the declaration and take up a catalog whose values changed
praetorctl profile set --lock-source-root=/path/to/praetor
```

It writes three things and nothing else (`adopt.SetProfile` in `internal/adopt/profile_set.go`):

| File | Change |
| :-- | :-- |
| `.standards.yaml` | The `profiles` and `facets` lists, rewritten only when they change. Every other line, comment and key keeps its text; a comment on the list's key line is kept. A `<profile>` replaces the whole profiles list; an omitted `--facets` keeps the declared facets. |
| `.standards.lock` | Rebuilt from `--lock-source-root` for the new declaration. |
| `.config/archetypes` | The texts the new lock pins, written or replaced. A text the declaration no longer names is kept; delete it yourself when nothing reads it. |

Every other file stays as it is (`TestSetProfile_Positive_LeavesAnAdoptedTreeAlone`). Every file
is read, built and checked before the first one is written, so a refusal leaves the repository as
it was (`TestSetProfile_Negative_RefusalsWriteNothing`). Each write is bound to the bytes read
first, so a file edited in between fails the run instead of losing the edit. A replaced lock or
catalog text is backed up under `.workingdir/adopt-backups/` and listed with its line delta, as
adoption lists a replaced file. `--dry-run` writes nothing and prints each changed file as a diff,
or its content when it would be created (`TestSetProfile_Boundary_EmptyFacetsAndDryRun`). A real
run reads the lock and the policy back as the audit's manifest and lock gates do: the lock must
verify against the vendored catalog without the source bundle (`TestProfileSetEndToEnd` in
`cmd/standardsctl/profile_test.go`).

#### Files derived from the declaration

Adoption also renders files from the declared profiles and facets, and `profile set` leaves them
as they are. After a profile or facet change they can fail `praetorctl audit`. The DevContainer is
synthesized from the declaration (`devcontainer.SynthesizeWithFeatures`), turning
`docs:seo-portal` on or off adds or retires the documentation assets, the README block and the
documentation context in the ruleset, and turning `api:public-contract` on or off adds or retires
the [Go API compatibility gate](guides/api-compatibility.md) and its context in a repository
whose `go.mod` git tracks. So after
writing, or in a dry run against the planned declaration, `profile set` runs the five audit gates
that check those files: the README block, the documentation gate, the API compatibility gate, the
DevContainer and the branch protection ruleset (`declarationGates` in
`cmd/standardsctl/profile.go`). Each prints its verdict
as `praetorctl audit` prints it. When one fails, `profile set` still exits 0, since it wrote what it
was asked to, and prints the refresh:

```bash
praetorctl adopt --force --dry-run --lock-source-root=/path/to/praetor   # preview
praetorctl adopt --force --lock-source-root=/path/to/praetor
```

`adopt --force` rewrites every audit-locked file that drifted, not only the ones these gates
check, so read its preview first. Plain `adopt` does not refresh them: it keeps an existing
DevContainer and refuses to retire the documentation or API compatibility context without
`--force`.
`TestProfileSetReportsDerivedDrift_3D` in `cmd/standardsctl/profile_test.go` covers the report,
the refresh and a re-run that passes every gate.

The other audit gates do not run. The invariant scan reads its limits from the effective policy
(`EffectivePolicy.HISSScanOptions` in `internal/config/hiss_exceptions.go`), so a profile with a
lower `max_func_loc` can report debt the baseline does not hold. Run `praetorctl audit` for the
full verdict.

`--lock-source-root` is required. A profile or facet the source bundle does not define fails
before anything is written. The error names the bundle and its catalog version, such as
`lock source /path/to/praetor, catalog v0.0.0+catalog.3536de06c71a: profile "os-image": ...`,
so a bundle older than the archetype shows up as the stale part
(`TestBuildLockfileMissingIDNamesTheSourceCatalog` in `internal/config/lockbuild_test.go`).

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

`"force": true` is the refresh described in
[What a forced re-adoption changes](#what-a-forced-re-adoption-changes) and needs `source_root`.
The tool returns a detailed summary of created, reconciled and replaced files, detected archetypes, and recorded legacy debt.

---

## ⚡ GitHub Action Bot & PR Automation

Add Praetor fast adoption to your GitHub repository using our composite action:

```yaml
name: Praetor Adoption Bot

on:
  issue_comment:
    types: [created]

permissions: {}

jobs:
  adopt:
    if: >-
      github.event.issue.pull_request &&
      contains(github.event.comment.body, '/adopt') &&
      contains(fromJSON('["OWNER","MEMBER","COLLABORATOR"]'), github.event.comment.author_association)
    runs-on: ubuntu-26.04
    timeout-minutes: 30
    permissions:
      contents: read
      pull-requests: read
    steps:
      - id: pr-head
        timeout-minutes: 2
        shell: bash
        env:
          GH_TOKEN: ${{ github.token }}
          PR_NUMBER: ${{ github.event.issue.number }}
        run: |
          sha="$(gh api "repos/$GITHUB_REPOSITORY/pulls/$PR_NUMBER" --jq .head.sha)"
          [[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "no head for #$PR_NUMBER: '$sha'" >&2; exit 1; }
          echo "sha=$sha" >> "$GITHUB_OUTPUT"
      - uses: actions/checkout@v7
        with:
          ref: ${{ steps.pr-head.outputs.sha }}
          persist-credentials: false
      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@main
        with:
          mode: adopt
          force: true
          cache: false
```

A `/adopt` comment on a pull request, written by the repository's owner, a member of its
organization or a collaborator, runs the adoption against that pull request's head and leaves the
result in the job summary. It commits nothing. Four parts of the example each close a hole:

- **The `author_association` conjunct.** `issue_comment` runs the default branch's workflow with
  the base repository's token for any account that can comment. `OWNER`, `MEMBER` and
  `COLLABORATOR` are the associations the repository granted; the other values GitHub defines
  (`CONTRIBUTOR`, `FIRST_TIMER`, `FIRST_TIME_CONTRIBUTOR`, `MANNEQUIN`, `NONE`) are within any
  account's reach.
- **The head lookup.** An `issue_comment` payload carries the issue, not the pull request, so a
  checkout without a `ref` takes the default branch and the adoption never sees the pull
  request. The step asks the pulls API for the head by number and fails rather than publishing
  anything that is not a commit id.
- **Read scopes and `persist-credentials: false`.** The checked-out tree is the pull request's
  content; the job holds no write scope and leaves no credential in the checkout.
- **`cache: false`.** `actions/setup-go` restores and saves the Go caches unless its `cache` input
  is false, and an `issue_comment` run saves into the default branch's cache scope, which every
  branch restores from. The job saves nothing there.

`force: true` makes the run the refresh described in
[What a forced re-adoption changes](#what-a-forced-re-adoption-changes), which rebuilds
`.standards.lock` from a git checkout of praetor. The runner unpacks a remote action without its
`.git`, so the action's build step runs `git init`, a one-commit `git fetch` of
`cordanaLLM/praetor` at `main` (the action's own repository and ref, read from
`github.action_repository` and `github.action_ref`) and a detached checkout. These land in a fresh
`praetor-source.*` directory under `RUNNER_TEMP`. It builds `standardsctl` from that checkout and
passes the checkout as `--lock-source-root`. The fetch is anonymous, from `GITHUB_SERVER_URL`, so
the runner needs to reach it. A fetch that fails, or a checkout that is not praetor, stops the
step before anything is built. With a branch ref such as `@main`, the checkout is that branch's
commit when the step runs. A tag or commit pins it.

`standardsctl` itself still comes from the praetor ref the `uses:` line pins, not from the pull
request (see below). That is why the example accepts a head pushed to a fork: the adoption reads
the fork's files but never builds or executes them. A job that builds or runs the pull request's
code has to refuse fork heads, as praetor's own adopt-comment job does.

### Praetor's own adoption workflow

`.github/workflows/adopt.yml` is the hardened form of the example, split into one job per
entry point:

| Job | Trigger | Token | What it does |
| :-- | :-- | :-- | :-- |
| `adopt` | `workflow_dispatch` | `contents: write`, handed only to the commit step | adopts `target_path` (default `.`), runs the HISS-13 debt ratchet on it, then commits with a DCO sign-off and pushes; with `dry_run` set it passes `dry-run` to the action, which plans the adoption without writing a file, and skips the ratchet and the commit; keeps its own Go build cache through `.github/actions/go-cache` (`job: adopt`) |
| `adopt-comment` | `/adopt` or `/dogfood` comment from a trusted commenter | `contents: read`, `pull-requests: read` | resolves the pull request head, refuses it unless it lives in this repository, checks it out without credentials, and runs the adoption and ratchet, or the dogfood benchmark, against it; restores and saves no cache |

Inside praetor the action builds the checked-out tree's own `cmd/standardsctl`, and the job loads
`./.github/actions/praetor-adopt` from that tree too, so the comment job builds and runs the pull
request's code. That is the point of `/dogfood`, and read scopes alone do not contain it: the job
runs in the default branch's context, and code in it can save Actions cache entries into the
default branch's scope, which the `adopt` job and every other branch restore. Three things keep
it contained:

- Only a trusted commenter can start the job.
- The head lookup refuses a head whose repository is not this one, which covers forks and
  deleted forks. Pushing a branch here takes write access, and an account with write access can
  start the job by commenting anyway, so a push that lands between the comment and the lookup
  admits no one the gate keeps out. The head is read when the job starts, so that later push is
  what runs; the repository and the commit come from one API answer, so the check covers it.
- Both uses of the action pass `cache: "false"`, and the job has no cache step, so nothing it
  builds is saved for a later run.

The `adopt` job sets Go up itself with `cache: false` before the action does. That keeps it
inside `AuditGoBuildCaches` (`internal/forge/go_cache_checks.go`), which reads the `setup-go`
steps of workflow jobs and not those inside an action.

Two checks keep the split honest. `AuditPullRequestPermissions` in
`internal/forge/workflow_permissions.go` reports any job that `pull_request`,
`pull_request_target` or an ungated `issue_comment` can start while it holds a write scope, and
`TestAuditPullRequestPermissions_Guard_ThisRepositoryIsDisciplined` runs it over this repository.
`internal/forge/adopt_workflow_test.go` pins the rest: only the dispatch job writes and pushes,
every checkout drops its credential, the comment job checks out the head it resolved,
`target_path` reaches both the adoption and the ratchet through env, `dry_run` reaches the
adoption and keeps the ratchet and the push from running, only the dispatch job keeps
a Go cache and stays visible to `AuditGoBuildCaches`, every job and the head lookup run under an
explicit `timeout-minutes`, and the head lookup's own shell body is executed against a stub `gh`,
which is how a fork head is shown to be refused.

### Inputs, the binary, and the `report` output

| Input | Reaches | Effect |
| :-- | :-- | :-- |
| `path` | `PRAETOR_PATH` | `--path=<value>`, and the `--source`/`--target-dir` of the `compile-context --verify` that follows an adopt run that is not a dry run; an empty value is refused before anything runs |
| `mode` | `PRAETOR_MODE` | selects the subcommand, `adopt` or `dogfood`; any other value is refused |
| `dry-run` | `PRAETOR_DRY_RUN` | `--dry-run=<value>`; in `adopt` mode `true` plans without writing a file and skips the `compile-context --verify`, which would otherwise check a tree the run did not touch and fail on a repository with no `AGENTS.md` yet (`TestPraetorAdoptAction_Boundary_DryRunSkipsCompileContext`); in `dogfood` mode it changes nothing, because `dogfood` applies adoptions only to `--targets` repositories (`testTargetAdoptions` in `internal/dogfood/dogfood.go`) and the action passes none, so the host is audited either way |
| `force` | `PRAETOR_FORCE` | `--force=<value>`, adopt only: the refresh in [What a forced re-adoption changes](#what-a-forced-re-adoption-changes). `--force` rebuilds `.standards.lock` and needs a lock source, so with `force: true` the action also passes `--lock-source-root=<checkout>`, the praetor checkout the build step compiled `standardsctl` from, and refuses the run when the build step published none (`TestPraetorAdoptAction_ForcedRunCarriesTheLockSource`). The rebuild inventories that checkout with `git ls-files`. When the action's own tree has no `.git`, as when loaded remotely, a forced adopt run first checks out the action's repository at the action's ref with git under `RUNNER_TEMP` and builds from it. A tree that already is a git checkout is used as it is (`internal/forge/adopt_action_source_test.go`) |
| `record-baseline` | `PRAETOR_RECORD_BASELINE` | `--record-baseline=<value>`, adopt only |
| `go-version` | `actions/setup-go` | the toolchain the step compiles `standardsctl` with; it never reaches `standardsctl`, and it has to satisfy the `go` directive of praetor's `go.mod` |
| `cache` | `actions/setup-go` | its `cache` input, default `true` as in `setup-go` itself; set `false` in a job a pull request or its comment starts, and in a job that keeps its own Go cache (`TestPraetorAdoptAction_Positive_CacheInputReachesSetupGo`) |

All five runtime inputs reach the run step as environment variables rather than as expressions
spliced into its script, so a value carrying a shell metacharacter or a newline is data rather
than script. `path` and the three booleans are then passed to `standardsctl` as single arguments;
`mode` is not passed at all, it picks the subcommand. `mode` and the booleans are validated
first: `mode: Dogfod` is a failure, not a silent `adopt` run. Each boolean is passed as
`--flag=<value>` rather than added when it is `true`, because `dogfood --dry-run` and
`adopt --record-baseline` default to true in `standardsctl` — an omitted flag would be an opt-in,
so `record-baseline: "false"` would have had no effect.

The `standardsctl` that runs is the one the action's own ref carries: the step builds
`cmd/standardsctl` out of the praetor checkout that `GITHUB_ACTION_PATH` points into, or, on a
forced adopt run of an action tree with no `.git`, out of a git checkout of the same repository
and ref (the `force` row above), so
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

The `binary` output is the absolute path of the `standardsctl` the build step compiled. A later
step of the same job that has to run praetor again, such as `adopt.yml`'s debt ratchet, runs that
binary instead of compiling a second one.

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
shell bodies against a stub binary and reads the input defaults out of `action.yml`, and by
`internal/forge/adopt_action_source_test.go`, which runs the build step's checkout with the real
git against a repository served over `file://`.

## Migration: explicit sources for missing lockfiles

Live adoption no longer creates the old placeholder lock. Pass
`--lock-source-root=/path/to/praetor` (MCP: `source_root`) when a target has no
valid lock. The source must have a valid manifest/lock and every selected local
archetype source. Digests come from actual source bytes. The MCP source path obeys
server root confinement.

### Lock version: the source catalog's identity

A built lock's `pinned_version`, and every entry's `version`, name the source catalog
rather than copying the source lock's own `pinned_version`:

```yaml
pinned_version: "v0.0.0+catalog.<12 hex digits>"
```

The hex digits start the aggregate digest over every archetype and facet under the
source's `.config/archetypes`, computed as a lock's top-level digest is, but over the
whole catalog instead of the target's selection. The source lock is still validated
first; its declared version is not trusted because nothing moves it when the catalog
changes. So:

- re-pinning against changed catalog content writes a new version, even when the
  target's own digests are unchanged;
- the same catalog content writes the same version, wherever the source lives;
- a source whose `.config/archetypes` defines nothing fails lock generation instead of
  writing a version it cannot back.

`internal/config/lockbuild_test.go` pins all three. `praetorctl init` renders its
unreleased build versions (`v0.0.0+<revision>`) through the same
`config.UnreleasedLockVersion` in `internal/config/lockbuild.go`. The revision is the VCS
stamp of a checkout build or, for `go install .../cmd/standardsctl@<commit>`, the commit
that build's pseudo-version ends with; `go install ...@<tag>` pins the tag itself
(`buildid.Identify` in `internal/buildid/buildid.go` resolves the identity, tested in
`internal/buildid/buildid_test.go`; `lockPin` in `cmd/standardsctl/buildidentity.go` turns it
into the pin, tested in `cmd/standardsctl/buildidentity_test.go`). An existing lock is
not rewritten for its version alone: validation compares digests, not versions, so the
catalog version arrives with the next rebuild (`praetorctl profile set`, `--force`, or a layout-only re-pin).

An existing valid target lock is preserved. An invalid lock fails adoption unless both
`--force` and an explicit source permit rebuilding it; `praetorctl profile set` rebuilds the
lock alone from an explicit source. A dry run without a source
reports lock generation as skipped; it cannot promise a complete adoption. Live
errors retain the partial report, since earlier scaffolding may already exist.
The same source option applies to `adopt --all-missing`; integrations invoking
adoption must supply it or arrange an already valid target lock.

## Migration: the Go API compatibility gate

Only a Go repository that declares `api:public-contract`, one whose `go.mod` git tracks at the
root or nested, fails `praetorctl audit` until `praetorctl adopt` writes
`tools/apicompat/gate/main.go` and `.github/workflows/praetor-api.yml`; its rendered ruleset then
requires **Go API Compatibility**. A repository declaring the facet without a tracked `go.mod`
needs no change: adoption writes nothing for the facet, and audit prints an `[INFO]` line saying
that no API compatibility checker runs for its languages. A repository pinning the earlier
`facets/api-public.yaml` re-pins it with `praetorctl adopt --force`. Remove the facet to opt out
([Go API compatibility gate](guides/api-compatibility.md#repositories-without-go)).

## Migration: `--force` keeps an operator-owned Paperclip harness

`praetorctl adopt --force` no longer regenerates an operator-owned `.paperclip/harness.json` or
recreates a deleted `.paperclip/rules.md` (#502). It keeps the harness, resets only a `platform`
that names another repository to this repository's, and fails on a harness that does not
validate, as a plain run does. To regenerate the harness, delete `.paperclip/harness.json` (and `rules.md`) and rerun
`praetorctl adopt`.

Adoption does not re-bind `register.sources` to a harness you edited, under `--force` either.
A declared contract that no longer matches stops the run before its first write with
`existing register.sources fails its configured gate`. The error names every value that
differs, extracted from the files as they stand, and two remedies:

1. Keep the edit: set `expected`, `not_applicable` and `sha256` under `register.sources` in
   `.standards.yaml` to the extracted values the error reports, then rerun
   `praetorctl adopt --force --lock-source-root=<praetor checkout>`. `praetorctl caveman check --configured-sources --root=.` reports
   the same values once every input is staged with `git add`; it refuses untracked inputs.
2. Drop the edit: delete `.paperclip/harness.json` and rerun `praetorctl adopt`. This works only
   when adoption writes a harness: with `adoption.decline: [paperclip]` or an unresolved
   repository identity it writes none, so restore the bound bytes with `git checkout` instead,
   as the error then says. A harness
   adoption writes where none existed is Praetor output. When every `register.sources` input
   selects `.paperclip/harness.json`, as the rows adoption declares do, adoption binds the pins
   to the harness it writes and reports `Re-bound register.sources to the Paperclip harness
   this run writes where none existed`. That includes pins an earlier release bound to the
   harness it wrote. A contract that also selects another file keeps its gate, because its one
   digest cannot tell that file's drift from the new harness. The error then reports values
   that cover the harness the run would write: set them and rerun.

Tests: `TestAdoptEditedHarnessFailsBeforeWritingWithRemedy` in `internal/adopt/adopt_test.go`,
`TestAdoptDeletedHarnessRebindsPinsAcrossReleases` and
`TestAdoptAbsentHarnessKeepsGateOfMixedContract` in
`internal/adopt/absent_harness_rebind_test.go`, and
`TestAdoptForceEditedHarnessNeedsRecomputedPins` in
`cmd/standardsctl/audit_paperclip_force_test.go`. The harness rules in full:
[text register](guides/text-register.md#upgrading-an-adopted-repository).

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
  The effective policy cannot resolve either, so sync neither checks nor writes
  `.github/rulesets/main.json` and counts that one cause once.
- `praetorctl plan` and the MCP `standards_plan` tool fail to resolve the policy and print none;
  `--catalog-root` (MCP: `catalog_root`) selects a catalog.
- `praetorctl adopt` records the outcome with a warning; `--lock-source-root` verifies against that bundle.
- `praetorctl harvest onboard` completes with `lock_verified: false` and `lock_status: unverifiable`.
- Lock generation refuses a source bundle without a catalog, or with one that defines no archetypes.

Generated locks omit `generated_at`; a lock that sets it still validates. The
behavior is pinned by `internal/config/lock_test.go`,
`cmd/standardsctl/lockdigest_test.go`, `cmd/standardsctl/sync_validation_test.go`,
`internal/adopt/lock_test.go` and `internal/harvester/onboard_safety_test.go`.
