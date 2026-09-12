# Deep Audit & Fix-All — Resume State

> Durable handoff for the 2026-09-11/12 multi-agent deep audit of this repository.
> Any session can continue from this file alone. Update it whenever a wave completes.

## Where the evidence lives

Everything outside the repo is under **`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/`**
(persistent; the `/tmp` scratchpad was destroyed by a reboot mid-run and must not be used again).

| Path (relative to that dir) | What it is |
| :--- | :--- |
| `report.md` | Final audit report, 18,865 lines, 9 subsystem sections + ledger + appendix. Private page: https://claude.ai/code/artifact/4a0fbb05-4a94-45e0-ae99-3cf60dbcecc4 |
| `report/section-*.md` | The nine per-subsystem sections |
| `findings.json` | Kept findings (the fix input) |
| `findings-all.json` | Every finding incl. refuted/duplicate |
| `fixgroups/<group>.json` | Findings split per fix group |
| `wf2-args.json` | Launch args for the fix workflow |
| `verified/verdicts-*.json`, `verified/critic-*.json` | Raw verifier verdicts |
| `citation_mismatches.txt` | 56 finding ids whose cited line is wrong |
| `prior_logs.json` | Run-by-run history incl. every interruption |
| `wf1b-verify.mjs`, `wf1c-report.mjs`, `wf2-fix.mjs` | The workflow scripts |
| `fold.py`, `recover.py`, `rebuild_tools.sh`, `build_manifest.py`, `gen_wf1c.py` | Regeneration scripts |
| `recover.py` | Rebuilds all state from the workflow journal if scratch is lost again |

## Decisions already made by the owner (do not re-litigate)

1. License is **EUPL-1.2** everywhere.
2. The only binary name is **praetorctl** (plus `praetor-mcp`, `praetor-lsp`). No `standardsctl` alias. Config filenames `.standards*` stay.
3. Receipts: pinned public key in `.standards.yaml`, private key from `PRAETOR_RECEIPT_KEY` or `~/.config/praetor/receipt.key`, payload = hash of the real gate output.
4. gosec runs with **zero exclusions**; every hit is fixed or carries a per-line `#nosec Gxxx -- <reason>`.
5. Fix **all** findings, not just high severity.
6. Findings land in `.workingdir/BUGS.md`; the full report stays outside the repo.

## Audit result (complete)

630 kept findings: **1 critical, 59 high, 270 medium, 285 low, 15 info**.
Categories: correctness 273, tests 91, self-governance 77, security 53, dead-surface 43, drift 24, config 23, ci 17, docs 17, supply-chain 12.
Plus 90 from two completeness-critic rounds, now folded in: **716 kept findings total**
(1 critical, 62 high, 299 medium, 336 low, 18 info), all registered as `BUG-001`..`BUG-720`.

Method: 37 units x 2-3 lenses (~70 finders) -> dedupe -> canaried 2-lens verification -> adversary pass -> 2 critic rounds.
No planted false finding was ever accepted across 58 verification batches.

### The five things that matter most

1. The enforcement the product sells does not run (no complexity gate; gosec excluded every rule that fires; `make lint` is `go vet`; semgrep/gitleaks named but never executed).
2. `compile-context --verify` cannot detect drift: the transpiler emits the six vendor files as Go string constants and never reads `AGENTS.md`.
3. The Ed25519 Exit-0 receipt gates nothing (ephemeral key in its own receipt, gitignored, read by nothing).
4. Mutating paths destroy real state: `SyncToBacklog` (the one critical, BUG-001), `gc`, `topology clean`, `harvest --dedupe`, `issue reconcile`, `adopt --force`.
5. Commands report success for work never performed (`internal/builder` imports no `os/exec`; `standards_audit` prints "100% Compliance" after four `stat` calls).

## Progress

