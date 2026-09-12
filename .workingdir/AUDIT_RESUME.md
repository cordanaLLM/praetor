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

G07b integration is currently in progress. G06 preserves both G05's context,
path, permission and unknown-coverage safeguards and G06's scan/migration
corrections. The scanner reports limit breaches, migration refuses branch resets,
and failed mutations return errors with partial results. G07a removes magic-token
network bypasses, retaining target confirmation, opt-in remote sync and pinned
receipt tests. Oversized MCP schemas now fail rather than losing fields.
G07b/G08/G09/G10 are still pending; merge only the committed G09 core.

The permission regression was also present in the shared wave-0 helper, beyond
the rejected G09 patch. `fix/secure-write-permissions` at `c142ec4` fixes it in
isolation: requested modes act as ceilings, and metadata checks/tightening happen
before truncation. Race tests, lint and gosec pass for the changed package;
cross-user devcontainer fixtures fail on the original helper and pass on the fix.
Integrate this commit after the first batch. Its full gate still reports existing
lint failures elsewhere; no integrated full-gate pass has been established.

Full logs, reproduction binaries and hashes are under `codex-continuation/`
within the evidence root above. Keep the distinction between branch-reported
fix counts, integrated regression coverage, and independently closed findings.

### Original workflow entry points

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
