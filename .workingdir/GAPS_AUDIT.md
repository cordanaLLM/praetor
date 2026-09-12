# Capability gaps and state reconciliation — 2026-09-12

This is the current continuation entry point. The owner requested this audit
before further feature expansion. It supplements the earlier 716-finding audit;
it is not a claim to have reverified every historical finding.

The subsequent [Go package reuse research](../docs/research/go-package-reuse.md)
compares maintained libraries and standard-library alternatives against these
failures, with pinned sources and migration acceptance cases. Research is
complete; dependency adoption remains open.

## Continuation: ledger repair completed

The [ledger integrity checkpoint](../docs/guides/state-ledger-integrity.md) fixes
loss-prone parsing, metadata persistence, ID allocation, confined initialization,
cooperating writer serialization and read-error propagation. All four missing
source findings are recovered as BUG-713..716; existing IDs were preserved.
F392/BUG-714 and F389/BUG-190 are resolved against retained regression evidence.
The ledger now has 716 records, 713 open and three resolved. The audit findings and
counts below are the pre-repair snapshot, not the current ledger totals.

All 48 package race suites and fresh MCP/CLI checks pass. Full verification still
fails lint/security; the in-place dedupe scan additionally counts ignored Claude
worktrees. Remaining ledger work includes question/task mutation integrity, STATE
append concurrency, Git error handling and broader historical reconciliation.

## Original audit snapshot

Audited base: `c4c5a25beb0d3e5f071b54aff934ecc91b26b5f1`, plus the in-flight
NotebookLM preparation and prompt-selection changes. Tested Go source SHA256:
`8cc712d377dd5b398502efd7542c7b7c08207e1da7499d52e510ed033b457dc0`.
Three parallel agents covered state, bots/forge/runtime, and routing/notebooks;
the coordinator checked IDE surfaces, source identity, shared analysis, and
the full verification target. Further IDE and forge implementation was paused.

## Evidence and limits

Retained reports, reproductions, source overlays, snapshot identity and full gate
output are under:
`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/codex-continuation/gaps-20260912/`.
The report files are `praetor-audit-state-20260912.md`,
`praetor-audit-bots-20260912.md`, `praetor-audit-routing-20260912.md`,
`praetor-audit-core-20260912.md`, and `editors-and-integration.md`.
Temporary execution copies are not the durable evidence source.

No live notebook contents were accessed, no bot workflow was dispatched, and no
forge writes, cluster deployments, global plugin installation, or timer changes
were made. A deployment absence below concerns the inspected cluster/application,
not all possible deployments. Unsupported provider methods return errors; that
is safer than fabricated success but still not an implementation.

## Current capability matrix

| Surface | Implemented and exercised | Remaining boundary |
| --- | --- | --- |
| Development MCP | Fresh source handshake and 35 fixture checks pass; 19 tools discovered. New notebook metadata tool also called with a synthetic source bundle. | Each feature needs its own tool acceptance. Discovery does not prove all tools work. Already-running clients can retain an older server. |
| Git hooks | Lefthook installed; all 43 hook tests pass, including rejection controls. Checkpoint pushes have explicit build/race gates. | Full repository lint/security are still red. No signed all-gates-pass claim. |
| Codex lifecycle hooks | Shared PreToolUse bridge implemented and tested. | Last native review showed enabled but untrusted; current trust was not rechecked. No Stop verification hook is configured. |
| VS Code extension | Source exists; it starts an LSP and exposes shell commands. | Not installed in the inspected Code profile. Build fails for missing tsconfig. MCP settings have no consumer; displayed compliance and host verification are fabricated. |
| Other IDE integrations | Neovim adapter and generated editor configuration exist. | Neovim runs repository-relative Go commands. JetBrains custom inspection classes have no plugin implementation here. Configuration files are not installed plugins. |
| GitHub forge | HTTP implementation and request tests exist. GitHub CI ran for the audited commit and failed at lint. | CLI callers directly construct GitHub; generic factory unused. Source-read failures can still yield successful issue-reconcile output. |
| Gitea/Forgejo and GitLab | Common interface and negative unsupported-method tests exist. | All seven enforcement methods are unimplemented. Nonempty token validation is not endpoint authentication. |
| Hosted bot | Workflow/deployment assets exist. | No App/webhook worker found; named Argo application and namespace workloads absent in the inspected cluster. Adoption workflow has zero recorded runs and authorization/ref-handling gaps. |
| Local dogfood and repair | Both user timers are active/waiting; shared repair planning and bounded provider execution exist. | Timer activity is not evidence every dataset, provider, repository stage or promotion path succeeds. |
| Task routing | Selection is called by CLI and repair planning. | Static model sync, unsupported discovery paths, ignored filters and unenforced governance fields remain. |
| Prompt optimization | New offline selector compares supplied metrics; race tests and package lint pass. | No automatic candidate/evaluator/provider/promotion loop. Evidence is caller-declared. Exact-case JSON ambiguity remains. |
| Notebook preparation | New local bundle preparation, template output and structural citation checks pass synthetic tests. | Connector dependency/auth not installed or exercised; generation and semantic review remain external. New CLI features are absent from installed binaries. |