- [x] Audit complete; report written
- [x] 630 findings registered as `BUG-001`..`BUG-630` in `.workingdir/BUGS.md`
- [x] Branch `audit/deep-audit-2026-09-11` created; ledger committed (`d64e6e1`)
- [x] **Wave 0** (shared primitives) done on branch `fix/wave0`, commit `2ba31cc`, 34/35 fixed.
      Verified independently: gofmt/build/vet clean, tests pass with `-race`, and `internal/util`,
      `internal/lockdown`, `internal/gating` have **0** gosec issues with exclusions removed.
      Adds `util.ConfinePath/WriteFileSecure/MkdirSecure/ValidateExecArg`, `lockdown.LoadSigningKey/PinnedPublicKey`,
      `praetorctl gate keygen|verify`, lock-digest validation, v2 `.golangci.yml`, empty `.gosec.json`.
- [x] Merged `fix/wave0` into `audit/deep-audit-2026-09-11` (`cb086c9`)
- [x] Folded the critic findings in; fix groups regenerated; 90 new bugs registered
- [~] **Wave A** in two halves of 11 groups (`wf2-run.mjs` with `args.subset`); each half is followed by its own merge.
      Half 1: G01 G02 G03 G04 G05 G06 G07a G07b G08 G09 G10. Half 2: G11 G12 G13 G14 G15 G17 G18 G19 G20 G21 G22.
      First attempt (run `wf_d9746de1-7cf`) died on the previous account's spend limit with zero work done; its empty worktrees/branches were removed.
- [ ] **SEQUENCE CHANGE (user direction 2026-09-12): deduplicate and unify BEFORE the remaining fix groups.**
      After the half-1 merge: hold wave A half 2 (G11-G22) and the structure-sensitive non-Go groups (N04 config,
      N05 templates, N06 agent surfaces); run the ADR-0009 structure/dedupe design (workflow `wf6-structure-dedupe.mjs`),
      then implement the dedupe/unification refactor on the audit branch (single home for token resolution, HTTP
      client, HISS engine, templates via go:embed, persona projection, config reader; delete orphans), then re-fold
      `findings.json` against the new tree (many dead-surface/drift/duplicate findings become moot), then run the
      remaining groups on what is left. Safe non-Go groups (N01 CI, N03 packaging/license, N07 editors, N08/N09 docs)
      may run after the half-1 merge; lefthook (`fix/lefthook`) merges first of all.
- [ ] **Wave B**: 9 non-Go groups (CI, packaging, config, templates, docs, editors, license)
- [ ] **Wave C**: praetorctl-only rename + `compile-context` regeneration
- [ ] **Wave D**: verification agents (gatekeeper/fuzzer/auditor/packager/dogfooder) + fix-review refuters + repair loop
- [ ] Close out: resolve fixed bugs in the ledger, push branch, open PR

## How to resume

### Codex continuation — 2026-09-12

The latest Claude session is `30d48235-2c67-471a-8f6c-6d7d78ccc99b`.
It stopped on provider usage limits at 14:29 UTC, before either first-batch merge
attempt did any work. The latest owner direction is blockers-first dispatch:
Lefthook first, integrate half 1, then deduplicate/unify before wave 2.

- G01–G08 and G10 have ten successful workflow results, totaling 234 claimed
  fixes. G09 has a committed HISS/LSP change (`e6e9b43`) but no successful
  workflow result. None of these eleven branches was merged at recovery time.
- The Lefthook workflow's "completed" notification covered research/design only:
  its implementation and proof were null. Implementation resumes on
  `fix/lefthook`; do not treat the old notification as shipping evidence.
- G09's six-file uncommitted cleanup is preserved in the original worktree and
  in `audit/codex-continuation/g09/interrupted.patch` below the evidence root.
  An independent review found permission widening, silent truncation at the new
  flat-directory copy cap, and cancellation falling through to hook installation.
  Hold this patch out of integration; reassess useful pieces after G01/G10 land.
- The copied G09 worktree passed targeted tests and the full race suite, but
  `make verify-all` failed at lint with 140 findings. Full output is retained as
  `audit/codex-continuation/g09/verify-all.log`. Passing tests do not refute the
  review findings, which lack existing regression coverage.
