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

# Force overwrite existing configurations & record technical debt
praetorctl adopt --force --record-baseline --lock-source-root=/path/to/praetor
```

### Dry-run ruleset preview

`adopt --dry-run` writes nothing. For the branch protection ruleset
(`.github/rulesets/main.json`), it prints what a real run would do and what the file would contain
(`AdoptReport.Previews`, `internal/adopt/preview.go`). The MCP `standards_adopt` tool prints the
same preview text (`adopt.FilePreview.Text`, `TestFormatAdoptMCPResultPrintsPreviews`):

| Action | Meaning | Printed |
| :--- | :--- | :--- |
| `create` | no ruleset yet; the run writes one | the rendered ruleset |
| `update` | the run replaces the ruleset: the rendering current before the run, which it refreshes, or any differing ruleset under `--force` | unified diff from the file on disk to the rendering |
| `unchanged` | the ruleset already is the rendering, line endings aside | nothing more |
| `keep` | the ruleset differs and stays, because `--force` was not passed | the diff `--force` would apply |

The preview takes its action from the same keep-or-replace decision as the real run
(`internal/adopt/preview_test.go`). Its status checks come from the workflows the run leaves
before its `branch-ruleset` step, not only the ones on disk:

- workflows the dry run records as written or removed, such as the documentation gate's;
- the workflows of the flavor the run applies for the profile it records
  (`flavor.PlannedWorkflows`), which a dry run does not apply.

On a first adoption the preview is therefore the file the run writes, byte for byte
(`TestAdoptDryRun_Positive_FirstAdoptionPreviewIsTheWrittenRuleset`).

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

`--force` replaces any kept ruleset.

A ruleset rendered for a policy the repository no longer declares is kept too, for example after
`.standards.yaml` overrides change. `praetorctl sync` then reports it as drift. Pass `--force` to
adoption or `flavor apply`, or delete the file and run `sync`, which writes the missing ruleset.
Keep a hand-managed ruleset with `adoption.decline: [branch-ruleset]`.

### Large repositories

Adoption discovers verification inputs (Makefiles, manifests, scripts) through a bounded
walk of the target: 4096 directory entries, 128 files and 32 levels of depth by default. A
repository above those bounds fails with an error that names the flag raising the bound:

```text
verification discovery exceeds 4096 entries; raise max_entries with --verification-max-entries, up to 200000
```

Raise a bound explicitly instead of trimming the tree:

```bash
standardsctl adopt --dry-run --path /path/to/large-repo \
  --lock-source-root=/path/to/praetor --verification-max-entries=32768
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
praetorctl paperclip harness --path /path/to/large-repo --verification-max-entries=32768
```

Tests: `internal/adopt/large_repo_bounds_test.go` and
`TestPaperclipHarness_VerificationLimitFlags` in `cmd/standardsctl/adopt_limits_test.go`.

### What Adoption Scaffolds Automatically

1. **`.standards.yaml`**: Declarative repository manifest containing profile, facets, tool versions, and policy locks.
2. **`.standards.lock`**: Cryptographic SemVer lockfile binding your repo to exact governance standard releases.
3. **`.standards-baseline.json`**: Technical debt ratcheting baseline. Existing infractions (e.g. legacy loop bounds, unwrapped errors) are recorded so legacy code compiles while new code is strictly gated.
4. **`AGENTS.md` + 6 Vendor Targets**: Canonical agent operating harness transpiled to `CLAUDE.md`, `.cursor/rules/*.mdc`, `.github/copilot-instructions.md`, `.windsurfrules`, `.gemini/GEMINI.md` and `.codex/rules.md`. `agent_clients` in `.standards.yaml` limits these to the clients the repository uses ([agent client selection](guides/editor-capabilities.md#selecting-agent-clients)).
5. **`.devcontainer/devcontainer.json`**: Multi-architecture container configuration pinned to verified base images.
6. **Multi-IDE Configs**: Workspace settings for every supported editor, or only the ones `editors` in `.standards.yaml` names ([editor selection](guides/editor-capabilities.md#selecting-editors)).
7. **Makefile & LeftHook**: Automated pre-commit hooks and standard verification targets (`make verify-all`).

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
  (`TestAdopt_DeclaredProfileGovernsAdoption`).
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
  warning, and the file is kept. `--force` regenerates only the scaffolds it owns
  (`TestScaffoldFile_ReportsDriftInsteadOfVerified`).
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
  that changes even one value keeps failing until `--force`
  (`TestAdoptDoesNotRepinAnEarlierCatalogToChangedValues`). An earlier text is recognised in
  either consistent line-ending style, so a CRLF checkout of the manifest or label taxonomy
  (`core.autocrlf` on Windows) is refreshed too and keeps CRLF
  (`TestAdoptMigratesACRLFEarlierManifestInItsOwnStyle`,
  `TestReconcileLabels_Positive_RefreshesCRLFPriorInItsOwnStyle`). The catalog is the
  exception: `.standards.lock` pins the exact LF bytes of each file, so a CRLF checkout of it
  fails lock verification before and after this refresh; keep `.config/archetypes` at
  `eol=lf` in `.gitattributes`. An edited copy of any of them, or one with mixed line endings,
  is left as it is and keeps the contract above
  (`TestAdoptDoesNotRepinAnEditedOrForeignCatalog`).

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

The tool returns a detailed summary of created and reconciled files, detected archetypes, and recorded legacy debt.

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

`standardsctl` itself still comes from the praetor ref the `uses:` line pins, not from the pull
request (see below). That is why the example accepts a head pushed to a fork: the adoption reads
the fork's files but never builds or executes them. A job that builds or runs the pull request's
code has to refuse fork heads, as praetor's own adopt-comment job does.

### Praetor's own adoption workflow

`.github/workflows/adopt.yml` is the hardened form of the example, split into one job per
entry point:

| Job | Trigger | Token | What it does |
| :-- | :-- | :-- | :-- |
| `adopt` | `workflow_dispatch` | `contents: write`, handed only to the commit step | adopts `target_path` (default `.`), runs the HISS-13 debt ratchet on it, then commits with a DCO sign-off and pushes unless `dry_run` is set; keeps its own Go build cache through `.github/actions/go-cache` (`job: adopt`) |
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
`target_path` reaches both the adoption and the ratchet through env, only the dispatch job keeps
a Go cache and stays visible to `AuditGoBuildCaches`, every job and the head lookup run under an
explicit `timeout-minutes`, and the head lookup's own shell body is executed against a stub `gh`,
which is how a fork head is shown to be refused.

### Inputs, the binary, and the `report` output

| Input | Reaches | Effect |
| :-- | :-- | :-- |
| `path` | `PRAETOR_PATH` | `--path=<value>`, and the `--source`/`--target-dir` of the `compile-context --verify` that follows an adopt run; an empty value is refused before anything runs |
| `mode` | `PRAETOR_MODE` | selects the subcommand, `adopt` or `dogfood`; any other value is refused |
| `dry-run` | `PRAETOR_DRY_RUN` | `--dry-run=<value>`; in `dogfood` mode it changes nothing, because `dogfood` applies adoptions only to `--targets` repositories (`testTargetAdoptions` in `internal/dogfood/dogfood.go`) and the action passes none, so the host is audited either way |
| `force` | `PRAETOR_FORCE` | `--force=<value>`, adopt only |
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
`cmd/standardsctl` out of the praetor checkout that `GITHUB_ACTION_PATH` points into, so
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
shell bodies against a stub binary and reads the input defaults out of `action.yml`.

## Migration: explicit sources for missing lockfiles

Live adoption no longer creates the old placeholder lock. Pass
`--lock-source-root=/path/to/praetor` (MCP: `source_root`) when a target has no
valid lock. The source must have a valid manifest/lock and every selected local
archetype source. Version pins come from that validated bundle, and digests come
from actual source bytes. The MCP source path obeys server root confinement.

An existing valid target lock is preserved. An invalid lock fails unless both
`--force` and an explicit source permit rebuilding it. A dry run without a source
reports lock generation as skipped; it cannot promise a complete adoption. Live
errors retain the partial report, since earlier scaffolding may already exist.
The same source option applies to `adopt --all-missing`; integrations invoking
adoption must supply it or arrange an already valid target lock.

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
- Lock generation refuses a source bundle without a catalog.

Generated locks omit `generated_at`; a lock that sets it still validates. The
behavior is pinned by `internal/config/lock_test.go`,
`cmd/standardsctl/lockdigest_test.go`, `cmd/standardsctl/sync_validation_test.go`,
`internal/adopt/lock_test.go` and `internal/harvester/onboard_safety_test.go`.