Installed local binaries still identify Go source `d6a9caff625f082cbbab4b0db06542619e34a94a326695ff9842a233e7a40e57`.
Do not describe the in-flight notebook/prompt code as locally activated merely
because the development server can build it.

## Findings to repair first

1. **Ledger parsing can lose findings and conceal a P0.**
   `internal/state/bugs.go:108` rejects pipe-containing titles, while the writer
   emits them without escaping. `ListBugs` silently omits such rows and subsequent
   save operations can discard them. Audit/sync also discard ledger read errors
   (`internal/state/audit.go:49`, `internal/state/state.go:71`). A temporary P0
   fixture with a pipe in its title audits as valid with zero bugs. The retained
   716-finding source contains four IDs absent from the 712-row ledger:
   **F5, F392, F485, F634**. F392 describes this parser defect itself.
   Existing link: BUG-190 for read-error handling; preserve the missing source
   records in evidence before adding/restoring ledger rows. Fix lossless parsing,
   validated writes and explicit read errors before bulk reconciliation.

2. **State summaries mix historical findings with current completion.**
   The current 711-open count is a count of ledger rows, not 711 currently
   reproduced failures. Completed audit/registration tasks were still unchecked;
   those task statuses are corrected in OPEN. The old resume heading's claims
   about completed gates and total registered IDs must be read as history.
   `state status` labels the current observation time as "Last Synced"
   (`cmd/standardsctl/state.go:154,162`). STATE sync does not persist the dirty
   count/state hash described by the harness. Cadence metadata is stale; do not
   refresh its timestamp as though a sweep ran. Reconcile each bug with code and
   a retained acceptance result after repairing the parser; do not mass-close it
   because its fix branch was merged.

3. **IDE success and integration claims exceed runtime behavior.**
   `editors/vscode/src/extension.ts:66,132` fabricates audit/host success. The
   contributed MCP settings are unread (`package.json:65`), no MCP provider is
   registered, `tsc -p editors/vscode/` fails TS5057, and the runtime language
   client is listed only as a development dependency. JetBrains XML names
   unimplemented inspection classes. Existing BUG-589, BUG-590, BUG-630,
   BUG-632, BUG-656, BUG-673. Repair one buildable adapter against shared services
   and verify actual host activation before expanding other IDE adapters.

4. **Bot assets do not supply a bot lifecycle.**
   `serve` provides probes/local scanning, not event ingestion or a worker.
   Checked-in Docker/Helm artifacts do not select the daemon command or a
   consistent binary artifact. The adoption comment workflow lacks actor
   authorization and uses the wrong event path for PR checkout. The composite
   action interpolates inputs into shell and selects an unpinned binary.
   Fix authorization, immutable source/ref identity, argument handling and
   read-only acceptance before granting a worker write stages. Correct the
   unconditional `cordana-standards[bot]` assurance in the harness through
   canonical AGENTS compilation when that repair is performed.

5. **Forge and discovery errors can become successful empty results.**
   A refused loopback endpoint makes `issue reconcile --dry-run` warn, process
   zero issues and exit zero (`cmd/standardsctl/issue.go:173,254`). Model sync
   similarly swallows discovery errors and reports a static 35-model catalog as
   live/verified. Its filters are unused, vLLM is skipped, and both `qwen3:7b`
   and `qwen3:72b` classify as midweight. Sync replaces operator routing config.
   Failed input acquisition must propagate as incomplete/error, not become a
   successful no-op or trigger writes from a partial graph.