- [ADR-0009](../docs/adr/0009-structural-unification.md) now records the proposed
  consolidation milestones and the previously accepted constraints. No structural
  implementation milestone is complete yet.

Recovery provenance (base heads and patch SHA-256) and a branch/file inventory
are in `audit/codex-continuation/recovery.json` and `branch-inventory.json`.
The old untracked `.standards-receipt.json` predates this audit and is not a
receipt for the current branch or verification result.

#### Verified integration checkpoints

| Checkpoint | Commit | Evidence |
| :--- | :--- | :--- |
| Recovery and ADR-0009 proposal | `c20d49d` | Transcript, branch inventory and preserved G09 patch |
| Lefthook implementation | `94a6af0` | Staged-index and exact-commit snapshots, behavioral tests, installed hooks |
| G01 adoption/config | `a4508b5` | Targeted package tests and commit hooks pass |
| G02 audit/baseline | `20fb300` | Audit tests plus 25 hook behavioral tests pass; frozen base and touched paths supplied |
| G03 CLI safety | `a5fe3b4` | CLI, adoption, harvester, state and needs tests pass |
| G04 command parsing | `a476ef7` | Command, harvester, needs and release-track tests pass |
| G05 capability/migration | `a9a70db` | CLI, needs and adoption tests pass |
| G06 scanning/migration | `90f7a1a` | Needs/adoption race tests pass; integration test size regression corrected during G07a |
| G07a forge validation | `c5ee29b` | CLI, forge, MCP, needs and affected helper race tests pass; workflow syntax checks pass |
| G07b project/milestone | `c9d4d28` | CLI, forge, milestone and util race tests pass; explicit store/backlog capacity failures preserve existing data |
| G08 MCP runtime/audit | `ac6e14d` | CLI, MCP, config and affected helper race tests pass; fresh-binary wire review passes 61 checks in 11 sessions |
| Development MCP and canonical context | `2fee173` | Native Codex calls, fresh-build wire probes and 14 client/provenance tests pass; all six outputs preserve AGENTS body |
| Secure-write permissions | `0a26497` | Integrated util, adoption, milestone, harvester, gating and CLI race tests pass |
| G09 scanner/LSP | `fac9d09` | Preserved earlier fixes; scanner bounds fail explicitly, including a 1,100-if AST-depth fixture; combined MCP audit wire probes pass |
| G10 harvesting/dogfood | `413c2a5` | Full prepared-branch races pass; combined CLI/MCP/dogfood/harvester/util/adopt races and development MCP preflight pass |
| Checkpoint publication | `0a96f26` | Real origin push passed file/build/race gates; 35 hook regressions preserve strict promotion checks |
| Transcript replay and MCP ingestion | `ba75796` | Full original source replay and duplicate-free retry; CLI/MCP pagination/readback and independent cursor/parser review pass |
| Public repository loop and real lock generation | `f59190c` | Fresh merged-source MCP verifies pinned Cobra/Flask through two stable applications; full atomic race coverage 79.8% |
| Configurable owner regression | `5b21260` | Audit heading test derives owner/name from manifest; public and private owner cases pass |

Wave A half 1 is integrated. G06 preserves both G05's context,
path, permission and unknown-coverage safeguards and G06's scan/migration
corrections. The scanner reports limit breaches, migration refuses branch resets,
and failed mutations return errors with partial results. G07a removes magic-token
network bypasses, retaining target confirmation, opt-in remote sync and pinned
receipt tests. Oversized MCP schemas now fail rather than losing fields.
G08 also confines default and generated-output paths, reports corrupt memory
caches, and shares actual lock pin/digest validation with the CLI. Only the
committed G09 core was integrated; its rejected interrupted WIP remains preserved.
G09 also disables unused parser object resolution and marks AST depth overflow
incomplete, so the scanner cannot certify a truncated analysis as clean.

