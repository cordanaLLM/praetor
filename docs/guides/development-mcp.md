# Develop against this checkout's MCP

Run from the repository root with Python 3, Git, and the Go version required by
`go.mod` available on `PATH`:

```bash
python3 scripts/dev_mcp.py probe
```

This builds the current Go sources into a temporary binary under the checkout's
git-ignored `bin/`, initializes its real stdio MCP transport, and checks
`serverInfo.version` against `provenance.server_version` (`dev-<source_sha256>`). The
build stays inside the checkout so the
[engine build check](workstation-update.md#engine-build-check) accepts its context writes
even when an untracked file stamps it `-dirty`. The report includes the checkout,
source and binary hashes, Git revision, dirty state, and Go version. Keep that
provenance with defect evidence. A source change during the build or probe fails
the run; retry against the completed edit.

The probe checks checkout symbol inspection, canonical context compilation with
all six outputs read back, verification drift, invalid arguments, path escape,
unknown tools, corrupt memory, and malformed locks. It also audits a valid fixture
before checking changed pinned content, invariant violations, and incomplete scans
that must fail without changing the baseline. All mutations use disposable
fixtures. A discovered tool name alone does not prove working behavior.

## Native project connections

The MCP server is client-neutral at the wire boundary. Client configuration and
lifecycle behavior are separate acceptance surfaces. The supported projections
are Codex, Claude, Gemini, OpenCode v1, Continue, Cline, Kilo, and AGY; see
[agent lifecycle coverage](agent-lifecycle.md) for their current qualification
states. A projection test or checked-in client file does not prove that an
installed client loaded, trusted, reconnected, or used the server.

Refresh the regular commands on this workstation with `make dev-install`.
`make build` writes only to the checkout's `bin/` directory; it does not update
standalone executables already on `PATH`. `scripts/dev_install.py` runs the MCP
behavior probe against a fresh build, then delegates the atomic install itself to
`praetorctl workstation install` (HISS-19: one installer, not two): it builds all
three Praetor binaries from current source and installs them into `~/.local/bin`
with the three legacy aliases. With no tracked file modified, current source is a
clean clone of HEAD, so untracked files stay out of the build; see
[install step 4](workstation-update.md#workstation-install). `python3 scripts/dev_install.py --help` lists
destination overrides for isolated installations.

`workstation install` rejects an unexpected symlink or special file at an
installation target before touching anything, backs up whatever is already
there, reads installed hashes back, and restores on a caught installation
error. It serializes concurrent installers through a lock directory in the
destination and records the install (including the prior commit and backup
location, once there is one) in a per-user install manifest. See
[workstation install and status](workstation-update.md) for the manifest
location, the lock and rollback behavior, and `workstation status`.
Already running CLI/MCP processes continue using their original executable until
restarted. The development connection below still builds directly from source.

The tracked configs (`.codex/config.toml`, `.mcp.json`, `.gemini/settings.json`)
register `praetor-dev`; they contain no machine-specific paths or credentials. All three
run `scripts/dev_mcp.py serve`, which builds once per server startup. Existing native connections keep that binary after source edits. Restart
or reconnect the server before testing the new code, or use the fresh direct
commands below. Check the server's initialization version against a fresh probe's
`provenance.server_version`; a matching Git commit alone misses uncommitted edits.

**Codex:** open this trusted checkout in Codex. Its project `.codex/config.toml`
registers the server with 120-second startup and tool timeouts. The launcher resolves
the Git worktree root from the session directory, so nested starts work. Verify the
loaded configuration with `codex mcp get praetor-dev --json`; use `/mcp` to inspect
the connection. Restart the CLI session or IDE extension to load a new server.
Project config and these MCP options are documented in the
[Codex MCP reference](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

**Claude Code:** open the checkout and enable its project `.mcp.json` server when
Claude presents the project approval. Check `claude mcp get praetor-dev` and use
`/mcp` to reconnect after edits. The launcher expands `CLAUDE_PROJECT_DIR` inside
the child shell; this documented server environment variable avoids depending on
Claude's launch directory. Allow cold Go builds with `MCP_TIMEOUT=120000 claude`;
the checked-in per-tool timeout is 120 seconds. See the
[Claude Code MCP reference](https://code.claude.com/docs/en/mcp).

Configuration parsing was checked with Codex CLI 0.145.0 and Claude Code 2.1.259.
Codex's real app-server tool-call path was checked from the repository root and a
nested directory. Claude's config was recognized as project-scoped, and its exact
launcher completed a handshake and symbol read from an unrelated directory with
the documented child environment. Its native connection remains subject to
Claude's project approval. If a client cannot load the project connection, record
that limitation and use the direct wire client.

**Gemini CLI:** start Gemini in the checkout root. Its project `.gemini/settings.json`
registers the server as `python3 scripts/dev_mcp.py serve` with a 120-second request
timeout. Gemini reads project settings only from the directory it starts in and launches a
stdio server there when no `cwd` is set, so the relative path always names this checkout's
script. Check the loaded server with `gemini mcp list` and use `/mcp` to inspect the
connection. See the [Gemini CLI MCP reference](https://geminicli.com/docs/tools/mcp-server/).

**Other supported clients:** use `praetorctl clients prepare` or the documented
native plan for OpenCode v1, Continue, Cline, Kilo, or AGY. Inspect the
exact destination or argv, complete that client's approval/reload flow, and
perform the initialization, discovery, and harmless readback checks. Existing
projection tests establish serialization and conflict handling; they are not
live session evidence.

Gemini's checked-in `.gemini/settings.json` also contains the native
`BeforeTool`/`AfterTool`/`AfterAgent` lifecycle hooks. Claude's
`.claude/settings.json` contains the corresponding `PreToolUse`/`PostToolUse`/
`Stop` hooks. OpenCode v1, Continue, Cline, Kilo, and AGY have no checked-in
Praetor lifecycle adapter; their MCP projection does not imply lifecycle
enforcement. Cursor, Windsurf, and Copilot have compiled context support only.
See [agent lifecycle coverage](agent-lifecycle.md) for the complete matrix.

## Reproduce through a real tool

Each direct call fresh-builds the checkout, verifies the server identity, and
prints its JSON-RPC response with provenance. Tool errors and protocol errors
produce a nonzero exit code:

```bash
python3 scripts/dev_mcp.py call standards_inspect_symbols \
  '{"path":"cmd/standards-mcp/main.go"}'
```

Use a temporary server root for mutation cases. `--root` follows the subcommand
and changes the tool's confined filesystem root; the code still comes from this
checkout. This example checks a real write, readback, and verification:

```bash
python3 - <<'PY'
import json
from pathlib import Path
import subprocess
import tempfile

with tempfile.TemporaryDirectory(prefix="praetor-mcp-case-") as directory:
    root = Path(directory)
    (root / "AGENTS.md").write_text("# Fixture\n\nPreserve fixture-policy.\n")
    command = ["python3", "scripts/dev_mcp.py", "call", "--root", directory,
               "standards_compile_context"]
    subprocess.run(command + ["{}"], check=True)
    assert "fixture-policy" in (root / "CLAUDE.md").read_text()
    subprocess.run(command + [json.dumps({"verify_only": True})], check=True)
PY
```

The writing call also splices the [text register](text-register.md) block into the
fixture's `AGENTS.md`, rendered from the manifest beside it, before the vendor files are
compiled. A `verify_only` call never writes the source: it reports a stale or missing block
as `Context verification failed`, and `standards_audit` reports it in its context gate.
Both also run the caveman lint over `AGENTS.md`, as `praetorctl compile-context --verify`
does: prose there fails with `AGENTS.md fails the caveman lint` and the first findings, and a
pass names its counts (see [the context gate](text-register.md#the-context-gate)). The
writing call runs the same lint after it writes and returns an error on a finding. The
fixture above is terse, so it passes.

The block sends agent evidence to `.workingdir/evidence/`. When the root is a Git work tree
whose rules do not ignore that directory, the writing call merges the Praetor private-artifact
block into `.gitignore` first and reports it, and a `verify_only` call fails with
`git does not ignore .workingdir/evidence/` (see [evidence](text-register.md#evidence)). A
`mktemp -d` root outside any work tree needs neither.

The same manifest selects which vendor files exist. `agent_clients: [claude]` in the
fixture's `.standards.yaml` makes the writing call compile only `CLAUDE.md` and list the
other five as `[NOT_APPLICABLE]`; a `verify_only` call then reads only `CLAUDE.md`. An
unknown id fails both calls with `unknown agent client id(s)`. The rule is described in
[editor and agent client selection](editor-capabilities.md#selecting-agent-clients).

A read-only tool needs no temporary root. For example, this call lists each client's
subagent text states:

```bash
python3 scripts/dev_mcp.py call standards_client_capabilities '{}'
```

Each client entry reports `brief_capture`, `return_capture` and `register_enforcement` as
separate states. Each state carries its definition paths and an `activation` that stays
`unverified` until a payload recorded from the installed native client proves it. The
[subagent text register gate](agent-hooks.md#subagent-text-register-gate) explains which
clients are `adapter-defined` and which are `unenforceable`.

For each defect, retain the input, expected behavior, actual response, provenance,
and filesystem evidence. Exercise positive, negative, and boundary behavior of the
affected tool. Report placeholder results, missing operations, and blocked
connections explicitly; the smoke probe does not establish correctness of every
tool. Run the relevant code tests and `make verify-all` after implementing the fix.

### Tool arguments and message framing are strict

`tools/call` refuses every argument key the tool's input schema does not declare,
before the handler runs, so a refused call writes nothing. A misspelled preview
flag is an error, not a real run with the flag at its default:

```bash
python3 scripts/dev_mcp.py call --root "$(mktemp -d)" standards_compile_context \
  '{"verifyonly": true}'
```

The refusal is a tool result with `isError: true` that names the undeclared keys
and the declared ones, and the command exits nonzero:

```text
mcp: argument not declared by the tool input schema: "verifyonly" (declared: source, target_dir, verify_only)
```

Every schema in `tools/list` publishes `"additionalProperties": false` to match.
The check is `ToolInputSchema.CheckArguments` in `internal/mcp/tool.go`, called by
`runTool` in `cmd/standards-mcp/server.go`. The quoted keys are client input, so
the refusal passes `mcp.SanitizeResult` like every handler result: a role
delimiter or override phrase in a key is served neutralized. Tests:
`TestServer_Negative_UndeclaredArgumentRefusedBeforeHandler` and
`TestServer_Negative_MisspelledPreviewFlagWritesNothing` in
`cmd/standards-mcp/server_test.go`;
`TestServer_Negative_InjectedUndeclaredKeyNeutralized` and
`TestServer_Boundary_ManyInjectedUndeclaredKeysNeutralized` in
`cmd/standards-mcp/served_sanitize_test.go`.

Every transport reads a message as JSON-RPC 2.0 and answers it as follows:

| Message | Answer |
| :--- | :--- |
| Not JSON | `-32700`, `"id": null` |
| JSON but not one request object: a batch array, a scalar, `null` | `-32600`, `"id": null` |
| `id` that is `null` or neither a string nor a number | `-32600`, `"id": null` |
| `jsonrpc` missing or not `"2.0"`, `method` missing or not a string | `-32600` with the message's `id` |
| No `id`: a notification | nothing, and the method does not run; plain HTTP answers `202 Accepted` |
| Unknown method with an `id` | `-32601` |

Member names are case-sensitive, so `{"JSONRPC": "2.0", ...}` has no `jsonrpc`
member. A numeric `id` is echoed exactly, `12345678901234567890` included. Plain
HTTP answers a `-32700` or `-32600` with status 400. The code is `decodeRequest`
in `cmd/standards-mcp/transport.go` and `validateEnvelope` in `server.go`. Tests:
`TestServer_Negative_EnvelopeValidation`,
`TestServer_Boundary_NotificationsGetNoReplyAndRunNothing`,
`TestStdio_Negative_InvalidMessagesCarryNullOrReadableID`,
`TestStdio_Boundary_NotificationsSilentAndIDsEchoedExactly` and
`TestHTTP_Negative_InvalidEnvelopeAndNotification`.

### Symbol inspection judges against the repository's ceilings

`standards_inspect_symbols` prints each Go function's lines, statements,
cyclomatic and cognitive complexity beside the ceiling it is judged against, as
`LOC: 43 (<=60)`. A function over the length ceiling, which the audit enforces,
is marked `HISS-04 WARN: LOC`. A function over a complexity ceiling is marked
`HISS-04 REPORT: <kinds>` and followed by the `[REPORT]` lines the audit prints:
the scanner's own measurement (`hiss.MeasureDecl`), reported and never enforced
(see [HISS-04: Go complexity is measured, not enforced](../standards/hiss-rule-matching.md#hiss-04-go-complexity-is-measured-not-enforced)).
A function literal bound to a package variable is listed as `Func literal`.
The ceilings come from `config.ResolveRepositoryComplexity`
(`internal/config/repository_policy.go`) for the server root, the resolver
`praetorctl editors` and `standards-lsp` also use, so the three report the
ceilings `praetorctl audit` enforces in that repository:

- A locked repository reports its resolved policy: pinned profiles, repository
  overrides and the audit function-length cap (`config.AuditMaxFuncLOC`, 60).
  That can be looser or tighter than the HISS-04 figures in `AGENTS.md`.
- No `.standards.yaml`, or a manifest without `.standards.lock`, reports the
  HISS-04 ceiling (cyclomatic 10, cognitive 15, statements 50, 60 lines),
  tightened by any complexity override the manifest declares.
- Every 60 above is `hiss.DefaultMaxFuncLOC` (`internal/hiss/hiss.go`), the
  scanner's own default and the one constant every function-length default
  derives from; `TestServer_Boundary_InspectSymbolsLengthIsScannerDefault` in
  `cmd/standards-mcp/server_test.go` pins the locked and unadopted cases to it.
- A manifest or lock that does not resolve, including the lock `praetorctl init`
  writes, reports that same ceiling and opens with
  `[WARN] repository policy unresolved (<cause>); stating the HISS-04 ceiling ...`.
  The inspection still runs; `praetorctl audit` still fails on that state.
- Policy resolution fails the tool only when the request is cancelled or the
  resolution times out: `Inspection failed: resolve complexity policy for <root>: ...`.

[Complexity ceilings](editor-capabilities.md#complexity-ceilings) explains why the
lock-less case differs from `praetorctl plan`. Tests:
`TestServer_Boundary_InspectSymbolsFollowsRepositoryPolicy` and
`TestServer_Negative_InspectSymbolsUnresolvablePolicy` in
`cmd/standards-mcp/server_test.go`.

`standards_plan` opens with `=== Praetor Reconcile Plan (Dry Run) ===` and takes
the `Repository: <owner>/<name>` line from the manifest's `repository` block
(`TestWritePlanHeader_NamesTheManifestRepository`). Its target invariants come from
`config.ResolveRepositoryPolicyFromCatalog`, the resolver `praetorctl plan` uses
(`createPlanTool` in `cmd/standards-mcp/server.go`), and its drift lines come from
`adopt.PlanDrift` and `adopt.FormatPlanStatus` (`internal/adopt/plan_drift.go`), the same code
`praetorctl plan` prints:

- A locked repository shows the pinned profiles and facets joined with the
  manifest's overrides, which is the policy `praetorctl adopt` writes the branch
  ruleset from. Its drift list follows that policy, so a profile that requires an
  SBOM reports `.github/workflows (no workflow generates an SBOM: …)` when no workflow
  generates one (`adopt.SBOMWorkflowDrift`, pinned by
  `TestServer_Positive_PlanShowsThePinnedProfilePolicy`). Any workflow step that writes an
  SBOM satisfies that requirement, including a GoReleaser release whose configuration declares
  `sboms`; a file named `sbom.yml` that runs no generator does not, and neither does a step
  that runs only `praetorctl sbom notices`, which rewrites `THIRD-PARTY-NOTICES.md` and writes
  no SBOM (`internal/forge/sbom_workflow_test.go`).
- A manifest without `.standards.lock` shows the built-in defaults plus the
  overrides and opens with `[INFO] no .standards.lock: built-in defaults and
  repository overrides only`
  (`TestServer_Boundary_PlanOverridesAfterTheJoinAndWithoutALock`).
- A lock that does not resolve, for example a profile edited after it was pinned,
  fails the tool with `Failed to resolve plan policy: ...` and prints no policy
  (`TestServer_Negative_PlanRejectsAnUnverifiableLock`).
- A pinned catalog that is not materialized under the server root resolves through
  the optional `catalog_root` argument, the confined catalog selection
  `standards_audit` takes; a blank value means the server root and a path outside
  it is refused (`TestServer_Boundary_PlanResolvesThroughTheSelectedCatalog`).

Each target invariant follows as one `- <field>: <value>.` line, for example
`- review_mode: single_maintainer.`

### Tool text is a counted runtime source

Every tool description, property description and result callsite in
`cmd/standards-mcp` is part of `register.sources` in `.standards.yaml`
(`mcp.descriptions` and `mcp.outputs`). Results go through the helpers in
`cmd/standards-mcp/mcp_runtime_text.go`: a static template is linted as agent text,
and runtime-owned text (structured JSON, untrusted passthrough, protocol bytes) is
classified and bound by count and digest instead. A bare `mcp.TextResult` with
dynamic text, direct `mcp.ToolResult` construction or a post-construction mutation
fails the extractor.

Adding, removing or rewording a tool string therefore changes the contract. Update
`expected`, `not_applicable` and `sha256` from
`praetorctl caveman check --configured-sources --root=.`, and the callsite count
and identity digest pinned by `TestMCPRuntimeOutputsHaveNoUnclassifiedCallsites`
in `cmd/standards-mcp/runtime_text_coverage_test.go`. That test recounts the
callsites with an independent AST oracle
(`cmd/standards-mcp/runtime_output_oracle_test.go`) and fails when it disagrees
with the production extractor. The form rules are in the
[text-register guide](text-register.md#tracked-runtime-sources).

### Shared audit authority and parity

The `standards_audit` tool executes the same gates as CLI `standardsctl audit`.
A failing HISS ratchet reports `[FAIL]` followed by `baseline.RatchetResult.Summary`, the
rejection text `praetorctl audit` and the gate print: up to three violations per class by rule,
file and line with a count of the hidden rest, or both totals when only the count rose. Like
`praetorctl audit`, the tool first attributes each unbaselined violation against the baseline's
commits, `commit_sha` and the last commit that changed the baseline file (`hiss.AttributeRatchet`),
so a finding in code unchanged since then is tagged as coming from a changed check, not as new
([A HISS rejection names the violations](adoption-verification.md#a-hiss-rejection-names-the-violations)).
The tool takes no listing flag; `praetorctl baseline --verify --all-violations` prints every
violation.
Both tools share the same authority implementations for artifact checks. When
verifying branch protection rulesets, `standards_audit` consults
`adopt.AuditBranchProtectionWithPolicy` with the effective policy it resolved
(profiles, facets and overrides joined), the policy adopt and sync render the ruleset
from. If `.standards.yaml` records an accepted
`adoption.decline: [branch-ruleset]`, `standards_audit` reports a verified pass
(`[PASS] Branch protection ruleset declined by adoption.decline.`) rather than
failing on the absent `.github/rulesets/main.json`. When not declined and required
by policy, `standards_audit` fails closed if the ruleset file is missing or its
content differs from the ruleset the declared policy renders for the repository's
default branch (`forge.ValidateRepositoryRuleset`, branch from
`forge.RepositoryDefaultBranch`), matching CLI behavior with byte-for-byte verdict
parity (`cmd/standards-mcp/audit_branch_ruleset_test.go`).

The context, label taxonomy and hook configuration gates read `adoption.decline` through the
reader the CLI gates use, `adopt.AuditDecline`, and the label and hook gates are the CLI's own
(`adopt.AuditLabelTaxonomy`, `adopt.AuditGitHookConfig`). A declined `labels` or `git-hooks`
step passes with the decline named; a declined `agent-harness` step still requires the register
block, the projections and the caveman lint, and a failure says so
([declined steps](adoption-verification.md#what-audit-does-with-a-declined-step),
`cmd/standards-mcp/audit_decline_test.go`).

The lock digest gate inside `standards_audit` resolves its catalog from the same
`catalog_root` tool argument the effective-policy gate uses
(`p.policy.CatalogRoot` in `cmd/standards-mcp/audit_tools.go`), not the repository
root by default. It calls `config.ValidateLockfileWithOptions` with
`RequireSources: true`, so a lockfile with no catalog to hash against fails the
tool call (`[FAIL] lockfile content digests are unverifiable: ...`,
`config.ErrLockUnverifiable`) instead of reporting a pass. A catalog that exists
but no longer defines a pinned archetype's `id:` fails the same way with
`config.ErrLockSourceMissing`. See
[lock verification outcomes](../adoption.md#lock-verification-outcomes) for the
full outcome table shared with the CLI.

### Adoption and version-audit parity

`standards_adopt` and `praetorctl adopt` render one outcome and one set of
governance pillar lines, both derived from the adoption report
(`AdoptReport.Outcome` and `AdoptReport.Pillars` in `internal/adopt/report.go`).
A dry run that recorded an error is reported `[INCOMPLETE]`, not
`[SIMULATED (DRY RUN)]`, and a pillar whose step warned, failed (by returning an
error or by recording one), was declined or never ran names that status instead
of a success mark. A dry run against a fresh
repository without `source_root` shows both: the DevContainer pillar reads
`warned` and the result is `[INCOMPLETE]`. `standards_adopt` and `praetorctl adopt`
also list a file that `force` overwrote in its own replaced section, with its line
delta and backup location, and never among the reconciled files (`formatAdoptFiles` in
`cmd/standards-mcp/tools_adoption.go`, `TestFormatAdoptMCPResult_Positive_ReplacedSection`).
The `force` and `source_root` property descriptions restate, in the agent register, the
`--force` contract `adopt --help` prints: what `force` rewrites, merges and keeps, and that it
needs `source_root`, a dry run included
([what a forced re-adoption changes](../adoption.md#what-a-forced-re-adoption-changes),
`TestCreateAdoptTool_ForceStatesTheContract` in
`cmd/standards-mcp/tools_adoption_force_test.go`).

A dry run also prints the branch ruleset preview the CLI prints: the action
(`create`, `update`, `unchanged` or `keep`), its note, and the rendered ruleset or
the diff. Both print `adopt.FilePreview.Text` (`internal/adopt/preview.go`;
`TestFormatAdoptMCPResultPrintsPreviews` in
`cmd/standards-mcp/report_renderers_test.go`). The
[adoption guide](../adoption.md#dry-run-ruleset-preview) explains the actions.

The `facets:` line names the facets the run applies, the declared ones for an existing
`.standards.yaml`. When a first adoption falls back to the default facets, each
`AdoptReport.FacetNotes` line follows it as `facet note: <note>`, the notes the CLI prints under
`Facets:` (`TestFormatAdoptMCPResultPrintsFacetNotes`). The
[adoption guide](../adoption.md) shows them under 1-Step CLI Adoption.

`standards_version_audit` lists the workflow-action inventory that
`praetorctl bump audit` prints, rendered by the same
`bump.FormatActionsInventory`, and counts it in its summary line
(`TestFormatVersionAuditListsActions` in
`cmd/standards-mcp/report_renderers_test.go`). The summary line ends with
`passed:`, and a report that failed, one holding any deprecation, comes back as
an error result, the verdict on which `bump audit` exits non-zero
(`TestVersionAuditResultFollowsPassed`). The audit queries upstream
registries, so exercise it through a real call only where network access is
intended.

### Context compilation parity

`standards_compile_context` runs the code `praetorctl compile-context` runs:
`compileContext` in `cmd/standards-mcp/server.go` calls
`compiler.VerifyCompiledContext` for `verify_only` and
`compiler.CompileContextProjections` for a write (`internal/compiler/projection.go`).
A tool call and the CLI therefore check and write the same things:

- A write compiles the vendor files, then copies every persona under
  `.agents/agents` into each selected client's persona directory and, when
  `.agents/plugins/praetor/plugin.json` exists, into the plugin's `agents/` and
  `skills/` copies.
- `verify_only` also runs the caveman lint over every persona and skill and
  fails on a persona or plugin skill copy that differs from its source beyond
  leading and trailing whitespace. It runs every check and returns every failure.
- A write runs the same lint after writing and fails on a finding, and before it
  compiles it makes Git ignore `.workingdir/evidence/`. Both steps are
  `adopt.CompileAgentContext`, the write the CLI's `compile-context` and `init` run.
- More than 50 files in `.agents/agents` fail both modes instead of being
  truncated.
- Every file the call writes below `target_dir` (the vendor files, the
  persona copies such as `.claude/agents/*.md`, and the plugin persona and
  skill copies), and every persona and skill it reads from `.agents`, is
  reached without following a symlink. A symlinked file, or a symlinked
  directory anywhere between `target_dir` and the file (`.claude`, `.agents`,
  `.agents/plugins/praetor`), is refused on write and on verify for the same
  reason, with or without `-allow-outside-root`. That flag still admits a
  `target_dir` outside the root; it no longer lets a link redirect a write.
  The writer itself (`contextopt.WriteSnapshotIn`) creates and opens every
  directory below `target_dir` without following a link, so a link planted
  after the check is refused too.
- A write reads every persona and skill, then checks every file it is about
  to write before it splices the text register into `source` and before it
  writes the first file (`planAgentSurfaces` in
  `internal/compiler/projection.go`). The check applies the writer's own
  refusals: the symlink refusals above, an existing file that is not a regular
  file, and an existing file the writer cannot observe because it is not
  UTF-8 text, holds a NUL byte or exceeds 1 MiB (`contextopt.MaxSourceBytes`).
  Any of those leaves `source`, the vendor files, the persona copies and the
  plugin copies unchanged. The check does not cover an I/O failure during the
  writes themselves, such as a full disk, or a target changed between the
  check and the write: the writer applies the same refusals again when it
  reaches each file, but the files written before a refusal stay written.
- Before a write, both check that the running engine matches the engine checkout they
  write into (`workstation.CheckBuildCurrent`, `internal/workstation/freshness.go`). An
  installed server or CLI built from an older revision refuses the write with
  `engine build does not match this checkout` instead of rendering its own stale text into
  `AGENTS.md` and the vendor files; `verify_only` is never refused. The rules are in
  [the engine build check](workstation-update.md#engine-build-check).

Tests: `cmd/standards-mcp/server_projection_test.go`,
`cmd/standards-mcp/compile_context_engine_test.go`,
`TestCompileContextRejectsSymlinkedOutputDescendants` and
`TestCompileContextWritesRealOutputDescendants` in
`cmd/standards-mcp/server_path_test.go`, `internal/compiler/output_paths_test.go`,
`internal/compiler/projection_test.go` and `internal/contextopt/write_in_test.go`.

`standards_adopt` and `praetorctl adopt` write the vendor files of the selected
clients and the two canonical personas (`repo-auditor.md`,
`repo-gatekeeper.md`) through the same writer, and project the personas with
`compiler.CompileAgentSurfaces`, the persona half of the `compile-context` write,
plugin persona and skill copies included. Before the first adoption step writes
anything, adopt runs the same check over those files and every persona and
plugin copy
(`preflightAgentSurfaces` in `internal/adopt/adopt.go`), so a symlinked
`.agents` or persona directory fails adoption with nothing written, and so does
a symlinked `.github` when copilot is a selected client (its vendor file lives
there). An existing selected vendor file or persona that is not UTF-8 text,
holds a NUL byte or exceeds 1 MiB is refused instead of overwritten. A dry run
runs the check too; a step declined through `adoption.decline` is not checked.
Every other file adoption writes, `AGENTS.md`, the pull request template and the
workflows included, still goes through the older writer (`writeRepoFile`), which
refuses a link that leaves the repository but follows one that stays inside it:
with copilot unselected, a symlinked `.github` fails later in the run, after
files were written behind it.
Tests: `internal/adopt/agent_surface_preflight_test.go`,
`internal/adopt/plugin_projection_test.go`.

`standards_audit` does not run the persona and skill checks yet; see
[the persona and skill gate](text-register.md#the-persona-and-skill-gate).

### Needs reports read operator settings at call time

`standards_needs_report` selects the operator settings on every call, the way
`praetorctl hook` does: `PRAETOR_FLEET_CONFIG` and `PRAETOR_WORKSTATION_CONFIG`, then the
install manifest (`loadNeedsRegistry` in `cmd/standards-mcp/server.go`). It loads the
settings and the targets' contracts through `needs.SelectRegistry`, the loader the
`praetorctl needs` subcommands use (`TestSelectRegistry_3D` in `internal/needs`). A settings
change therefore reaches a running server without a restart. The tool resolves `framework.targets`
and its `framework` argument exactly as `praetorctl needs report` does
(`needs.SelectFrameworkSource`) and prints the same header (`needs.FormatReportHeader`,
including `Deprecated input:` lines). Praetor ships no framework data: with no target
configured the header reads `Framework: not configured (…)` with mapping availability
`n/a (no target framework configured)`. A settings document that fails validation is an error result
(`Failed to load operator settings: ...`), not a report against defaults
(`TestNeedsReportHonoursConfiguredTarget` in `cmd/standards-mcp/operator_settings_test.go`):

```bash
PRAETOR_WORKSTATION_CONFIG=/path/to/workstation.yaml \
  python3 scripts/dev_mcp.py call standards_needs_report '{"path":"."}'
```

See [framework targets](needs-capability-evidence.md#framework-targets) for what a target
changes in a report.

## Retained public dogfood loops

Use the [public dogfooding guide](../dogfooding.md) for the shared CLI/MCP
plan/apply/recheck loop. Public cloning is disabled by default. Explicitly add
`--allow-remote-benchmarks` to `serve` or `call`; a client tool argument cannot
turn on network access. Direct calls can use `--timeout 300` for the bounded
five-minute loop. Keep `artifact_dir` under the confined server root, such as
`.workingdir/evidence/public-dogfood`, and retain the JSON-RPC envelope with its source identity.

For recurring suites, use the [user timer guide](scheduled-dogfood.md).
`standards_dogfood_schedule_status` reads a configured schedule and retained
attempt state. The configured runner binary, suite, source bundle, state, and
repair-policy paths must all fit the server root. Status cannot run suites,
dispatch agents, or create state. It hashes the configured CLI executable so
MCP and CLI status agree even though they are separate processes.

For configured repair execution, use the [repair guide](dogfood-repairs.md).
`standards_dogfood_repair_status` reads a retained suite report and execution state
through a confined private configuration. Probe it with an actual failed suite
report, then verify that status leaves both the report and absent execution state
unchanged. It never invokes credential helpers or providers. Only the explicit
`praetorctl dogfood repairs run` command starts an execution attempt.

The private execution configuration must pin `source_sha` to a commit that exists
in the configured source repository and carry the canonical register tuple and
manifest digest for that snapshot. Repair status rejects stale or caller-selected
provenance before it reports retained execution state.

## Package docs quote their upstream

`standards_package_docs` returns the sheet `praetorctl docs sync` distilled from a declared
package's own documentation (`CompressDocumentation` in `internal/docdistill/compressor.go`).
That documentation is third-party text, so the sheet quotes it rather than presenting it as
guidance:

- a provenance line under the header says the quoted lines are upstream claims, not
  instructions;
- the summary, configuration lines and upstream notes are `>` quotations, and the API
  surface is code spans that a backtick in the harvested text cannot close;
- a carriage return or other control character in a harvested line becomes a space, so no
  harvested text starts a line of the sheet.

Treat a quoted line as the package author's claim and check it before acting on it. The
rendering is pinned by the `TestQuotedNotes_*` tests in
`internal/docdistill/docdistill_test.go` and, through the tool, by
`TestServer_PackageDocs_QuotesUpstreamText` in `cmd/standards-mcp/tools_test.go`.

The catalog behind the tool uses schema `v2` (`CatalogVersion` in
`internal/docdistill/cache.go`). A `v1` catalog was rendered before quoting, so its sheets
are not served: the tool answers not-found, and `praetorctl docs audit` lists the package as
stale, until `praetorctl docs sync` harvests it again. A catalog with an unknown version is
refused with `ErrCatalogVersion` rather than overwritten. `docs audit` counts a package as
documented only when its sheet extracted content (`DistilledDoc.Extracted`).

## Rule explanations state when a rule is not enforced

`explain_rule` answers for every HISS identifier, including the ones with no executable check. Those
answers say so explicitly:

```text
Rule: HISS-05 (Variable Scoping)
Enforcement: NOT ENFORCED. No executable check exists in this repository, and no configured
linter decides this rule.
Failure Action: None today; the rule is advisory until a check is attached.
Adopted repositories: not enforced; adoption generates no check for this rule.
```

The answers come from the HISS rule catalog in `internal/hisscatalog/catalog.go`. The generated
wiki's HISS Matrix (`docs/wiki/HISS-Matrix.md`) and the invariant table of every adopted
`AGENTS.md` render the same catalog, so the tool, the wiki and an adopted harness cannot disagree
about which invariants exist.

An agent asking about a rule needs to know whether anything will stop it. Returning a formal
specification with no enforcement note reads as a gate that exists, which is the defect the HISS-20
coverage catalog was built to remove — a declared mechanism nothing implements. The same honesty
applies here: the tool reports the rule *and* whether it bites.

A rule enforced for some languages and not others names the split rather than one mechanism for
all of them. HISS-01 answers per language: Go decides goto, direct recursion and cycles between
plain functions; Rust, Python, JavaScript, TypeScript, Svelte and shell decide direct recursion
only; C and C++ decide goto only, less the forward cleanup gotos a declared `hiss.exceptions.c_goto_cleanup` accepts
([declared HISS exceptions](../adoption.md#what-adoption-reads-before-it-writes)). Each
line matches a claim in `.config/hiss/coverage.yaml`, and
`TestServer_ExplainRuleHISS01ScopesEnforcementPerLanguage` in `cmd/standards-mcp/server_test.go`
fails if the answer drifts back to a universal claim.

The `Enforcement` line describes praetor's own mechanism. Every answer ends with an
`Adopted repositories:` line that states what an adopted repository gets instead: the check and the
generated stages that run it, or `not enforced; adoption generates no check for this rule.` For
HISS-01 the line starts `'praetorctl audit' HISS scan in verify-all + lefthook pre-commit/pre-push:
Go 'goto', recursion + plain-function call cycles; Rust, Python direct recursion; C 'goto'`. The
tool answers for no particular repository, so it names every pipeline a full adoption generates
and adds that the check holds only where adoption generated that pipeline. An adopted `AGENTS.md`
table credits only the pipelines its own run generated. Both read the `Adoption` field of the
same catalog entry ([`internal/hisscatalog/adopted.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/hisscatalog/adopted.go)),
so they cannot disagree (`TestAdoptedExplanation_Positive_StatesAdoptedEnforcement` and
`TestAdopted_Boundary_FollowsGeneratedPipelines` in `internal/hisscatalog/adopted_test.go`).

## Package docs answer the same way twice

`standards_package_docs` resolves a package name through one selection rule, shared with
`praetorctl docs lookup` (`DocCatalog.Lookup`, `internal/docdistill/types.go`):

1. an exact `package_name` match wins;
2. a path-suffix match (`yaml.v3` for `gopkg.in/yaml.v3`) answers only when nothing matches
   exactly, so a suffix can never beat a package the caller named in full;
3. when the catalog holds the same package at several versions, the greatest catalog key
   (`name@version`) in sorted order wins. That order is lexical, not semantic: probe a
   specific version by asking for the catalog entry rather than expecting semver ordering.

Before this rule the tool ranged the catalog map, so two calls against one cache could
return different versions and a suffix match could win over an exact one
(`internal/docdistill/catalog_lookup_test.go` pins all three points).

The fact tallies printed by `standards_hindsight_optimize` and by `praetorctl hindsight
distill|audit` are sorted by category for the same reason: two runs over an unchanged
repository produce identical output, so a diff between them shows a real change.

## Hindsight distillation names the sources it could not read

`standards_hindsight_optimize` and `praetorctl hindsight distill` harvest facts from four
sources (`workspaceSources`, `internal/hindsight/distiller.go`):

| Source | On failure |
| --- | --- |
| bug ledger (`.workingdir/BUGS.md`) | required: the run fails and the cache is not written |
| flavor (`flavor.Resolve`) | optional: recorded as a warning |
| dedupe scan | optional: recorded as a warning |
| package docs (`.workingdir/docs/catalog.json`) | optional: recorded as a warning |

Each optional failure lands in `DistillationReport.Warnings` with the source name, and the
remaining sources still run. The MCP tool then heads its result `Partially distilled` instead
of `Successfully distilled`, and both surfaces print one `Warning:` line per failed source.
When sources failed and nothing was harvested, the run is an error, so an empty result never
replaces a populated `.workingdir/memory/distilled.json`.

Tests: `internal/hindsight/distiller_warnings_test.go`, `TestServer_HindsightOptimizeNamesFailedSources`
(`cmd/standards-mcp/tools_test.go`) and `cmd/standardsctl/hindsight_cli_test.go`.