6. **Shared language and interfaces have not produced shared behavior.**
   CLI and MCP genuinely share adoption logic. LSP analysis remains a separate
   implementation (`cmd/standards-lsp/server.go:376` vs `internal/hiss`). Editor
   files and generator literals have separate copies; forge and milestone paths
   construct transports independently. `NewForge` has no production callers.
   Follow ADR-0009: explicit constructor dependencies, one policy/config
   resolution path, shared analysis with protocol adapters, and template families
   migrated with output-equivalence tests. Do not add a DI framework or extract
   another package merely to rename duplication. Do not remove an apparently
   unused exported API without checking external/config/test consumers.

   Executed parity fixtures confirm the disagreement: direct recursion and a
   condition-only loop produce 0 scanner / 1 LSP finding; goto produces 1 / 0;
   a 66-LOC function under the current defaults produces 1 / 0. Preserve the
   union of checks and explicit completeness when sharing the engine. Existing
   BUG-309, BUG-483 and BUG-302 cover these mechanism classes.

7. **Automation inputs still lack complete evidence contracts.**
   New prompt selection checks supplied measurements but does not execute or
   attest them. The shared JSON decoder accepts case aliases such as `quality`
   plus `Quality`, allowing a later field to replace the first. Notebook
   citation validation checks source hashes and quoted substrings, not semantic
   entailment or task dependency correctness. Keep manual-review/selection-only
   labels and fix schema ambiguity before connecting automatic promotion.

8. **Synthetic artifacts are counted as verified execution.**
   An absent dependency README becomes metadata-only text and then 100% documented
   coverage (BUG-175). Canary failure output is saved as a `.patch` that
   `git apply --check` rejects (BUG-158/167); applying it changes the dependency
   version before failing (BUG-160). Dry-run certifies a nonexistent repository
   (BUG-613). All were reproduced in temporary fixtures. Use separate unavailable,
   planned, executed, failed and verified states; validate candidates before
   owner-tree writes. Keep diagnostics distinct from executable patches.

## Repair order and acceptance

| Order | Bounded work | Exit evidence |
| --- | --- | --- |
| 1 | Ledger integrity and missing-source recovery | Pipe/newline/escape round trips, malformed and unreadable ledger failures, no lost rows or reused IDs, P0 remains blocking; reconcile source IDs before changing totals. |
| 2 | Shared policy, analysis, transport and generated assets per ADR-0009 | Same fixture produces equivalent core decisions through CLI/MCP/LSP; injected clients/clocks/execution dependencies have explicit bounds; replaced copies removed only after consumer tests pass. |
| 3 | Forge input coverage and workflow/runtime correctness | Failed fetch cannot report success or apply partial state; authorized exact-SHA event fixture reaches a read-only worker; unsupported providers remain explicit. |
| 4 | One installed IDE adapter and one configured owner-repo bot stage | Build/package/startup acceptance plus live tool invocation and source identity; no status inferred from settings; owner configuration remains separate from public application defaults. |
| 5 | Ingestion, evaluation, selection, repair and replay loop | Real retained data provenance, reproducible held-out evaluation, provider settings/cost/latency, unchanged acceptance cases, bounded promotion and rollback. Expand public-repo/workstation coverage afterward. |

## Verification of this checkpoint

`make -k verify-all` ran in an isolated snapshot of the in-flight code and exited
**2**. All **48 Go package race suites** passed, as did Python connector tests,
development MCP fixtures, compiler/audit checks, hook tests and the other completed
targets. **Lint and security failed**. Public checkpoint `ebd5184` also passed
hosted race/coverage checks and then failed lint in
[CI run 34713336249](https://github.com/cordanaLLM/praetor/actions/runs/34713336249).
Security reported 90 findings locally. Lint includes
six diagnostics in the new notebook/CLI code as well as existing debt; those new
diagnostics remain open, so this checkpoint is not release-ready. Full output is
`verify-all.log` in the evidence directory. A separately compiled VS Code check
failed TS5057; the extension was not installed or activated.

The only additional implementation completed during audit startup was the bounded
prompt metric-presence fix: omitted/null metrics now fail, explicit zero/false
remain legal, and its package passes race/lint checks. Notebook/prompt code is
preserved as unfinished checkpoint work. No receipt, active deployment, completed
provider optimizer or global all-gates-pass result is claimed.