The user directed development MCP preflight before relying on advertised
capabilities. The new launcher builds the current working tree into temporary
storage, records source/binary hashes, and provides direct JSON-RPC calls plus
fixture-only behavioral probes. Native Codex startup has been exercised through
the app-server without a model request; existing IDE sessions may need reconnect.
Canonical instruction propagation is fixed: the previous compiler ignored the
AGENTS.md body and generated static policy. Reusable wire probes now require actual
canonical body content in all six compiled outputs, valid and invalid audits, and
failure on truncated analysis without baseline writes.

`make verify-all` after the G08/development-MCP changes passes context verification,
the full race suite and CLI audit, then stops at 111 lint findings. Later gates
have not run in that invocation; this is not a passing Exit-0 receipt.

The permission regression was also present in the shared wave-0 helper, beyond
the rejected G09 patch. `fix/secure-write-permissions` at `c142ec4` fixes it in
isolation: requested modes act as ceilings, and metadata checks/tightening happen
before truncation. Race tests, lint and gosec pass for the changed package;
cross-user devcontainer fixtures fail on the original helper and pass on the fix.
It is integrated as `0a26497`. The G09 integration's full gate passes context, all
43 race-tested packages and audit, then stops at 93 lint findings outside its
changed files. No integrated full-gate pass has been established.

#### Current owner priorities

- Publish WIP regularly. The owner explicitly approved `checkpoint/*` destinations
  with snapshot file checks, affected builds and race tests; full CI must run on
  these pushes. Other refs and PR/merge/receipt gates remain strict.
- The first normal push attempts found manifest formatting, a missing local
  Semgrep executable, and incorrect linter package scopes. Formatting is fixed;
  Semgrep 1.177.0 is installed in an isolated environment. Real-tool regressions
  cover the scope fix, including gosec's previous zero-file false success.
- BUG-001/F212 is independently verified fixed and closed. Fifteen fresh-CLI
  fixture commands preserved historical and newly archived task headings; the
  real state audit now passes with 711 remaining open findings.
- Prioritize the automatic loop against public non-owned repositories, plus
  harvesting/memory ingestion and replay of retained `lusoris/praetor` data.
  Preserve original inputs and source revisions; run only in disposable clones,
  retain failure evidence, and expose incomplete/skipped outcomes. No upstream
  repository writes or messages are authorized by this testing work.
- Include each workstation's coding CLI and agent global state, rules, skills,
  session logs, brains, memories and related data. Inventory available sources
  and formats with provenance, preserve originals, and replay sanitized fixtures;
  report unavailable workstations and unsupported formats explicitly.
- Keep `cordanaLLM/praetor` as the public application. The owner wants
  `lusoris/praetor` to be its synchronized operational fork, with maintained
  differences limited to owner repository/workstation configuration. The current
  private repository is a downstream copy (`fork=false`), not a GitHub-linked
  fork. Four owner-derived configuration changes are prepared against the public
  checkpoint; automatic upstream synchronization and rollout stages remain open.
- Configure explicit rollout stages before bot activation: inventory, dry-run,
  proposed fixes, then verified automation. Advancement must use actual evidence;
  a simulated scan, checkpoint push or empty report is not a passing stage.
- Next systems requested: per-task agent selection using capability and measured
  success/cost/latency with escalation, then optimization of local agent/CLI
  rules, skills and context using canonical ownership and replay verification.
  Implement real consumers with each setting; do not add inert configuration.
- Keep the structural proposal and held fix groups pending while these explicitly
  requested publication and replay prerequisites are completed.

#### Remote checkpoint and verification evidence

`checkpoint/deep-audit-2026-09-12` was successfully pushed to origin at `0a96f26`,
and remote SHA readback matched. The actual pre-push checked 264 changed paths,
all affected builds and race tests. The 35-test hook suite includes real remote
pushes, mixed-ref and strict-promotion rejection, compiler/race failures, and
test-only packages that still compile and execute in the race gate.

