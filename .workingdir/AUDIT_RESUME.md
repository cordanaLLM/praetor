# Deep Audit & Fix-All — Resume State

> Durable handoff for the 2026-09-11/12 multi-agent deep audit of this repository.
> Any session can continue from this file alone. Update it whenever a wave completes.

## Where the evidence lives

Everything outside the repo is under **`~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit/`**
(persistent; the `/tmp` scratchpad was destroyed by a reboot mid-run and must not be used again).

| Path (relative to that dir) | What it is |
| :--- | :--- |
| `report.md` | Final audit report, 18,865 lines, 9 subsystem sections + ledger + appendix |
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
- [~] **Wave A** RUNNING: 22 Go fix groups in isolated worktrees (`wf2-run.mjs`, run `wf_d9746de1-7cf`), then merge
- [ ] **Wave B**: 9 non-Go groups (CI, packaging, config, templates, docs, editors, license)
- [ ] **Wave C**: praetorctl-only rename + `compile-context` regeneration
- [ ] **Wave D**: verification agents (gatekeeper/fuzzer/auditor/packager/dogfooder) + fix-review refuters + repair loop
- [ ] Close out: resolve fixed bugs in the ledger, push branch, open PR

## How to resume

```bash
S=~/.claude/projects/-home-kilian-dev-cordanaLLM-praetor/audit
cat "$S/fold_summary.txt"                 # current finding counts
python3 "$S/fold.py"                      # regenerate findings.json + fixgroups + wf2-args.json (needs S env)
sh "$S/bugs.sh"                           # register any not-yet-ledgered findings (incremental)
```

Then launch the fix workflow with `Workflow({scriptPath: "$S/wf2-fix.mjs", args: {...wf2-args.json, only: ["A"]}})`.
Waves are independent; `only` accepts `["0"],["A"],["B"],["C"],["D"]`.

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