Hosted CI run `34703959347` ran and failed in `TestBundlePreservesUmask`: its child
left umask 0777 active at coverage shutdown. The fix is integrated as `62edf3e`;
isolated full race/atomic coverage passes at 79.6% (CI floor 65%). An exact clean
checkout full `make -k verify-all` before that test-only fix passed all gates
except 76 lint and 91 gosec findings. Dedupe passed on 175 files/1,181 functions;
the earlier duplicate count included preserved nested worktrees. No complete
Exit-0 gate or signed receipt has been established.

Original corpus and workstation metadata are preserved outside Git under
`codex-continuation/corpus-discovery/DISCOVERY.md`. Gemini's full Praetor transcript
contains 10,863 records; the old 2,000-line extractor stopped before its first
`lusoris/praetor` checkout reference. Raw private payloads remain outside tracked
files. Both implementations are integrated and pushed on the checkpoint branch.
The merged CLI replayed the original source in two pages: 10,830 already-present
events, 33 metadata-only skips, zero new records, and explicit completion. The
initial library replay stored the 10,830 events. Source SHA256 remained
`ccc79794d9e92ca8998291082e9176344ceec3b330797df7be39bf2af33eeae4`.

Fresh merged-source MCP public-loop acceptance on `f59190c` verified pinned
Cobra `adbc8813901bba65827259daa8e22ff94ec1f30e` and Flask
`d73fa1cdcbd8b1465c151db8924ba58b1dd14e35`. Each clone was configured twice with
stable file/directory digests, valid real-content lock pins, synchronized agent
context and no HISS debt growth. This scope does not execute upstream application
tests or builds. Failed pins retain failed checkouts and return a tool error.

Final clean-checkout `make -k verify-all` at `f59190c` has only 76 lint and 91 gosec
failures, unchanged signatures outside the new files. All 43 packages pass race
tests with atomic coverage 79.8%; 16 MCP client tests, 17 wire checks/14 tools,
context, audit, vulnerabilities, flavor, state, topology and 35 hook tests pass.
Dedupe scans 191 files/1,244 functions, but its separate missing-root/dangling-file
false-success regression remains open as BUG-252/F525 (bounds/context: BUG-522).
Full logs and replay counterexamples are in `combined-verification-f59190cc/`.

Workstation inventory covers local Claude, Codex, Gemini, OpenCode, Copilot, Pi,
shared skills and editor stores. Only Antigravity JSONL has the new ingestion
adapter. Other formats, consistent SQLite snapshots, further workstations,
and verified-fact extraction/recall remain explicit work. Raw observations are
never silently treated as verified facts. Owner-fork bot rollout, measured
routing/dispatch and content-aware context optimization remain in `OPEN.md`.

#### Local activation and new consumers — 2026-09-12

`19b4744` adds `make dev-install`: build all three executables from current source,
run the real MCP probe, preserve previous binaries in a private backup and read
back installed hashes. Existing PATH executables were stale standalone copies
from `43c180e`; `make build` alone had not refreshed them. The installed MCP passed
the real probe, and the PATH CLI replayed the original 10,863-record corpus with
10,830 already-present events, 33 metadata skips, zero new events and an unchanged
source hash. Installation metadata lives at `~/.local/bin/.praetor-dev-install.json`.
Nine installer regressions cover backup, rollback, permissions, links and locking.

Codex recognizes the project `praetor-dev` connection. `claude mcp get praetor-dev`
still reports native project approval pending; fresh direct wire calls work.
Existing native server processes retain their startup binary until reconnected.

`b1e2981` integrates `context-optimize` and the read-only MCP
`standards_context_analyze`. Both use explicit bounded inputs and return matching
hashes, aliases and byte counts. Candidates preserve retained document bytes and
are written only to a new private directory; they require review before use.
The selected repository canonical/vendor bundle measures 37,195 input bytes and
5,260 packed bytes. The real four-file global rule/skill set has no duplicates:
30,655 input/payload bytes, 30,963 packed bytes. Neither result establishes live
per-turn token or latency savings. The expanded wire probe has 22 checks/15 tools.

`147903d` integrates offline `models route`: declared task/capability eligibility,
configured token-cost ordering and explicit observation status. Supplied capacity
snapshots reject missing/ambiguous counters; absent observations are never filled
with invented zeroes. The stricter shared config loader requires explicit finite
nonnegative prices, including explicit zero. Existing catalog data is unchanged.
Provider dispatch, telemetry, measured quality/latency, quota reservation and
automatic escalation remain future work. See the routing guide's migration notes.

`50d81bb` supplies `operational sync plan|prepare`, verified against the real owner
checkout and reviewed upstream commits. Preparation creates a separate clone,
preserves ordinary merge ancestry and limits differences to the four existing
owner configuration paths and their declared fields. Unknown upstream manifest
fields survive. Effective Git filters, submodules, replacement/graft ancestry,
configuration drift, and incomplete or dirty candidates fail explicitly. The
prepared clone starts with inert hooks; activate reviewed hooks and run checks
before normal checkpoint publication. Broad repository/workstation overlays,
scheduled synchronization and bot promotion remain later stages.

Combined verification after integration passes all 45 Go packages with races,
22 MCP wire checks, 16 MCP client tests, nine installer tests and the 35 hook tests.
The working-checkout full harness reports 76 existing lint findings and 90 gosec
findings (one fewer after the routing loader change). Its dedupe scan also counts
the preserved nested Claude worktrees as source duplicates: 1,988 scanned files,
10,827 functions, and 1,423 duplicate groups. Those worktrees are evidence and
remain intact; compare an isolated checkout for the ordinary CI scan scope.
The missing-root/dangling-file false-success scanner issue remains separately open.

The clean combined checkout passes the ordinary dedupe scan (215 files, 1,337
functions); its full verification has only the same 76 lint and 90 security
findings, none in the newly added files. Atomic coverage exposed an instrumented
test child writing Go's missing-GOCOVERDIR warning into its asserted stderr.
The test helper now supplies a private temporary coverage directory while keeping
the exact byte assertions and production environment isolation unchanged. Focused
utility race coverage passes at 87.8%; retained full coverage results live under
`combined-local-systems-clean/` and the focused regression under
`operational-fork/sync/coverage-fix/`.

Evidence: `local-dev-install-readback.json`, `installed-cli-original-replay.json`,
`context-global-{cli,mcp}.json`, `context-integrated-mcp-probe.json`,
`context-optimizer/`, `task-routing/`, `router-review/`, `operational-fork/sync/`,
`operational-sync-review/`, and `combined-local-systems-verify.log` under the external
`codex-continuation/` evidence root. Private candidate contents remain outside Git.

Full logs, reproduction binaries and hashes are under `codex-continuation/`
within the evidence root above. Keep the distinction between branch-reported
fix counts, integrated regression coverage, and independently closed findings.

### Original workflow entry points

#### Configured replay suite continuation — 2026-09-12

`03d4b69` integrates the Claude Code JSONL adapter: explicit format selection,
format-bound cursors and event provenance, metadata/thinking exclusions, and
nonblocking Unix source/cache opens. The original 1,986-record Claude session
produces 613 observations, with 1,162 metadata and 211 thinking-only records
explicitly skipped. Private cache readback preserves 229 tool calls, 229 results
and eight tool references. The original source remains unchanged.

The configured `dogfood suite` CLI and `standards_dogfood_suite` MCP tool consume
strict version-1 JSON with 1–8 pinned public/transcript cases. Plan validates
declarations only. Verify runs real public reconciliation and complete transcript
ingestion plus same-cache replay, retaining every case and failure. Embedded
transcript paths obey MCP confinement and public verification requires server
remote opt-in, both checked against the consumed config snapshot. The checked-in
`.config/dogfood/public-suite.json` holds only public inputs; private workstation
configuration stays in the external evidence directory.

Real combined MCP acceptance verifies pinned Cobra and Flask, then the original
Claude and Antigravity inputs: 12,849 records scanned, 11,443 observations stored,
1,406 explicit skips, and zero new records during replay. Source hashes remain
unchanged. Independent review passes 51 CLI and 14 MCP checks, including the
10,001-record page boundary, private modes, continued outcomes after failure,
global case-ID uniqueness, and failed final-report persistence clearing success.

The source MCP probe has 30 checks/16 tools. Full working-checkout verification
has only the existing 76 lint/90 security findings and preserved nested-worktree
dedupe noise. Follow-up review identified malformed JSON surrogate replacement
and arbitrary record-type text leaking through error messages; their bounded fix
is tracked separately before final activation. Evidence is under
`codex-continuation/next-stage/`, including `local-suite.json`,
`original-suite-mcp/`, `suite-review/` and `adapter-review/`. These private files
remain outside Git. Scheduling, agent dispatch, semantic verified-fact memory
and automatic rollout promotion remain later stages.

```bash
S=~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit
cat "$S/fold_summary.txt"                 # current finding counts
python3 "$S/fold.py"                      # regenerate findings.json + fixgroups + wf2-args.json (needs S env)
sh "$S/bugs.sh"                           # register any not-yet-ledgered findings (incremental)
```

Then launch the fix workflow with `Workflow({scriptPath: "$S/wf2-run.mjs", args: {ts, only: ["A"], subset: [<group ids>]}})`
(`wf2-run.mjs` has the groups embedded; regenerate it from `wf2-fix.mjs` + `wf2-args.json` after any fold).
Waves are independent; `only` accepts `["0"],["A"],["B"],["C"],["D"]`; `subset` limits a wave to listed group ids.

### Limit-safety rules (learned the hard way)
- Fix agents commit every 3-5 findings and, on relaunch, resume from their own `fix/<group>` branch if it has commits ahead of the audit branch. A spend-limit hit therefore loses at most a few findings per in-flight agent.
- Run waves in halves (<=11 concurrent agents) and check `/usage` between halves; a hit mid-wave kills every in-flight agent's uncommitted work and the warm context with it.
- The Workflow worktree isolation does NOT start from the audit branch (observed: worktrees created at `36ec6c6`); every fix agent explicitly does `git checkout -B fix/<id> audit/deep-audit-2026-09-11` first.
- The circuit breaker aborts a run after 4 consecutive empty agent results; resume with `resumeFromRunId` once the limit resets.

## Hermetic sandbox (available now, prototype of `praetorctl sandbox run`)

`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/sandbox.sh <repo-or-worktree> [<ref>] -- '<cmd>'`
clones the checkout (uncommitted changes included when no ref is given) into a throwaway directory, runs the
command inside the devcontainer image `praetor-dev:audit` (built from `docker/dev/Dockerfile`) as the invoking
uid with HOME/GOCACHE/GOPATH inside the clone, and deletes everything afterwards. Verified: `go test -race ./...`
and `standardsctl audit` run there without touching this tree. Wave D verifiers and the fix-review refuters
use it; fix agents may use it for `make verify-all` and end-to-end reproductions. Rebuild the image with
`docker build -f docker/dev/Dockerfile -t praetor-dev:audit .` after a reboot if `docker image inspect` fails.

## Hard-won operational notes

- Agents inherit this repo's `CLAUDE.md`, which tells them to run `make verify-all`, `go test ./...` and
  `state sync`. Read-only audit agents **must** be told to ignore it, or they mutate the tree.
- Installed `golangci-lint`/`staticcheck`/`deadcode`/`nilness` are built with Go 1.26 and refuse this
  Go 1.27 tree. Use `go run <tool>@latest`.
- The test suite mutates the repository (`internal/gating/gating_test.go` writes `.standards-receipt.json`).
  Run the full suite in a detached worktree.
- 56 findings (see `citation_mismatches.txt`) cite a wrong or non-existent line. The claim is usually
  still real; re-locate by the snippet, not the line number.
- Only the Fable model tier is capped on this account; Opus and Sonnet are not.
